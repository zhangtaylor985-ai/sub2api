package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/sessiondelivery"
)

const defaultSpoolMaxBytes int64 = 4 << 30

func main() {
	if err := run(); err != nil {
		log.Fatalf("sessionctl: %v", err)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return usageError()
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	switch os.Args[1] {
	case "migrate":
		return runMigrate(ctx, os.Args[2:])
	case "forward":
		return runForward(ctx, os.Args[2:])
	case "spool-status":
		return runSpoolStatus(os.Args[2:])
	case "repair-quarantine":
		return runRepairQuarantine(os.Args[2:])
	case "export":
		return runExport(ctx, os.Args[2:])
	case "validate":
		return runValidate(os.Args[2:])
	case "status":
		return runStatus(ctx, os.Args[2:])
	case "purge":
		return runPurge(ctx, os.Args[2:])
	default:
		return usageError()
	}
}

func runRepairQuarantine(args []string) error {
	flags := flag.NewFlagSet("repair-quarantine", flag.ContinueOnError)
	spoolDir := flags.String("spool-dir", envOr("SESSION_DELIVERY_SPOOL_DIR", "/opt/sub2api/data/session-delivery/spool"), "gateway spool directory")
	spoolMax := flags.Int64("spool-max-bytes", envInt64("SESSION_DELIVERY_SPOOL_MAX_BYTES", defaultSpoolMaxBytes), "spool byte limit")
	secretEnv := flags.String("secret-env", "SESSION_DELIVERY_HMAC_SECRET", "environment variable containing the Session delivery HMAC secret")
	apply := flags.Bool("apply", false, "rewrite eligible records and return them to the pending queue")
	if err := flags.Parse(args); err != nil {
		return err
	}
	secret, err := requiredEnv(*secretEnv)
	if err != nil {
		return err
	}
	spool, err := sessiondelivery.NewSpool(*spoolDir, *spoolMax)
	if err != nil {
		return err
	}
	ids, err := sessiondelivery.NewIDGenerator(secret, nil)
	if err != nil {
		return err
	}
	stats, err := spool.RepairMissingSessionIDSpool(ids, *apply)
	if err != nil {
		return err
	}
	return writeOutput(stats)
}

func runSpoolStatus(args []string) error {
	flags := flag.NewFlagSet("spool-status", flag.ContinueOnError)
	spoolDir := flags.String("spool-dir", envOr("SESSION_DELIVERY_SPOOL_DIR", "/opt/sub2api/data/session-delivery/spool"), "gateway spool directory")
	spoolMax := flags.Int64("spool-max-bytes", envInt64("SESSION_DELIVERY_SPOOL_MAX_BYTES", defaultSpoolMaxBytes), "spool byte limit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	spool, err := sessiondelivery.NewSpool(*spoolDir, *spoolMax)
	if err != nil {
		return err
	}
	stats, err := spool.Stats()
	if err != nil {
		return err
	}
	return writeOutput(stats)
}

func runMigrate(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	dsnEnv := flags.String("dsn-env", "SESSION_DATABASE_DSN", "environment variable containing the Session PostgreSQL DSN")
	if err := flags.Parse(args); err != nil {
		return err
	}
	store, err := openStoreFromEnv(ctx, *dsnEnv)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return err
	}
	return writeOutput(map[string]any{"status": "migrated"})
}

func runForward(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("forward", flag.ContinueOnError)
	spoolDir := flags.String("spool-dir", envOr("SESSION_DELIVERY_SPOOL_DIR", "/opt/sub2api/data/session-delivery/spool"), "gateway spool directory")
	spoolMax := flags.Int64("spool-max-bytes", envInt64("SESSION_DELIVERY_SPOOL_MAX_BYTES", defaultSpoolMaxBytes), "spool byte limit")
	endpoint := flags.String("endpoint", envOr("SESSION_INGEST_ENDPOINT", ""), "sessiond HTTPS base URL")
	endpoints := flags.String("endpoints", envOr("SESSION_INGEST_ENDPOINTS", ""), "comma-separated sessiond HTTPS base URLs")
	secretEnv := flags.String("secret-env", "SESSION_INGEST_SECRET", "environment variable containing the ingest HMAC secret")
	batchLimit := flags.Int("batch-limit", 100, "maximum records per pass")
	concurrency := flags.Int("concurrency", int(envInt64("SESSION_FORWARD_CONCURRENCY", 2)), "maximum concurrent ingest uploads")
	loop := flags.Bool("loop", false, "run continuously")
	interval := flags.Duration("interval", 5*time.Second, "delay between passes in loop mode")
	timeout := flags.Duration("timeout", envDuration("SESSION_INGEST_TIMEOUT", 20*time.Minute), "timeout for one ingest upload")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *concurrency <= 0 || *concurrency > 16 {
		return errors.New("-concurrency must be between 1 and 16")
	}
	secret, err := requiredEnv(*secretEnv)
	if err != nil {
		return err
	}
	spool, err := sessiondelivery.NewSpool(*spoolDir, *spoolMax)
	if err != nil {
		return err
	}
	configuredEndpoints := splitNonEmpty(*endpoints)
	forwarder, err := sessiondelivery.NewForwarder(spool, sessiondelivery.ForwarderConfig{
		Endpoint:    *endpoint,
		Endpoints:   configuredEndpoints,
		Secret:      secret,
		BatchLimit:  *batchLimit,
		Concurrency: *concurrency,
		Timeout:     *timeout,
	})
	if err != nil {
		return err
	}
	for {
		stats, err := forwarder.ForwardOnce(ctx)
		if err != nil {
			if !*loop {
				return err
			}
			log.Printf("Session forward pass failed: %v", err)
		} else if stats.Attempted > 0 || !*loop {
			if err := writeOutput(stats); err != nil {
				return err
			}
		}
		if !*loop {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(*interval):
		}
	}
}

func splitNonEmpty(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func runExport(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	dsnEnv := flags.String("dsn-env", "SESSION_DATABASE_DSN", "environment variable containing the Session PostgreSQL DSN")
	hourValue := flags.String("hour", "", "UTC hour in YYYY-MM-DDTHH or RFC3339 (default: previous closed UTC hour)")
	archiveType := flags.String("archive-backend", envOr("SESSION_ARCHIVE_BACKEND", "local"), "archive backend: local or rclone")
	archiveDir := flags.String("archive-dir", envOr("SESSION_ARCHIVE_DIR", "/var/lib/sub2api/session-delivery/archive"), "local staging archive directory")
	rcloneBinary := flags.String("rclone-binary", envOr("SESSION_ARCHIVE_RCLONE_BINARY", "rclone"), "rclone executable used by the Google Drive backend")
	rcloneRemote := flags.String("rclone-remote", envOr("SESSION_ARCHIVE_RCLONE_REMOTE", ""), "rclone Google Drive destination, for example gdrive:Sub2API/session-delivery")
	tempDir := flags.String("temp-dir", envOr("SESSION_EXPORT_TEMP_DIR", "/var/lib/sub2api/session-delivery/export-tmp"), "export work directory")
	allowCurrentHour := flags.Bool("allow-current-hour", false, "allow current UTC hour (tests only)")
	purgeAfterVerify := flags.Bool("purge-after-verify", envBool("SESSION_AUTO_PURGE_ENABLED", false), "drop the ingest-hour partition after durable archive verification")
	drain := flags.Bool("drain", false, "export oldest pending closed hours instead of one exact hour")
	maxHours := flags.Int("max-hours", 48, "maximum closed hours to process in drain mode")
	settleDelay := flags.Duration("settle-delay", envDuration("SESSION_EXPORT_SETTLE_DELAY", 2*time.Hour), "minimum delay after an ingest hour closes before drain mode can export it")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *drain && strings.TrimSpace(*hourValue) != "" {
		return errors.New("-drain and -hour cannot be used together")
	}
	if *maxHours <= 0 || *maxHours > 744 {
		return errors.New("-max-hours must be between 1 and 744")
	}
	if *settleDelay < 0 || *settleDelay > 7*24*time.Hour {
		return errors.New("-settle-delay must be between 0 and 168h")
	}
	store, err := openStoreFromEnv(ctx, *dsnEnv)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return err
	}
	backend, err := newArchiveBackend(*archiveType, *archiveDir, *rcloneBinary, *rcloneRemote)
	if err != nil {
		return err
	}
	if *purgeAfterVerify && !backend.Durable() {
		return errors.New("purge-after-verify requires a durable archive backend")
	}
	exporter, err := sessiondelivery.NewExporter(store, backend, sessiondelivery.ExporterConfig{
		PublicModel:      sessiondelivery.DefaultPublicModel,
		TempDir:          *tempDir,
		AllowCurrentHour: *allowCurrentHour,
	})
	if err != nil {
		return err
	}
	if !*drain {
		hour, err := parseHour(*hourValue)
		if err != nil {
			return err
		}
		output, err := exportOne(ctx, store, exporter, backend, hour, *purgeAfterVerify)
		if err != nil {
			return err
		}
		return writeOutput(output)
	}
	results := make([]any, 0, *maxHours)
	cutoff := exportCutoff(time.Now(), *settleDelay)
	for len(results) < *maxHours {
		hour, err := store.NextExportableHour(ctx, cutoff, *purgeAfterVerify)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		output, err := exportOne(ctx, store, exporter, backend, hour, *purgeAfterVerify)
		if err != nil {
			return err
		}
		results = append(results, output)
	}
	return writeOutput(map[string]any{
		"processed":    len(results),
		"hours":        results,
		"cutoff":       cutoff.Format(time.RFC3339),
		"settle_delay": settleDelay.String(),
	})
}

func exportCutoff(now time.Time, settleDelay time.Duration) time.Time {
	return now.UTC().Add(-settleDelay).Truncate(time.Hour)
}

func exportOne(
	ctx context.Context,
	store *sessiondelivery.Store,
	exporter *sessiondelivery.Exporter,
	backend sessiondelivery.ArchiveBackend,
	hour time.Time,
	purgeAfterVerify bool,
) (map[string]any, error) {
	if existing, err := store.GetExportBatch(ctx, hour); err == nil {
		switch existing.Status {
		case "purged":
			return map[string]any{"hour": hour.Format(time.RFC3339), "batch": existing, "purged": true, "action": "already_purged"}, nil
		case "verified":
			if !purgeAfterVerify {
				return map[string]any{"hour": hour.Format(time.RFC3339), "batch": existing, "purged": false, "action": "already_verified"}, nil
			}
			if err := verifyBatchWithBackend(ctx, existing, backend); err != nil {
				return nil, err
			}
			if err := store.PurgeHour(ctx, hour, existing.ArchiveSHA256, true); err != nil {
				return nil, err
			}
			return map[string]any{"hour": hour.Format(time.RFC3339), "batch": existing, "purged": true, "action": "resumed_verified_purge"}, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	result, err := exporter.ExportHour(ctx, hour)
	if err != nil {
		return nil, err
	}
	purged := false
	if purgeAfterVerify {
		if err := store.PurgeHour(ctx, hour, result.Archive.SHA256, true); err != nil {
			return nil, err
		}
		purged = true
	}
	return map[string]any{"hour": hour.Format(time.RFC3339), "export": result, "purged": purged}, nil
}

func verifyBatchWithBackend(ctx context.Context, batch *sessiondelivery.ExportBatch, backend sessiondelivery.ArchiveBackend) error {
	if batch == nil || batch.Status != "verified" {
		return errors.New("Session export batch is not verified")
	}
	if backend == nil || !backend.Durable() {
		return errors.New("verified purge requires a durable archive backend")
	}
	if batch.ArchiveBackend != backend.Name() {
		return fmt.Errorf("configured archive backend %q does not match verified batch backend %q", backend.Name(), batch.ArchiveBackend)
	}
	object := sessiondelivery.ArchiveObject{
		Backend: batch.ArchiveBackend,
		Name:    batch.ArchiveObject,
		SHA256:  batch.ArchiveSHA256,
		Size:    batch.ArchiveSize,
	}
	if err := backend.Verify(ctx, object); err != nil {
		return fmt.Errorf("re-verify durable Session archive before resumed purge: %w", err)
	}
	return nil
}

func newArchiveBackend(backendType, localDir, rcloneBinary, rcloneRemote string) (sessiondelivery.ArchiveBackend, error) {
	switch strings.ToLower(strings.TrimSpace(backendType)) {
	case "local":
		return sessiondelivery.NewLocalArchiveBackend(localDir)
	case "rclone", "google-drive", "google_drive":
		return sessiondelivery.NewRcloneArchiveBackend(sessiondelivery.RcloneArchiveConfig{
			Binary: rcloneBinary,
			Remote: rcloneRemote,
		})
	default:
		return nil, fmt.Errorf("unsupported Session archive backend %q", backendType)
	}
}

func runValidate(args []string) error {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	archivePath := flags.String("archive", "", "path to a Session tar.zst archive")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*archivePath) == "" {
		return errors.New("-archive is required")
	}
	validation, err := sessiondelivery.ValidateArchive(*archivePath, sessiondelivery.DefaultPublicModel)
	if err != nil {
		return err
	}
	return writeOutput(validation)
}

func runStatus(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	dsnEnv := flags.String("dsn-env", "SESSION_DATABASE_DSN", "environment variable containing the Session PostgreSQL DSN")
	hourValue := flags.String("hour", "", "UTC hour in YYYY-MM-DDTHH or RFC3339 (default: previous closed UTC hour)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	hour, err := parseHour(*hourValue)
	if err != nil {
		return err
	}
	store, err := openStoreFromEnv(ctx, *dsnEnv)
	if err != nil {
		return err
	}
	defer store.Close()
	stats, err := store.StatsForHour(ctx, hour)
	if err != nil {
		return err
	}
	output := map[string]any{"hour": hour.Format(time.RFC3339), "stats": stats}
	batch, err := store.GetExportBatch(ctx, hour)
	if err == nil {
		output["batch"] = batch
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return writeOutput(output)
}

func runPurge(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("purge", flag.ContinueOnError)
	dsnEnv := flags.String("dsn-env", "SESSION_DATABASE_DSN", "environment variable containing the Session PostgreSQL DSN")
	hourValue := flags.String("hour", "", "UTC hour in YYYY-MM-DDTHH or RFC3339")
	archiveSHA := flags.String("archive-sha256", "", "verified durable archive SHA-256")
	allow := flags.Bool("allow-purge", false, "explicitly allow dropping the verified hour partition")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*hourValue) == "" {
		return errors.New("-hour is required for purge")
	}
	hour, err := parseHour(*hourValue)
	if err != nil {
		return err
	}
	store, err := openStoreFromEnv(ctx, *dsnEnv)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.PurgeHour(ctx, hour, strings.TrimSpace(*archiveSHA), *allow); err != nil {
		return err
	}
	return writeOutput(map[string]any{"status": "purged", "hour": hour.Format(time.RFC3339)})
}

func openStoreFromEnv(ctx context.Context, dsnEnv string) (*sessiondelivery.Store, error) {
	dsn, err := requiredEnv(dsnEnv)
	if err != nil {
		return nil, err
	}
	return sessiondelivery.OpenStore(ctx, dsn)
}

func parseHour(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Now().UTC().Truncate(time.Hour).Add(-time.Hour), nil
	}
	if hour, err := time.Parse("2006-01-02T15", raw); err == nil {
		return hour.UTC(), nil
	}
	hour, err := time.Parse(time.RFC3339, raw)
	if err != nil || !hour.Equal(hour.Truncate(time.Hour)) {
		return time.Time{}, fmt.Errorf("invalid UTC hour %q", raw)
	}
	return hour.UTC(), nil
}

func writeOutput(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func usageError() error {
	return errors.New("usage: sessionctl <migrate|forward|spool-status|repair-quarantine|export|validate|status|purge> [flags]")
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt64(name string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	var parsed int64
	if _, err := fmt.Sscan(value, &parsed); err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func requiredEnv(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("environment variable name is required")
	}
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("required environment variable %s is empty", name)
	}
	return value, nil
}
