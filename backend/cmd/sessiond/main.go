package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/sessiondelivery"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("sessiond: %v", err)
	}
}

func run() error {
	flags := flag.NewFlagSet("sessiond", flag.ContinueOnError)
	listen := flags.String("listen", envOr("SESSIOND_LISTEN", "127.0.0.1:8091"), "HTTP listen address")
	dsnEnv := flags.String("dsn-env", "SESSION_DATABASE_DSN", "environment variable containing the Session PostgreSQL DSN")
	secretEnv := flags.String("secret-env", "SESSION_INGEST_SECRET", "environment variable containing the ingest HMAC secret")
	tempDir := flags.String("temp-dir", envOr("SESSION_INGEST_TEMP_DIR", "/var/lib/sub2api/session-delivery/ingest-tmp"), "temporary ingest directory")
	maxBodyBytes := flags.Int64("max-body-bytes", envInt64("SESSION_INGEST_MAX_BODY_BYTES", 256<<20), "maximum compressed envelope size")
	maxDecodedBytes := flags.Int64("max-decoded-bytes", envInt64("SESSION_INGEST_MAX_DECODED_BYTES", 544<<20), "maximum decoded envelope size")
	maxConcurrent := flags.Int("max-concurrent", envInt("SESSION_INGEST_MAX_CONCURRENT", 2), "maximum concurrent ingest bodies")
	diskPath := flags.String("disk-path", envOr("SESSION_DISK_PATH", "/var/lib/sub2api/session-delivery"), "filesystem path protected by the disk guard")
	rejectDiskUsage := flags.Int("reject-disk-usage", envInt("SESSION_DISK_REJECT_PERCENT", 75), "reject ingest when disk usage reaches this percent")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	dsn, err := requiredEnv(*dsnEnv)
	if err != nil {
		return err
	}
	secret, err := requiredEnv(*secretEnv)
	if err != nil {
		return err
	}
	removed, err := prepareIngestTempDir(*tempDir)
	if err != nil {
		return err
	}
	if removed > 0 {
		log.Printf("sessiond removed %d stale ingest temp files", removed)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := sessiondelivery.OpenStore(ctx, dsn)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return err
	}
	handler, err := sessiondelivery.NewIngestHandler(store, sessiondelivery.IngestHandlerConfig{
		Secret:          secret,
		TempDir:         *tempDir,
		MaxBodyBytes:    *maxBodyBytes,
		MaxDecodedBytes: *maxDecodedBytes,
		MaxConcurrent:   *maxConcurrent,
		DiskPath:        *diskPath,
		RejectDiskUsage: *rejectDiskUsage,
	})
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              strings.TrimSpace(*listen),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Minute,
		WriteTimeout:      20 * time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	serveErr := make(chan error, 1)
	go func() {
		log.Printf("sessiond listening on %s", server.Addr)
		serveErr <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func prepareIngestTempDir(directory string) (int, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return 0, errors.New("Session ingest temp directory is required")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return 0, fmt.Errorf("create Session ingest temp directory: %w", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0, fmt.Errorf("list Session ingest temp directory: %w", err)
	}
	removed := 0
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), ".ingest-") || !strings.HasSuffix(entry.Name(), ".json.zst") {
			continue
		}
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
			return removed, fmt.Errorf("remove stale Session ingest temp file: %w", err)
		}
		removed++
	}
	return removed, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt64(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
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
