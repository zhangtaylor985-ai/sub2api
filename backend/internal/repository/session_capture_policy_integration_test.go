//go:build integration

package repository

import (
	"context"
	"database/sql"
	"os/exec"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	appmigrations "github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestSessionCapturePolicyMigrationAndRepositoryLifecycle(t *testing.T) {
	ctx := context.Background()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("Docker is unavailable; skipping Session capture policy integration test")
	}
	container, err := tcpostgres.Run(
		ctx,
		"postgres:18.1-alpine3.23",
		tcpostgres.WithDatabase("sub2api_policy_test"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.PingContext(ctx))

	_, err = db.ExecContext(ctx, `
		CREATE TABLE users (id BIGINT PRIMARY KEY, email TEXT NOT NULL);
		CREATE TABLE groups (id BIGINT PRIMARY KEY, name TEXT NOT NULL);
		CREATE TABLE api_keys (
			id BIGINT PRIMARY KEY,
			user_id BIGINT NOT NULL REFERENCES users(id),
			group_id BIGINT REFERENCES groups(id),
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			deleted_at TIMESTAMPTZ
		);
		INSERT INTO users (id, email) VALUES (1, 'owner@example.com');
		INSERT INTO groups (id, name) VALUES (2, 'Delivery');
		INSERT INTO api_keys (id, user_id, group_id, name, status) VALUES
			(10, 1, 2, 'primary', 'active'),
			(11, 1, 2, 'secondary', 'active');
	`)
	require.NoError(t, err)
	migration, err := appmigrations.FS.ReadFile("173_session_capture_policy.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err, "Session capture policy migration must be idempotent")

	repo := &sessionCapturePolicyRepository{sql: db}
	snapshot, err := repo.LoadSessionCapturePolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, service.SessionCaptureModeAll, snapshot.Mode)
	require.Empty(t, snapshot.Policies)

	require.NoError(t, repo.UpdateSessionCaptureAPIKey(ctx, 11, service.SessionCaptureKeyPolicyExclude, 99))
	require.NoError(t, repo.UpdateSessionCaptureMode(ctx, service.SessionCaptureModeSelected, 99))
	snapshot, err = repo.LoadSessionCapturePolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, service.SessionCaptureModeSelected, snapshot.Mode)
	require.Equal(t, service.SessionCaptureKeyPolicyExclude, snapshot.Policies[11])

	require.NoError(t, repo.SetOnlySessionCaptureAPIKey(ctx, 10, 99))
	snapshot, err = repo.LoadSessionCapturePolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, map[int64]service.SessionCaptureKeyPolicy{10: service.SessionCaptureKeyPolicyInclude}, snapshot.Policies)
	items, total, err := repo.ListSessionCaptureAPIKeys(ctx, "primary", 1, 20)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	require.Equal(t, service.SessionCaptureKeyPolicyInclude, items[0].Policy)
}
