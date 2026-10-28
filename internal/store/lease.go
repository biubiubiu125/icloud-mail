package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
)

func (s *Store) Claim(ctx context.Context, in domain.ClaimInput) (domain.ClaimResult, error) {
	if in.Now.IsZero() {
		in.Now = time.Now()
	}
	if in.TTL <= 0 {
		in.TTL = time.Duration(domain.DefaultLeaseTTLMinutes) * time.Minute
	}
	if in.TTL > time.Duration(domain.MaxLeaseTTLMinutes)*time.Minute {
		in.TTL = time.Duration(domain.MaxLeaseTTLMinutes) * time.Minute
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ClaimResult{}, err
	}
	defer tx.Rollback(ctx)

	project := strings.TrimSpace(in.Project)
	requestID := strings.TrimSpace(in.RequestID)
	if requestID != "" {
		lease, mailbox, ok, err := leaseByRequest(ctx, tx, project, requestID)
		if err != nil {
			return domain.ClaimResult{}, err
		}
		if ok {
			result, err := replayLockedClaim(ctx, tx, lease, mailbox, in)
			if err != nil {
				return domain.ClaimResult{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return domain.ClaimResult{}, err
			}
			return result, nil
		}
	}

	rows, err := tx.Query(ctx, mailboxSelect+`
		WHERE status = $1
		  AND api_active
		  AND icloud_active
		  AND active_lease_id = ''
		  AND remote_delete_status = ''
		  AND ($2 = '' OR account_id = $2)
		ORDER BY created_at
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, domain.StatusAvailable, strings.TrimSpace(in.AccountID))
	if err != nil {
		return domain.ClaimResult{}, err
	}
	items, err := collectMailboxes(rows)
	rows.Close()
	if err != nil {
		return domain.ClaimResult{}, err
	}
	if len(items) == 0 {
		return domain.ClaimResult{}, apperr.New(200, "no_available_mailbox", "没有可领取的邮箱", false)
	}
	mailbox := items[0]
	leaseID, err := newID()
	if err != nil {
		return domain.ClaimResult{}, err
	}
	lease := domain.Lease{
		ID:        leaseID,
		MailboxID: mailbox.ID,
		Project:   project,
		Purpose:   strings.TrimSpace(in.Purpose),
		RequestID: requestID,
		State:     domain.LeaseHeld,
		ExpiresAt: in.Now.Add(in.TTL),
		CreatedAt: in.Now,
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO mailbox_leases (
			id, mailbox_id, project, purpose, request_id, state, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, lease.ID, lease.MailboxID, lease.Project, lease.Purpose, lease.RequestID, lease.State, lease.ExpiresAt, lease.CreatedAt); err != nil {
		if isUnique(err) {
			return domain.ClaimResult{}, apperr.New(409, "request_id_conflict", "请求号正在被另一笔领取使用", true)
		}
		return domain.ClaimResult{}, err
	}
	note := strings.TrimSpace(in.Note)
	if note == "" {
		note = "已领取"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE mailboxes
		SET status = $2, note = $3, active_lease_id = $4, updated_at = $5, code_not_before = $5
		WHERE id = $1
	`, mailbox.ID, domain.StatusUsed, note, lease.ID, in.Now); err != nil {
		return domain.ClaimResult{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM messages WHERE mailbox_id = $1 AND received_at <= $2`, mailbox.ID, in.Now); err != nil {
		return domain.ClaimResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ClaimResult{}, err
	}
	mailbox.Status = domain.StatusUsed
	mailbox.Note = note
	mailbox.ActiveLeaseID = lease.ID
	mailbox.UpdatedAt = in.Now
	claimedAt := in.Now
	mailbox.CodeNotBefore = &claimedAt
	return domain.ClaimResult{Mailbox: mailbox, Lease: &lease, Created: true}, nil
}

func (s *Store) GetLease(ctx context.Context, id string) (domain.Lease, bool, error) {
	var item domain.Lease
	err := s.pool.QueryRow(ctx, leaseSelect+` WHERE id = $1`, id).Scan(leaseScan(&item)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Lease{}, false, nil
	}
	return item, err == nil, err
}

func (s *Store) CommitLease(ctx context.Context, id string, at time.Time, mailboxKey string) (domain.Lease, error) {
	return s.transitionLease(ctx, id, at, domain.LeaseCommitted, false, mailboxKey)
}

func (s *Store) ReleaseLease(ctx context.Context, id string, at time.Time, returnToPool bool, mailboxKey string) (domain.Lease, error) {
	return s.transitionLease(ctx, id, at, domain.LeaseReleased, returnToPool, mailboxKey)
}

func (s *Store) RenewLease(ctx context.Context, id string, at time.Time, ttl time.Duration, mailboxKey string) (domain.Lease, error) {
	if ttl <= 0 {
		ttl = time.Duration(domain.DefaultLeaseTTLMinutes) * time.Minute
	}
	if ttl > time.Duration(domain.MaxLeaseTTLMinutes)*time.Minute {
		ttl = time.Duration(domain.MaxLeaseTTLMinutes) * time.Minute
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Lease{}, err
	}
	defer tx.Rollback(ctx)
	lease, err := lockLease(ctx, tx, id)
	if err != nil {
		return domain.Lease{}, err
	}
	if err := guardMailboxKey(ctx, tx, lease.MailboxID, lease.ID, mailboxKey); err != nil {
		return domain.Lease{}, err
	}
	if lease.State != domain.LeaseHeld {
		return domain.Lease{}, apperr.New(409, "lease_not_held", "只有持有中的租约可以续期", false)
	}
	if !lease.ExpiresAt.After(at) {
		return domain.Lease{}, apperr.New(409, "lease_expired", "租约已过期", false)
	}
	lease.ExpiresAt = at.Add(ttl)
	if _, err := tx.Exec(ctx, `UPDATE mailbox_leases SET expires_at = $2 WHERE id = $1`, lease.ID, lease.ExpiresAt); err != nil {
		return domain.Lease{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Lease{}, err
	}
	return lease, nil
}

func (s *Store) ExpireHeldLeases(ctx context.Context, at time.Time) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT id, mailbox_id FROM mailbox_leases
		WHERE state = $1 AND expires_at <= $2
		FOR UPDATE
	`, domain.LeaseHeld, at)
	if err != nil {
		return 0, err
	}
	type pair struct{ id, mailboxID string }
	var pairs []pair
	for rows.Next() {
		var item pair
		if err := rows.Scan(&item.id, &item.mailboxID); err != nil {
			rows.Close()
			return 0, err
		}
		pairs = append(pairs, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, item := range pairs {
		if _, err := tx.Exec(ctx, `
			UPDATE mailbox_leases SET state = $2, expired_at = $3 WHERE id = $1
		`, item.id, domain.LeaseExpired, at); err != nil {
			return 0, err
		}
		if err := returnMailboxToPool(ctx, tx, item.mailboxID, item.id, "租约已过期", at, true); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int64(len(pairs)), nil
}

func (s *Store) transitionLease(ctx context.Context, id string, at time.Time, next string, returnToPool bool, mailboxKey string) (domain.Lease, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Lease{}, err
	}
	defer tx.Rollback(ctx)
	lease, err := lockLease(ctx, tx, id)
	if err != nil {
		return domain.Lease{}, err
	}
	if err := guardMailboxKey(ctx, tx, lease.MailboxID, lease.ID, mailboxKey); err != nil {
		return domain.Lease{}, err
	}
	switch next {
	case domain.LeaseCommitted:
		if lease.State != domain.LeaseHeld {
			return domain.Lease{}, apperr.New(409, "lease_not_held", "只有持有中的租约可以确认", false)
		}
		if !lease.ExpiresAt.After(at) {
			return domain.Lease{}, apperr.New(409, "lease_expired", "租约已过期", false)
		}
		lease.State = domain.LeaseCommitted
		lease.CommittedAt = &at
		if _, err := tx.Exec(ctx, `
			UPDATE mailbox_leases SET state = $2, committed_at = $3 WHERE id = $1
		`, lease.ID, lease.State, at); err != nil {
			return domain.Lease{}, err
		}
	case domain.LeaseReleased:
		if lease.State != domain.LeaseHeld && lease.State != domain.LeaseCommitted {
			return domain.Lease{}, apperr.New(409, "lease_not_releasable", "该租约不能释放", false)
		}
		wasHeld := lease.State == domain.LeaseHeld
		lease.State = domain.LeaseReleased
		lease.ReleasedAt = &at
		if _, err := tx.Exec(ctx, `
			UPDATE mailbox_leases SET state = $2, released_at = $3 WHERE id = $1
		`, lease.ID, lease.State, at); err != nil {
			return domain.Lease{}, err
		}
		if wasHeld || returnToPool {
			if err := returnMailboxToPool(ctx, tx, lease.MailboxID, lease.ID, "已释放", at, false); err != nil {
				return domain.Lease{}, err
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE mailboxes SET active_lease_id = '', updated_at = $2
				WHERE id = $1 AND active_lease_id = $3
			`, lease.MailboxID, at, lease.ID); err != nil {
				return domain.Lease{}, err
			}
		}
	default:
		return domain.Lease{}, apperr.New(400, "lease_action_invalid", "不支持的租约操作", false)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Lease{}, err
	}
	return lease, nil
}

const leaseSelect = `
	SELECT id, mailbox_id, project, purpose, request_id, state, expires_at,
	       committed_at, released_at, expired_at, created_at
	FROM mailbox_leases
`

func leaseScan(item *domain.Lease) []any {
	return []any{
		&item.ID, &item.MailboxID, &item.Project, &item.Purpose, &item.RequestID, &item.State,
		&item.ExpiresAt, &item.CommittedAt, &item.ReleasedAt, &item.ExpiredAt, &item.CreatedAt,
	}
}

func returnMailboxToPool(ctx context.Context, tx pgx.Tx, mailboxID, leaseID, note string, at time.Time, requireUsed bool) error {
	token, err := protocolRandomToken()
	if err != nil {
		return err
	}
	query := `
		UPDATE mailboxes
		SET status = $2, active_lease_id = '', note = $3, api_token = $4, code_not_before = $5, updated_at = $5
		WHERE id = $1 AND active_lease_id = $6`
	args := []any{mailboxID, domain.StatusAvailable, note, token, at, leaseID}
	if requireUsed {
		query += ` AND status = $7`
		args = append(args, domain.StatusUsed)
	}
	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	_, err = tx.Exec(ctx, `DELETE FROM messages WHERE mailbox_id = $1 AND received_at <= $2`, mailboxID, at)
	return err
}

func guardMailboxKey(ctx context.Context, tx pgx.Tx, mailboxID, leaseID, key string) error {
	if key == "" {
		return nil
	}
	var token, active string
	err := tx.QueryRow(ctx, `SELECT api_token, active_lease_id FROM mailboxes WHERE id = $1 FOR UPDATE`, mailboxID).Scan(&token, &active)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!domain.TokenMatches(key, token) || active != leaseID)) {
		return apperr.New(404, "mailbox_not_found", "邮箱不存在或密钥不正确", false)
	}
	return err
}

func lockLease(ctx context.Context, tx pgx.Tx, id string) (domain.Lease, error) {
	var item domain.Lease
	err := tx.QueryRow(ctx, leaseSelect+` WHERE id = $1 FOR UPDATE`, id).Scan(leaseScan(&item)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Lease{}, apperr.New(404, "lease_not_found", "租约不存在", false)
	}
	return item, err
}

func leaseByRequest(ctx context.Context, tx pgx.Tx, project, requestID string) (domain.Lease, domain.Mailbox, bool, error) {
	var lease domain.Lease
	err := tx.QueryRow(ctx, leaseSelect+` WHERE project = $1 AND request_id = $2 FOR UPDATE`, project, requestID).Scan(leaseScan(&lease)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Lease{}, domain.Mailbox{}, false, nil
	}
	if err != nil {
		return domain.Lease{}, domain.Mailbox{}, false, err
	}
	rows, err := tx.Query(ctx, mailboxSelect+` WHERE id = $1 FOR UPDATE`, lease.MailboxID)
	if err != nil {
		return domain.Lease{}, domain.Mailbox{}, false, err
	}
	items, err := collectMailboxes(rows)
	rows.Close()
	if err != nil {
		return domain.Lease{}, domain.Mailbox{}, false, err
	}
	if len(items) == 0 {
		return domain.Lease{}, domain.Mailbox{}, false, apperr.New(404, "mailbox_not_found", "租约对应的邮箱不存在", false)
	}
	return lease, items[0], true, nil
}

func newID() (string, error) {
	return protocolRandomID()
}

func replayLockedClaim(ctx context.Context, tx pgx.Tx, lease domain.Lease, mailbox domain.Mailbox, in domain.ClaimInput) (domain.ClaimResult, error) {
	if strings.TrimSpace(in.AccountID) != "" && mailbox.AccountID != in.AccountID {
		return domain.ClaimResult{}, apperr.New(409, "request_id_account_mismatch", "该请求号属于另一个账号，不能返回密钥", true)
	}
	switch domain.ClassifyClaimReplay(lease, mailbox, in.Now, in.AccountID) {
	case domain.ClaimReplayReturn:
		return domain.ClaimResult{Mailbox: mailbox, Lease: &lease, Idempotent: true}, nil
	case domain.ClaimReplayRehold:
		expires := in.Now.Add(in.TTL)
		note := strings.TrimSpace(in.Note)
		if note == "" {
			note = "已领取"
		}
		tag, err := tx.Exec(ctx, `
			UPDATE mailboxes
			SET status = $2, active_lease_id = $3, note = $4, updated_at = $5
			WHERE id = $1
			  AND status = $6
			  AND active_lease_id = ''
			  AND api_active
			  AND icloud_active
			  AND remote_delete_status = ''
		`, mailbox.ID, domain.StatusUsed, lease.ID, note, in.Now, domain.StatusAvailable)
		if err != nil {
			return domain.ClaimResult{}, err
		}
		if tag.RowsAffected() == 0 {
			return domain.ClaimResult{}, apperr.New(409, "request_id_not_reclaimable", "该请求号对应的邮箱已不再由这笔领取独占，不能返回密钥", true)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE mailbox_leases
			SET state = $2, expires_at = $3, committed_at = NULL, released_at = NULL, expired_at = NULL
			WHERE id = $1
		`, lease.ID, domain.LeaseHeld, expires); err != nil {
			return domain.ClaimResult{}, err
		}
		mailbox.Status = domain.StatusUsed
		mailbox.Note = note
		mailbox.ActiveLeaseID = lease.ID
		lease.State = domain.LeaseHeld
		lease.ExpiresAt = expires
		lease.CommittedAt = nil
		lease.ReleasedAt = nil
		lease.ExpiredAt = nil
		return domain.ClaimResult{Mailbox: mailbox, Lease: &lease, Idempotent: true}, nil
	default:
		return domain.ClaimResult{}, apperr.New(409, "request_id_not_reclaimable", "该请求号对应的邮箱已不再由这笔领取独占，不能返回密钥", true)
	}
}
