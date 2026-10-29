package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

const interruptedDeleteMessage = "上次远端删除在进程退出前未确认结果，请先核对 iCloud 远端状态后再重试"

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("database url is empty")
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, schemaSQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (1, now()) ON CONFLICT (version) DO NOTHING`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) NormalizeInterruptedDeletes(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE mailboxes
		SET remote_delete_status = $1,
		    remote_delete_error = $2,
		    updated_at = now()
		WHERE remote_delete_status = $3
	`, domain.DeleteUnknown, interruptedDeleteMessage, domain.DeletePending)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) NormalizeStalePendingDeletes(ctx context.Context, now time.Time) (int64, error) {
	cutoff := now.Add(-stalePendingDeleteAfter)
	tag, err := s.pool.Exec(ctx, `
		UPDATE mailboxes
		SET remote_delete_status = $1,
		    remote_delete_error = $2,
		    updated_at = $3
		WHERE remote_delete_status = $4
		  AND (remote_delete_at IS NULL OR remote_delete_at < $5)
	`, domain.DeleteUnknown, interruptedDeleteMessage, now, domain.DeletePending, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) AdminCount(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM admins`).Scan(&n)
	return n, err
}

func (s *Store) CreateAdmin(ctx context.Context, admin domain.Admin) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `LOCK TABLE admins IN EXCLUSIVE MODE`); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM admins`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return apperr.New(409, "admin_exists", "管理员已存在", false)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO admins (id, username, password_hash, created_at)
		VALUES ($1, $2, $3, $4)
	`, admin.ID, admin.Username, admin.PasswordHash, admin.CreatedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) GetAdminByUsername(ctx context.Context, username string) (domain.Admin, bool, error) {
	var admin domain.Admin
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, password_hash, created_at
		FROM admins WHERE username = $1
	`, username).Scan(&admin.ID, &admin.Username, &admin.PasswordHash, &admin.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Admin{}, false, nil
	}
	return admin, err == nil, err
}

func (s *Store) CreateAdminSession(ctx context.Context, session domain.AdminSession) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO admin_sessions (token_hash, admin_id, expires_at, created_at)
		VALUES ($1, $2, $3, $4)
	`, session.TokenHash, session.AdminID, session.ExpiresAt, session.CreatedAt)
	return err
}

func (s *Store) GetAdminSession(ctx context.Context, tokenHash string) (domain.AdminSession, bool, error) {
	var session domain.AdminSession
	err := s.pool.QueryRow(ctx, `
		SELECT token_hash, admin_id, expires_at, created_at
		FROM admin_sessions WHERE token_hash = $1
	`, tokenHash).Scan(&session.TokenHash, &session.AdminID, &session.ExpiresAt, &session.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AdminSession{}, false, nil
	}
	return session, err == nil, err
}

func (s *Store) DeleteAdminSession(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM admin_sessions WHERE token_hash = $1`, tokenHash)
	return err
}

func (s *Store) GetSettings(ctx context.Context) (domain.Settings, error) {
	var item domain.Settings
	err := s.pool.QueryRow(ctx, `
		SELECT public_api_enabled, api_key, public_base_url, default_keyword,
		       lease_ttl_minutes, message_retention_days, scheduler_enabled,
		       scheduler_interval_seconds, scheduler_batch, watcher_enabled,
		       watcher_interval_seconds, web_code_sync_enabled, apple_account_api_key
		FROM settings WHERE id = 1
	`).Scan(
		&item.PublicAPIEnabled, &item.APIKey, &item.PublicBaseURL, &item.DefaultKeyword,
		&item.LeaseTTLMinutes, &item.MessageRetentionDays, &item.SchedulerEnabled,
		&item.SchedulerIntervalSeconds, &item.SchedulerBatch, &item.WatcherEnabled,
		&item.WatcherIntervalSeconds, &item.WebCodeSyncEnabled, &item.AppleAccountAPIKey,
	)
	return item, err
}

func (s *Store) SaveSettings(ctx context.Context, item domain.Settings) error {
	if item.LeaseTTLMinutes <= 0 {
		item.LeaseTTLMinutes = domain.DefaultLeaseTTLMinutes
	}
	if item.LeaseTTLMinutes > domain.MaxLeaseTTLMinutes {
		item.LeaseTTLMinutes = domain.MaxLeaseTTLMinutes
	}
	if item.MessageRetentionDays <= 0 {
		item.MessageRetentionDays = 90
	}
	if item.SchedulerIntervalSeconds < 30 {
		item.SchedulerIntervalSeconds = 30
	}
	if item.SchedulerBatch <= 0 {
		item.SchedulerBatch = 1
	}
	if item.SchedulerBatch > 20 {
		item.SchedulerBatch = 20
	}
	if item.WatcherIntervalSeconds < 15 {
		item.WatcherIntervalSeconds = 15
	}
	if strings.TrimSpace(item.DefaultKeyword) == "" {
		item.DefaultKeyword = domain.DefaultKeyword
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE settings SET
			public_api_enabled = $1,
			api_key = CASE WHEN $2 = '' THEN api_key ELSE $2 END,
			public_base_url = $3,
			default_keyword = $4,
			lease_ttl_minutes = $5,
			message_retention_days = $6,
			scheduler_enabled = $7,
			scheduler_interval_seconds = $8,
			scheduler_batch = $9,
			watcher_enabled = $10,
			watcher_interval_seconds = $11,
			web_code_sync_enabled = $12,
			apple_account_api_key = CASE WHEN $13 = '' THEN apple_account_api_key ELSE $13 END,
			updated_at = now()
		WHERE id = 1
	`, item.PublicAPIEnabled, strings.TrimSpace(item.APIKey), strings.TrimRight(strings.TrimSpace(item.PublicBaseURL), "/"),
		strings.TrimSpace(item.DefaultKeyword), item.LeaseTTLMinutes, item.MessageRetentionDays,
		item.SchedulerEnabled, item.SchedulerIntervalSeconds, item.SchedulerBatch,
		item.WatcherEnabled, item.WatcherIntervalSeconds, item.WebCodeSyncEnabled,
		strings.TrimSpace(item.AppleAccountAPIKey))
	return err
}

func (s *Store) EnsureAPIKey(ctx context.Context, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE settings SET api_key = $1, updated_at = now() WHERE id = 1 AND api_key = ''`, apiKey)
	return err
}

func (s *Store) ListAccounts(ctx context.Context) ([]domain.Account, error) {
	rows, err := s.pool.Query(ctx, accountSelect+` ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectAccounts(rows)
}

func (s *Store) GetAccount(ctx context.Context, id string) (domain.Account, bool, error) {
	rows, err := s.pool.Query(ctx, accountSelect+` WHERE id = $1`, id)
	if err != nil {
		return domain.Account{}, false, err
	}
	defer rows.Close()
	items, err := collectAccounts(rows)
	if err != nil || len(items) == 0 {
		return domain.Account{}, false, err
	}
	return items[0], true, nil
}

func (s *Store) CreateAccount(ctx context.Context, item domain.Account) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO accounts (
			id, label, apple_id, apple_password, proxy_url, session_json,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, '{}'::jsonb, $6, $6)
	`, item.ID, item.Label, item.AppleID, item.ApplePassword, item.ProxyURL, item.CreatedAt)
	if isUnique(err) {
		return apperr.New(409, "account_exists", "账号已存在", false)
	}
	return err
}

func (s *Store) UpdateAccount(ctx context.Context, item domain.Account, updatePassword bool) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE accounts SET
			label = $2,
			apple_id = $3,
			apple_password = CASE WHEN $4 THEN $5 ELSE apple_password END,
			session_json = CASE
				WHEN lower(btrim(apple_id)) IS DISTINCT FROM lower(btrim($3)) THEN '{}'::jsonb
				ELSE session_json
			END,
			updated_at = $6
		WHERE id = $1
	`, item.ID, item.Label, item.AppleID, updatePassword, item.ApplePassword, item.UpdatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(404, "account_not_found", "账号不存在", false)
	}
	return nil
}

func (s *Store) SaveAccountPatch(ctx context.Context, item domain.Account, updatePassword, updateProxy bool, proxyURL string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE accounts SET
			label = $2,
			apple_id = $3,
			apple_password = CASE WHEN $4 THEN $5 ELSE apple_password END,
			session_json = CASE
				WHEN lower(btrim(apple_id)) IS DISTINCT FROM lower(btrim($3)) THEN '{}'::jsonb
				ELSE session_json
			END,
			proxy_url = CASE WHEN $6 THEN $7 ELSE proxy_url END,
			updated_at = $8
		WHERE id = $1
	`, item.ID, item.Label, item.AppleID, updatePassword, item.ApplePassword, updateProxy, strings.TrimSpace(proxyURL), item.UpdatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(404, "account_not_found", "账号不存在", false)
	}
	if updateProxy {
		message := "账号代理已更新"
		if strings.TrimSpace(proxyURL) == "" {
			message = "账号代理已清空"
		}
		_ = s.AddEvent(ctx, domain.Event{At: item.UpdatedAt, Level: "info", Action: "account_proxy_updated", AccountID: item.ID, Message: message})
	}
	return nil
}

func (s *Store) UpdateProxy(ctx context.Context, accountID, proxyURL string, at time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE accounts SET proxy_url = $2, updated_at = $3 WHERE id = $1
	`, accountID, strings.TrimSpace(proxyURL), at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(404, "account_not_found", "账号不存在", false)
	}
	return nil
}

func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists int
	err = tx.QueryRow(ctx, `SELECT 1 FROM accounts WHERE id = $1 FOR UPDATE`, id).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.New(404, "account_not_found", "账号不存在", false)
	}
	if err != nil {
		return err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM mailboxes WHERE account_id = $1`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return apperr.New(409, "account_has_mailboxes", "请先逐个远端删除该账号下的邮箱", false)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CountMailboxes(ctx context.Context, accountID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM mailboxes WHERE account_id = $1`, accountID).Scan(&n)
	return n, err
}

func (s *Store) LoadSession(ctx context.Context, accountID string) (protocol.ICloudSession, domain.Account, error) {
	account, ok, err := s.GetAccount(ctx, accountID)
	if err != nil {
		return protocol.ICloudSession{}, domain.Account{}, err
	}
	if !ok {
		return protocol.ICloudSession{}, domain.Account{}, apperr.New(404, "account_not_found", "账号不存在", false)
	}
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT session_json FROM accounts WHERE id = $1`, accountID).Scan(&raw); err != nil {
		return protocol.ICloudSession{}, domain.Account{}, err
	}
	var session protocol.ICloudSession
	if len(raw) > 0 && string(raw) != "{}" && string(raw) != "null" {
		if err := json.Unmarshal(raw, &session); err != nil {
			return protocol.ICloudSession{}, domain.Account{}, err
		}
	}
	session.AccountID = account.ID
	session.AppleID = firstNonEmpty(session.AppleID, account.AppleID)
	return protocol.ApplyAccountProxy(session, account.ProxyURL), account, nil
}

func (s *Store) SaveSession(ctx context.Context, accountID string, incoming protocol.ICloudSession) (protocol.ICloudSession, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return protocol.ICloudSession{}, err
	}
	defer tx.Rollback(ctx)
	merged, encoded, err := mergeLockedSession(ctx, tx, accountID, "", incoming)
	if err != nil {
		return protocol.ICloudSession{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE accounts SET session_json = $2, updated_at = now() WHERE id = $1`, accountID, encoded); err != nil {
		return protocol.ICloudSession{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return protocol.ICloudSession{}, err
	}
	return merged, nil
}

func (s *Store) CommitLogin(ctx context.Context, item domain.Account, updatePassword bool, incoming protocol.ICloudSession) (protocol.ICloudSession, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return protocol.ICloudSession{}, err
	}
	defer tx.Rollback(ctx)
	merged, encoded, err := mergeLockedSession(ctx, tx, item.ID, item.AppleID, incoming)
	if err != nil {
		return protocol.ICloudSession{}, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE accounts SET
			apple_id = CASE WHEN btrim($2) = '' THEN apple_id ELSE $2 END,
			apple_password = CASE WHEN $3 THEN $4 ELSE apple_password END,
			session_json = $5,
			updated_at = now()
		WHERE id = $1
	`, item.ID, strings.TrimSpace(item.AppleID), updatePassword, item.ApplePassword, encoded)
	if err != nil {
		return protocol.ICloudSession{}, err
	}
	if tag.RowsAffected() == 0 {
		return protocol.ICloudSession{}, apperr.New(404, "account_not_found", "账号不存在", false)
	}
	if err := tx.Commit(ctx); err != nil {
		return protocol.ICloudSession{}, err
	}
	return merged, nil
}

func mergeLockedSession(ctx context.Context, tx pgx.Tx, accountID, nextAppleID string, incoming protocol.ICloudSession) (protocol.ICloudSession, []byte, error) {
	var storedAppleID, proxyURL string
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT apple_id, proxy_url, session_json FROM accounts WHERE id = $1 FOR UPDATE`, accountID).Scan(&storedAppleID, &proxyURL, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return protocol.ICloudSession{}, nil, apperr.New(404, "account_not_found", "账号不存在", false)
	}
	if err != nil {
		return protocol.ICloudSession{}, nil, err
	}
	if protocol.SessionAccountMismatch(accountID, incoming.AccountID) {
		return protocol.ICloudSession{}, nil, apperr.New(409, "session_account_mismatch", "登录态属于另一个账号，已拒绝合并", false)
	}
	var existing protocol.ICloudSession
	if len(raw) > 0 && string(raw) != "{}" && string(raw) != "null" {
		if err := json.Unmarshal(raw, &existing); err != nil {
			return protocol.ICloudSession{}, nil, err
		}
	}
	if protocol.SessionAccountMismatch(existing.AccountID, incoming.AccountID) {
		return protocol.ICloudSession{}, nil, apperr.New(409, "session_account_mismatch", "登录态属于另一个账号，已拒绝合并", false)
	}
	existing = sessionBaseForLogin(existing, storedAppleID, nextAppleID)
	merged := protocol.MergeSavedSession(existing, incoming, proxyURL)
	merged.AccountID = accountID
	encoded, err := json.Marshal(merged)
	if err != nil {
		return protocol.ICloudSession{}, nil, err
	}
	return merged, encoded, nil
}

func (s *Store) MarkReconciliation(ctx context.Context, accountID, origin, message, anonymousID, email string, at time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE accounts SET
			reconciliation_required = true,
			reconciliation_at = $2,
			reconciliation_error = $3,
			reconciliation_origin = $4,
			reconciliation_anonymous_id = $5,
			reconciliation_email = $6,
			updated_at = $2
		WHERE id = $1
	`, accountID, at, message, strings.ToUpper(strings.TrimSpace(origin)), strings.TrimSpace(anonymousID), strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(404, "account_not_found", "账号不存在", false)
	}
	return nil
}

func (s *Store) ClearReconciliation(ctx context.Context, accountID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE accounts SET
			reconciliation_required = false,
			reconciliation_error = '',
			reconciliation_origin = '',
			reconciliation_anonymous_id = '',
			reconciliation_email = '',
			updated_at = now()
		WHERE id = $1
	`, accountID)
	return err
}

func (s *Store) ListMailboxes(ctx context.Context, accountID, status string) ([]domain.Mailbox, error) {
	query := mailboxSelect + ` WHERE ($1 = '' OR account_id = $1) AND ($2 = '' OR status = $2) ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, query, accountID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectMailboxes(rows)
}

func (s *Store) GetMailbox(ctx context.Context, id string) (domain.Mailbox, bool, error) {
	return s.oneMailbox(ctx, mailboxSelect+` WHERE id = $1`, id)
}

func (s *Store) GetMailboxByEmail(ctx context.Context, email string) (domain.Mailbox, bool, error) {
	return s.oneMailbox(ctx, mailboxSelect+` WHERE email = $1`, domain.NormalizeEmail(email))
}

func (s *Store) oneMailbox(ctx context.Context, query string, arg any) (domain.Mailbox, bool, error) {
	rows, err := s.pool.Query(ctx, query, arg)
	if err != nil {
		return domain.Mailbox{}, false, err
	}
	defer rows.Close()
	items, err := collectMailboxes(rows)
	if err != nil || len(items) == 0 {
		return domain.Mailbox{}, false, err
	}
	return items[0], true, nil
}

func (s *Store) InsertMailbox(ctx context.Context, item domain.Mailbox) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mailboxes (
			id, account_id, email, label, note, status, api_token, api_active, icloud_active,
			remote_anonymous_id, remote_origin, last_sync_uid, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			$10, $11, '', $12, $12
		)
	`, item.ID, item.AccountID, domain.NormalizeEmail(item.Email), item.Label, item.Note, item.Status,
		item.APIToken, item.APIActive, item.ICloudActive, item.RemoteAnonymousID, item.RemoteOrigin, item.CreatedAt)
	if isUnique(err) {
		return apperr.New(409, "mailbox_exists", "邮箱已存在", false)
	}
	return err
}

func (s *Store) UpdateMailboxAdmin(ctx context.Context, id, label, note string, apiActive bool, at time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE mailboxes SET label = $2, note = $3, api_active = $4, updated_at = $5
		WHERE id = $1
	`, id, label, note, apiActive, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(404, "mailbox_not_found", "邮箱不存在", false)
	}
	return nil
}

func (s *Store) TouchSync(ctx context.Context, id, uid string, at time.Time) error {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		tag, err := s.pool.Exec(ctx, `
			UPDATE mailboxes SET last_sync_at = $2, updated_at = $2 WHERE id = $1
		`, id, at)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.New(404, "mailbox_not_found", "邮箱不存在", false)
		}
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current string
	err = tx.QueryRow(ctx, `SELECT last_sync_uid FROM mailboxes WHERE id = $1 FOR UPDATE`, id).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.New(404, "mailbox_not_found", "邮箱不存在", false)
	}
	if err != nil {
		return err
	}
	next := domain.LaterUID(current, uid)
	if _, err := tx.Exec(ctx, `UPDATE mailboxes SET last_sync_at = $2, last_sync_uid = $3, updated_at = $2 WHERE id = $1`, id, at, next); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) MarkRemoteMissing(ctx context.Context, id string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE mailboxes SET remote_missing_at = $2, icloud_active = false, updated_at = $2 WHERE id = $1
	`, id, at)
	return err
}

func (s *Store) MarkRemotePresent(ctx context.Context, id, anonymousID, origin string, active bool) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE mailboxes SET
			remote_anonymous_id = CASE
				WHEN $2 = '' THEN remote_anonymous_id
				WHEN $3 <> '' AND upper(CASE WHEN btrim(COALESCE(remote_origin, '')) = '' THEN 'ICLOUD_WEB' ELSE remote_origin END) <> upper($3) THEN remote_anonymous_id
				ELSE $2
			END,
			remote_origin = CASE
				WHEN $3 = '' THEN remote_origin
				WHEN upper(CASE WHEN btrim(COALESCE(remote_origin, '')) = '' THEN 'ICLOUD_WEB' ELSE remote_origin END) <> upper($3) THEN remote_origin
				ELSE $3
			END,
			icloud_active = CASE
				WHEN $3 <> '' AND upper(CASE WHEN btrim(COALESCE(remote_origin, '')) = '' THEN 'ICLOUD_WEB' ELSE remote_origin END) <> upper($3) THEN icloud_active
				ELSE $4
			END,
			remote_missing_at = CASE
				WHEN $3 <> '' AND upper(CASE WHEN btrim(COALESCE(remote_origin, '')) = '' THEN 'ICLOUD_WEB' ELSE remote_origin END) <> upper($3) THEN remote_missing_at
				ELSE NULL
			END,
			updated_at = now()
		WHERE id = $1
	`, id, anonymousID, origin, active)
	return err
}

func (s *Store) BeginRemoteDelete(ctx context.Context, id string, at time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE mailboxes
		SET remote_delete_status = $2,
		    remote_delete_error = '',
		    remote_delete_at = $3,
		    updated_at = $3
		WHERE id = $1 AND remote_delete_status IS DISTINCT FROM $2
	`, id, domain.DeletePending, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	item, ok, err := s.GetMailbox(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.New(404, "mailbox_not_found", "邮箱不存在", false)
	}
	if item.RemoteDeleteStatus == domain.DeletePending {
		return apperr.New(409, "remote_delete_in_progress", "该邮箱已有远端删除操作进行中，请稍后重试", true)
	}
	return apperr.New(409, "remote_delete_in_progress", "远端删除状态已变化，请刷新后重试", true)
}

func (s *Store) MarkRemoteDelete(ctx context.Context, id, status, message string, at time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE mailboxes
		SET remote_delete_status = $2,
		    remote_delete_error = $3,
		    remote_delete_at = $4,
		    updated_at = $4
		WHERE id = $1
	`, id, status, message, at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(404, "mailbox_not_found", "邮箱不存在", false)
	}
	return nil
}

func (s *Store) DeleteMailbox(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM mailboxes WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(404, "mailbox_not_found", "邮箱不存在", false)
	}
	return nil
}

func (s *Store) UpsertMessages(ctx context.Context, mailboxID string, items []domain.Message) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, item := range items {
		if strings.TrimSpace(item.CanonicalID) == "" {
			continue
		}
		var existing domain.Message
		err := tx.QueryRow(ctx, `
			SELECT id, remote_id, uid, subject, from_addr, body, source, received_at
			FROM messages
			WHERE mailbox_id = $1 AND canonical_id = $2
			FOR UPDATE
		`, mailboxID, item.CanonicalID).Scan(
			&existing.ID, &existing.RemoteID, &existing.UID, &existing.Subject, &existing.From, &existing.Body, &existing.Source, &existing.ReceivedAt,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `
				INSERT INTO messages (
					id, mailbox_id, remote_id, canonical_id, uid, subject, from_addr, body, source, received_at, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
			`, item.ID, mailboxID, item.RemoteID, item.CanonicalID, item.UID, item.Subject, item.From, item.Body, item.Source, item.ReceivedAt)
			if err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		merged := mergeObservedMessage(existing, item)
		if _, err := tx.Exec(ctx, `
			UPDATE messages SET
				remote_id = $2,
				uid = $3,
				subject = $4,
				from_addr = $5,
				body = $6,
				source = $7,
				received_at = $8
			WHERE id = $1
		`, existing.ID, merged.RemoteID, merged.UID, merged.Subject, merged.From, merged.Body, merged.Source, merged.ReceivedAt); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ListMessages(ctx context.Context, mailboxID string, after time.Time, limit int) ([]domain.Message, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, mailbox_id, remote_id, canonical_id, uid, subject, from_addr, body, source, received_at
		FROM messages
		WHERE mailbox_id = $1 AND ($2::timestamptz IS NULL OR received_at > $2)
		ORDER BY received_at DESC
		LIMIT $3
	`, mailboxID, nullTime(after), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Message
	for rows.Next() {
		var item domain.Message
		if err := rows.Scan(&item.ID, &item.MailboxID, &item.RemoteID, &item.CanonicalID, &item.UID, &item.Subject, &item.From, &item.Body, &item.Source, &item.ReceivedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) DeleteOldMessages(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM messages WHERE received_at < $1 AND received_at >= TIMESTAMPTZ '0002-01-01 00:00:00+00'`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) AddEvent(ctx context.Context, item domain.Event) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO events (at, level, action, account_id, mailbox_id, message)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, item.At, item.Level, item.Action, item.AccountID, item.MailboxID, item.Message)
	return err
}

func (s *Store) ListEvents(ctx context.Context, limit int) ([]domain.Event, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, at, level, action, account_id, mailbox_id, message
		FROM events ORDER BY id DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Event
	for rows.Next() {
		var item domain.Event
		if err := rows.Scan(&item.ID, &item.At, &item.Level, &item.Action, &item.AccountID, &item.MailboxID, &item.Message); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type ExportStamp struct {
	APIExportedAt *time.Time
	UpdatedAt     time.Time
}

func (s *Store) MarkExported(ctx context.Context, ids []string, at time.Time) (map[string]ExportStamp, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	previous := map[string]ExportStamp{}
	for _, id := range ids {
		var stamp ExportStamp
		err := tx.QueryRow(ctx, `SELECT api_exported_at, updated_at FROM mailboxes WHERE id = $1 FOR UPDATE`, id).Scan(&stamp.APIExportedAt, &stamp.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.New(400, "export_selection_missing", "导出选择里有不存在的邮箱", false)
		}
		if err != nil {
			return nil, err
		}
		previous[id] = stamp
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `UPDATE mailboxes SET api_exported_at = $2, updated_at = $2 WHERE id = $1`, id, at); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return previous, nil
}

func (s *Store) RestoreExported(ctx context.Context, previous map[string]ExportStamp) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for id, stamp := range previous {
		if _, err := tx.Exec(ctx, `UPDATE mailboxes SET api_exported_at = $2, updated_at = $3 WHERE id = $1`, id, stamp.APIExportedAt, stamp.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
