package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type sessionCapturePolicyRepository struct {
	sql *sql.DB
}

func NewSessionCapturePolicyRepository(_ *dbent.Client, sqlDB *sql.DB) service.SessionCapturePolicyRepository {
	return &sessionCapturePolicyRepository{sql: sqlDB}
}

func (r *sessionCapturePolicyRepository) LoadSessionCapturePolicy(ctx context.Context) (*service.SessionCapturePolicySnapshot, error) {
	if r == nil || r.sql == nil {
		return nil, errors.New("Session capture policy database is unavailable")
	}
	snapshot := &service.SessionCapturePolicySnapshot{Policies: make(map[int64]service.SessionCaptureKeyPolicy)}
	var updatedBy sql.NullInt64
	if err := r.sql.QueryRowContext(ctx, `
		SELECT mode, updated_at, updated_by
		FROM session_capture_settings
		WHERE id = 1`).Scan(&snapshot.Mode, &snapshot.UpdatedAt, &updatedBy); err != nil {
		return nil, fmt.Errorf("load Session capture mode: %w", err)
	}
	if updatedBy.Valid {
		snapshot.UpdatedBy = updatedBy.Int64
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT policies.api_key_id, policies.policy
		FROM session_capture_api_key_policies AS policies
		JOIN api_keys ON api_keys.id = policies.api_key_id
		WHERE api_keys.deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("load Session API key policies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var policy service.SessionCaptureKeyPolicy
		if err := rows.Scan(&id, &policy); err != nil {
			return nil, fmt.Errorf("scan Session API key policy: %w", err)
		}
		snapshot.Policies[id] = policy
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Session API key policies: %w", err)
	}
	return snapshot, nil
}

func (r *sessionCapturePolicyRepository) ListSessionCaptureAPIKeys(
	ctx context.Context,
	query string,
	page, pageSize int,
) ([]service.SessionCaptureAPIKey, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	query = strings.TrimSpace(query)
	pattern := "%" + query + "%"
	where := `api_keys.deleted_at IS NULL AND (
		$1 = '' OR api_keys.name ILIKE $2 OR users.email ILIKE $2 OR api_keys.id::text = $1
	)`
	var total int64
	if err := r.sql.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM api_keys
		JOIN users ON users.id = api_keys.user_id
		WHERE `+where, query, pattern).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count Session capture API keys: %w", err)
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT api_keys.id, api_keys.name, api_keys.status, users.email,
		       COALESCE(groups.name, ''), COALESCE(policies.policy, 'inherit'),
		       policies.updated_at
		FROM api_keys
		JOIN users ON users.id = api_keys.user_id
		LEFT JOIN groups ON groups.id = api_keys.group_id
		LEFT JOIN session_capture_api_key_policies AS policies ON policies.api_key_id = api_keys.id
		WHERE `+where+`
		ORDER BY api_keys.id DESC
		LIMIT $3 OFFSET $4`, query, pattern, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("list Session capture API keys: %w", err)
	}
	defer rows.Close()
	items := make([]service.SessionCaptureAPIKey, 0, pageSize)
	for rows.Next() {
		var item service.SessionCaptureAPIKey
		var updatedAt sql.NullTime
		if err := rows.Scan(
			&item.ID, &item.Name, &item.Status, &item.UserEmail,
			&item.GroupName, &item.Policy, &updatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan Session capture API key: %w", err)
		}
		if updatedAt.Valid {
			value := updatedAt.Time.UTC()
			item.PolicyUpdatedAt = &value
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate Session capture API keys: %w", err)
	}
	return items, total, nil
}

func (r *sessionCapturePolicyRepository) UpdateSessionCaptureMode(ctx context.Context, mode service.SessionCaptureMode, actorUserID int64) error {
	tx, err := r.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Session capture mode update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var previous service.SessionCaptureMode
	if err := tx.QueryRowContext(ctx, `SELECT mode FROM session_capture_settings WHERE id = 1 FOR UPDATE`).Scan(&previous); err != nil {
		return fmt.Errorf("lock Session capture mode: %w", err)
	}
	if previous == mode {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_capture_settings
		SET mode = $1, updated_by = $2, updated_at = NOW()
		WHERE id = 1`, mode, nullablePositiveID(actorUserID)); err != nil {
		return fmt.Errorf("update Session capture mode: %w", err)
	}
	if err := insertSessionCaptureAudit(ctx, tx, actorUserID, "update_global_mode", 0,
		map[string]any{"mode": previous}, map[string]any{"mode": mode}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Session capture mode update: %w", err)
	}
	return nil
}

func (r *sessionCapturePolicyRepository) UpdateSessionCaptureAPIKey(
	ctx context.Context,
	apiKeyID int64,
	policy service.SessionCaptureKeyPolicy,
	actorUserID int64,
) error {
	tx, err := r.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Session API key policy update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var lockedAPIKeyID int64
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM api_keys WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE`, apiKeyID).Scan(&lockedAPIKeyID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return fmt.Errorf("lock Session capture API key: %w", err)
	}
	previous := service.SessionCaptureKeyPolicyInherit
	var current sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT policy FROM session_capture_api_key_policies WHERE api_key_id = $1`, apiKeyID).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read Session API key policy: %w", err)
	}
	if current.Valid {
		previous = service.SessionCaptureKeyPolicy(current.String)
	}
	if previous == policy {
		return tx.Commit()
	}
	if policy == service.SessionCaptureKeyPolicyInherit {
		if _, err := tx.ExecContext(ctx, `DELETE FROM session_capture_api_key_policies WHERE api_key_id = $1`, apiKeyID); err != nil {
			return fmt.Errorf("clear Session API key policy: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO session_capture_api_key_policies (api_key_id, policy, updated_by)
			VALUES ($1, $2, $3)
			ON CONFLICT (api_key_id) DO UPDATE SET
				policy = EXCLUDED.policy,
				updated_by = EXCLUDED.updated_by,
				updated_at = NOW()`, apiKeyID, policy, nullablePositiveID(actorUserID)); err != nil {
			return fmt.Errorf("upsert Session API key policy: %w", err)
		}
	}
	if err := insertSessionCaptureAudit(ctx, tx, actorUserID, "update_api_key", apiKeyID,
		map[string]any{"policy": previous}, map[string]any{"policy": policy}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Session API key policy update: %w", err)
	}
	return nil
}

func (r *sessionCapturePolicyRepository) SetOnlySessionCaptureAPIKey(ctx context.Context, apiKeyID, actorUserID int64) error {
	tx, err := r.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin exclusive Session API key policy update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var lockedAPIKeyID int64
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM api_keys WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE`, apiKeyID).Scan(&lockedAPIKeyID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return fmt.Errorf("lock exclusive Session capture API key: %w", err)
	}
	var previousMode service.SessionCaptureMode
	if err := tx.QueryRowContext(ctx, `SELECT mode FROM session_capture_settings WHERE id = 1 FOR UPDATE`).Scan(&previousMode); err != nil {
		return fmt.Errorf("lock Session capture mode: %w", err)
	}
	var previousOverrides int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_capture_api_key_policies`).Scan(&previousOverrides); err != nil {
		return fmt.Errorf("count Session capture overrides: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE session_capture_settings
		SET mode = 'selected', updated_by = $1, updated_at = NOW()
		WHERE id = 1`, nullablePositiveID(actorUserID)); err != nil {
		return fmt.Errorf("enable selected Session capture mode: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM session_capture_api_key_policies`); err != nil {
		return fmt.Errorf("clear Session capture overrides: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_capture_api_key_policies (api_key_id, policy, updated_by)
		VALUES ($1, 'include', $2)`, apiKeyID, nullablePositiveID(actorUserID)); err != nil {
		return fmt.Errorf("set exclusive Session capture API key: %w", err)
	}
	if err := insertSessionCaptureAudit(ctx, tx, actorUserID, "set_only_api_key", apiKeyID,
		map[string]any{"mode": previousMode, "override_count": previousOverrides},
		map[string]any{"mode": service.SessionCaptureModeSelected, "policy": service.SessionCaptureKeyPolicyInclude}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit exclusive Session API key policy update: %w", err)
	}
	return nil
}

func insertSessionCaptureAudit(
	ctx context.Context,
	tx *sql.Tx,
	actorUserID int64,
	action string,
	apiKeyID int64,
	previous, next map[string]any,
) error {
	previousJSON, err := json.Marshal(previous)
	if err != nil {
		return fmt.Errorf("encode previous Session capture policy: %w", err)
	}
	nextJSON, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("encode new Session capture policy: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_capture_policy_audit (
			actor_user_id, action, api_key_id, previous_value, new_value
		) VALUES ($1, $2, $3, $4::jsonb, $5::jsonb)`,
		nullablePositiveID(actorUserID), action, nullablePositiveID(apiKeyID), string(previousJSON), string(nextJSON)); err != nil {
		return fmt.Errorf("write Session capture policy audit: %w", err)
	}
	return nil
}

func nullablePositiveID(value int64) any {
	if value > 0 {
		return value
	}
	return nil
}

var _ service.SessionCapturePolicyRepository = (*sessionCapturePolicyRepository)(nil)
