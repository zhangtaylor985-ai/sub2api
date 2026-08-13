package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSessionCapturePolicyRepositoryLoadsImmutableSnapshot(t *testing.T) {
	db, mock := newSessionPolicySQLMock(t)
	repo := &sessionCapturePolicyRepository{sql: db}
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT mode, updated_at, updated_by.*FROM session_capture_settings`).
		WillReturnRows(sqlmock.NewRows([]string{"mode", "updated_at", "updated_by"}).AddRow("selected", now, int64(9)))
	mock.ExpectQuery(`SELECT policies.api_key_id, policies.policy.*FROM session_capture_api_key_policies`).
		WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "policy"}).AddRow(int64(2), "include").AddRow(int64(3), "exclude"))

	snapshot, err := repo.LoadSessionCapturePolicy(context.Background())
	require.NoError(t, err)
	require.Equal(t, service.SessionCaptureModeSelected, snapshot.Mode)
	require.Equal(t, service.SessionCaptureKeyPolicyInclude, snapshot.Policies[2])
	require.Equal(t, service.SessionCaptureKeyPolicyExclude, snapshot.Policies[3])
	require.Equal(t, int64(9), snapshot.UpdatedBy)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionCapturePolicyRepositoryAuditsModeAndKeyMutations(t *testing.T) {
	t.Run("global mode", func(t *testing.T) {
		db, mock := newSessionPolicySQLMock(t)
		repo := &sessionCapturePolicyRepository{sql: db}
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT mode FROM session_capture_settings.*FOR UPDATE`).
			WillReturnRows(sqlmock.NewRows([]string{"mode"}).AddRow("all"))
		mock.ExpectExec(`UPDATE session_capture_settings.*SET mode`).
			WithArgs(service.SessionCaptureModeDisabled, int64(7)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(`INSERT INTO session_capture_policy_audit`).
			WithArgs(int64(7), "update_global_mode", nil, sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		require.NoError(t, repo.UpdateSessionCaptureMode(context.Background(), service.SessionCaptureModeDisabled, 7))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("API key inherit", func(t *testing.T) {
		db, mock := newSessionPolicySQLMock(t)
		repo := &sessionCapturePolicyRepository{sql: db}
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT id FROM api_keys.*FOR UPDATE`).WithArgs(int64(12)).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(12)))
		mock.ExpectQuery(`SELECT policy FROM session_capture_api_key_policies`).WithArgs(int64(12)).
			WillReturnRows(sqlmock.NewRows([]string{"policy"}).AddRow("exclude"))
		mock.ExpectExec(`DELETE FROM session_capture_api_key_policies`).WithArgs(int64(12)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(`INSERT INTO session_capture_policy_audit`).
			WithArgs(int64(7), "update_api_key", int64(12), sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		require.NoError(t, repo.UpdateSessionCaptureAPIKey(context.Background(), 12, service.SessionCaptureKeyPolicyInherit, 7))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestSessionCapturePolicyRepositorySetOnlyIsAtomic(t *testing.T) {
	db, mock := newSessionPolicySQLMock(t)
	repo := &sessionCapturePolicyRepository{sql: db}
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM api_keys.*FOR UPDATE`).WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(42)))
	mock.ExpectQuery(`SELECT mode FROM session_capture_settings.*FOR UPDATE`).
		WillReturnRows(sqlmock.NewRows([]string{"mode"}).AddRow("all"))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM session_capture_api_key_policies`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(3)))
	mock.ExpectExec(`UPDATE session_capture_settings.*mode = 'selected'`).
		WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM session_capture_api_key_policies`).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(`INSERT INTO session_capture_api_key_policies`).
		WithArgs(int64(42), int64(7)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`INSERT INTO session_capture_policy_audit`).
		WithArgs(int64(7), "set_only_api_key", int64(42), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.SetOnlySessionCaptureAPIKey(context.Background(), 42, 7))
	require.NoError(t, mock.ExpectationsWereMet())
}

func newSessionPolicySQLMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}
