package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

func TestPostgresSessionProxyAndClaim(t *testing.T) {
	dsn := os.Getenv("ICLOUD_MAIL_TEST_DSN")
	if dsn == "" {
		t.Skip("ICLOUD_MAIL_TEST_DSN is empty")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `TRUNCATE events, messages, mailbox_leases, mailboxes, accounts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	account := domain.Account{ID: "acc-1", AppleID: "owner@icloud.com", ProxyURL: "http://proxy.example:8080", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	incoming := protocol.ICloudSession{ProxyURL: "socks5://stale.example:1080", AppleID: "owner@icloud.com", LoginStates: []protocol.LoginState{{
		Kind: protocol.LoginStateAppleAccount, APIKey: "kept",
	}}}
	if _, err := db.SaveSession(ctx, account.ID, incoming); err != nil {
		t.Fatal(err)
	}
	saved, _, err := db.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ProxyURL != account.ProxyURL {
		t.Fatalf("proxy overwritten: %q", saved.ProxyURL)
	}
	loaded, _, err := db.LoadSession(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ProxyURL != account.ProxyURL {
		t.Fatalf("loaded proxy = %q", loaded.ProxyURL)
	}
	rejected := incoming
	rejected.AccountID = "other-account"
	if _, err := db.SaveSession(ctx, account.ID, rejected); err == nil {
		t.Fatal("different account id was merged")
	}
	afterReject, _, err := db.LoadSession(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	keptKey := false
	for _, state := range afterReject.LoginStates {
		if state.Kind == protocol.LoginStateAppleAccount && state.APIKey == "kept" {
			keptKey = true
		}
	}
	if !keptKey {
		t.Fatal("rejected merge changed the saved login")
	}

	mailbox := domain.Mailbox{
		ID: "box-1", AccountID: account.ID, Email: "alias@icloud.com", Status: domain.StatusAvailable,
		APIToken: "token-1", APIActive: true, ICloudActive: true, RemoteAnonymousID: "anon",
		RemoteOrigin: domain.OriginICloudWeb, CreatedAt: now,
	}
	if err := db.InsertMailbox(ctx, mailbox); err != nil {
		t.Fatal(err)
	}
	if err := db.BeginRemoteDelete(ctx, mailbox.ID, now); err != nil {
		t.Fatal(err)
	}
	changed, err := db.NormalizeInterruptedDeletes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("changed = %d", changed)
	}
	after, ok, err := db.GetMailbox(ctx, mailbox.ID)
	if err != nil || !ok || after.RemoteDeleteStatus != domain.DeleteUnknown {
		t.Fatalf("status = %#v err=%v ok=%v", after.RemoteDeleteStatus, err, ok)
	}

	second := mailbox
	second.ID = "box-2"
	second.Email = "alias2@icloud.com"
	second.RemoteDeleteStatus = ""
	if err := db.InsertMailbox(ctx, second); err != nil {
		t.Fatal(err)
	}
	first, err := db.Claim(ctx, domain.ClaimInput{Project: "reg", RequestID: "req-1", Now: now, TTL: time.Minute})
	if err != nil || !first.Created || first.Mailbox.Email != second.Email {
		t.Fatalf("first claim = %#v err=%v", first, err)
	}
	again, err := db.Claim(ctx, domain.ClaimInput{Project: "reg", RequestID: "req-1", Now: now, TTL: time.Minute})
	if err != nil || again.Created || !again.Idempotent || again.Mailbox.ID != first.Mailbox.ID || again.Mailbox.APIToken != first.Mailbox.APIToken {
		t.Fatalf("idempotent claim = %#v err=%v", again, err)
	}
	if err := db.UpsertMessages(ctx, second.ID, []domain.Message{{ID: "msg-kept", CanonicalID: "mid:kept@example.com", Subject: "keep", ReceivedAt: now}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CommitLease(ctx, first.Lease.ID, now.Add(30*time.Second), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReleaseLease(ctx, first.Lease.ID, now.Add(time.Minute), false, ""); err != nil {
		t.Fatal(err)
	}
	kept, ok, err := db.GetMailbox(ctx, first.Mailbox.ID)
	if err != nil || !ok || kept.APIToken != first.Mailbox.APIToken || kept.Status != domain.StatusUsed || kept.CodeNotBefore == nil || kept.CodeNotBefore.Sub(now).Abs() > time.Millisecond {
		t.Fatalf("committed release changed token or status: %#v err=%v", kept, err)
	}
	keptMessages, err := db.ListMessages(ctx, second.ID, time.Time{}, 50)
	if err != nil || len(keptMessages) != 1 {
		t.Fatalf("committed release removed history: %d err=%v", len(keptMessages), err)
	}
	replayKept, err := db.Claim(ctx, domain.ClaimInput{Project: "reg", RequestID: "req-1", Now: now.Add(time.Minute), TTL: time.Minute})
	if err != nil || !replayKept.Idempotent || replayKept.Mailbox.APIToken != first.Mailbox.APIToken {
		t.Fatalf("exclusive replay = %#v err=%v", replayKept, err)
	}
	poolBox := second
	poolBox.ID = "box-pool"
	poolBox.Email = "pool@icloud.com"
	poolBox.Status = domain.StatusAvailable
	poolBox.APIToken = "pool-token"
	poolBox.ActiveLeaseID = ""
	poolBox.RemoteDeleteStatus = ""
	if err := db.InsertMailbox(ctx, poolBox); err != nil {
		t.Fatal(err)
	}
	poolClaim, err := db.Claim(ctx, domain.ClaimInput{Project: "reg", RequestID: "req-pool", Now: now, TTL: time.Minute})
	if err != nil || poolClaim.Mailbox.ID != poolBox.ID || poolClaim.Mailbox.APIToken != "pool-token" {
		t.Fatalf("pool claim = %#v err=%v", poolClaim, err)
	}
	if err := db.UpsertMessages(ctx, poolBox.ID, []domain.Message{{ID: "msg-old", CanonicalID: "mid:old@example.com", Subject: "old", ReceivedAt: now.Add(90 * time.Second)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReleaseLease(ctx, poolClaim.Lease.ID, now.Add(2*time.Minute), false, "wrong-key"); err == nil {
		t.Fatal("wrong mailbox key released the lease")
	}
	if _, err := db.ReleaseLease(ctx, poolClaim.Lease.ID, now.Add(2*time.Minute), false, "pool-token"); err != nil {
		t.Fatal(err)
	}
	pooled, ok, err := db.GetMailbox(ctx, poolBox.ID)
	if err != nil || !ok || pooled.Status != domain.StatusAvailable || pooled.APIToken == "pool-token" || pooled.APIToken == "" || pooled.CodeNotBefore == nil {
		t.Fatalf("pool token = %#v err=%v", pooled, err)
	}
	oldMessages, err := db.ListMessages(ctx, poolBox.ID, time.Time{}, 50)
	if err != nil || len(oldMessages) != 0 {
		t.Fatalf("pool return kept old messages: %d err=%v", len(oldMessages), err)
	}
	reheld, err := db.Claim(ctx, domain.ClaimInput{Project: "reg", RequestID: "req-pool", Now: now.Add(3 * time.Minute), TTL: time.Minute})
	if err != nil || !reheld.Idempotent || reheld.Mailbox.APIToken != pooled.APIToken || reheld.Mailbox.Status != domain.StatusUsed {
		t.Fatalf("rehold = %#v err=%v", reheld, err)
	}
	if reheld.Mailbox.Note != "已领取" {
		t.Fatalf("rehold response note = %q", reheld.Mailbox.Note)
	}
	reheldBox, ok, err := db.GetMailbox(ctx, poolBox.ID)
	if err != nil || !ok || reheldBox.Note != "已领取" {
		t.Fatalf("rehold stored note = %#v err=%v", reheldBox, err)
	}
	if _, err := db.ReleaseLease(ctx, reheld.Lease.ID, now.Add(4*time.Minute), false, ""); err != nil {
		t.Fatal(err)
	}
	taken, err := db.Claim(ctx, domain.ClaimInput{Project: "reg", RequestID: "req-taken", Now: now.Add(5 * time.Minute), TTL: time.Minute})
	if err != nil || taken.Mailbox.ID != poolBox.ID || taken.Mailbox.APIToken == "pool-token" {
		t.Fatalf("taken = %#v err=%v", taken, err)
	}
	_, err = db.Claim(ctx, domain.ClaimInput{Project: "reg", RequestID: "req-pool", Now: now.Add(6 * time.Minute), TTL: time.Minute})
	var replayErr *apperr.Error
	if err == nil || !errors.As(err, &replayErr) || replayErr.Code != "request_id_not_reclaimable" {
		t.Fatalf("taken replay err = %v", err)
	}

	beforeExport, ok, err := db.GetMailbox(ctx, second.ID)
	if err != nil || !ok {
		t.Fatal(err)
	}
	previous, err := db.MarkExported(ctx, []string{second.ID}, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RestoreExported(ctx, previous); err != nil {
		t.Fatal(err)
	}
	restored, _, err := db.GetMailbox(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.APIExportedAt != nil {
		t.Fatal("export timestamp was not restored")
	}
	if !restored.UpdatedAt.Equal(beforeExport.UpdatedAt) {
		t.Fatalf("updated_at = %s want %s", restored.UpdatedAt, beforeExport.UpdatedAt)
	}

	if err := db.MarkReconciliation(ctx, account.ID, domain.OriginICloudWeb, "uncertain", "anon-target", "Alias@icloud.com", now); err != nil {
		t.Fatal(err)
	}
	marked, _, err := db.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !marked.ReconciliationRequired || marked.ReconciliationAnonymousID != "anon-target" || marked.ReconciliationEmail != "alias@icloud.com" {
		t.Fatalf("reconciliation target = %#v", marked)
	}
	if err := db.ClearReconciliation(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	cleared, _, err := db.GetAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.ReconciliationRequired || cleared.ReconciliationAnonymousID != "" || cleared.ReconciliationEmail != "" {
		t.Fatal("reconciliation target was not cleared")
	}

	third := second
	third.ID = "box-3"
	third.Email = "alias3@icloud.com"
	third.Status = domain.StatusAvailable
	third.APIToken = "expire-token"
	third.ActiveLeaseID = ""
	third.RemoteDeleteStatus = ""
	if err := db.InsertMailbox(ctx, third); err != nil {
		t.Fatal(err)
	}
	expireClaim, err := db.Claim(ctx, domain.ClaimInput{Project: "reg", RequestID: "req-expire", Now: now, TTL: time.Minute})
	if err != nil || expireClaim.Mailbox.ID != third.ID || expireClaim.Mailbox.APIToken != "expire-token" {
		t.Fatalf("expire claim = %#v err=%v", expireClaim, err)
	}
	_, err = db.Claim(ctx, domain.ClaimInput{Project: "reg", RequestID: "req-expire", Now: now.Add(2 * time.Minute), TTL: time.Minute})
	var expiredReplay *apperr.Error
	if err == nil || !errors.As(err, &expiredReplay) || expiredReplay.Code != "request_id_not_reclaimable" {
		t.Fatalf("expired replay err = %v", err)
	}
	pinned, ok, err := db.GetMailbox(ctx, third.ID)
	if err != nil || !ok || pinned.APIToken != "expire-token" || pinned.Status != domain.StatusUsed {
		t.Fatalf("expired replay changed mailbox = %#v err=%v", pinned, err)
	}
	if _, err := db.ExpireHeldLeases(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	expiredBox, ok, err := db.GetMailbox(ctx, third.ID)
	if err != nil || !ok || expiredBox.Status != domain.StatusAvailable || expiredBox.APIToken == "expire-token" || expiredBox.APIToken == "" {
		t.Fatalf("expired mailbox = %#v err=%v", expiredBox, err)
	}
}

func TestCreateAdminRejectsSecond(t *testing.T) {
	dsn := os.Getenv("ICLOUD_MAIL_TEST_DSN")
	if dsn == "" {
		t.Skip("ICLOUD_MAIL_TEST_DSN is empty")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `TRUNCATE admin_sessions, admins RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	first := domain.Admin{ID: "admin-1", Username: "owner", PasswordHash: "hash", CreatedAt: now}
	if err := db.CreateAdmin(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := domain.Admin{ID: "admin-2", Username: "other", PasswordHash: "hash", CreatedAt: now}
	err = db.CreateAdmin(ctx, second)
	var appErr *apperr.Error
	if err == nil || !errors.As(err, &appErr) || appErr.Code != "admin_exists" {
		t.Fatalf("second admin err = %v", err)
	}
}

func TestDeleteAccountAndAdminUpdate(t *testing.T) {
	dsn := os.Getenv("ICLOUD_MAIL_TEST_DSN")
	if dsn == "" {
		t.Skip("ICLOUD_MAIL_TEST_DSN is empty")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	account := domain.Account{ID: "acc-delete-" + now.Format("150405.000000"), AppleID: "delete@icloud.com", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	item := domain.Mailbox{
		ID: "mb-delete-" + now.Format("150405.000000"), AccountID: account.ID, Email: "delete-" + now.Format("150405.000000") + "@icloud.com",
		Status: domain.StatusUsed, APIToken: "keep-token", APIActive: true, ICloudActive: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.InsertMailbox(ctx, item); err != nil {
		t.Fatal(err)
	}
	err = db.DeleteAccount(ctx, account.ID)
	var appErr *apperr.Error
	if err == nil || !errors.As(err, &appErr) || appErr.Code != "account_has_mailboxes" {
		t.Fatalf("delete with mailbox err = %v", err)
	}
	if err := db.UpdateMailboxAdmin(ctx, item.ID, "label", "note", false, now); err != nil {
		t.Fatal(err)
	}
	updated, ok, err := db.GetMailbox(ctx, item.ID)
	if err != nil || !ok || updated.Status != domain.StatusUsed || updated.APIToken != "keep-token" || !updated.ICloudActive || updated.Label != "label" || updated.APIActive {
		t.Fatalf("admin update changed claim state: %#v err=%v", updated, err)
	}
	if _, err := db.pool.Exec(ctx, `DELETE FROM mailboxes WHERE id = $1`, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAccount(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresMessageMergeAndCursor(t *testing.T) {
	dsn := os.Getenv("ICLOUD_MAIL_TEST_DSN")
	if dsn == "" {
		t.Skip("ICLOUD_MAIL_TEST_DSN is empty")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	suffix := strings.ReplaceAll(now.Format("150405.000000"), ".", "")
	account := domain.Account{ID: "acc-merge-" + suffix, AppleID: "old@icloud.com", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.pool.Exec(ctx, `DELETE FROM messages WHERE mailbox_id LIKE $1`, "box-merge-"+suffix+"%")
		_, _ = db.pool.Exec(ctx, `DELETE FROM mailboxes WHERE account_id = $1`, account.ID)
		_, _ = db.pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, account.ID)
	}()
	if _, err := db.SaveSession(ctx, account.ID, protocol.ICloudSession{AppleID: "old@icloud.com", Cookies: []protocol.SessionCookie{{Name: "X-APPLE-WEBAUTH-TOKEN", Value: "token", Domain: "icloud.com"}}}); err != nil {
		t.Fatal(err)
	}
	changed := account
	changed.AppleID = "new@icloud.com"
	changed.UpdatedAt = now
	if err := db.UpdateAccount(ctx, changed, false); err != nil {
		t.Fatal(err)
	}
	var sessionJSON string
	if err := db.pool.QueryRow(ctx, `SELECT session_json::text FROM accounts WHERE id = $1`, account.ID).Scan(&sessionJSON); err != nil {
		t.Fatal(err)
	}
	if sessionJSON != "{}" {
		t.Fatalf("session after apple id change = %s", sessionJSON)
	}

	box := domain.Mailbox{
		ID: "box-merge-" + suffix, AccountID: account.ID, Email: "merge-" + suffix + "@icloud.com",
		Status: domain.StatusAvailable, APIToken: "token", APIActive: true, ICloudActive: true,
		RemoteAnonymousID: "web-anon", RemoteOrigin: domain.OriginICloudWeb, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.InsertMailbox(ctx, box); err != nil {
		t.Fatal(err)
	}
	known := now
	if err := db.UpsertMessages(ctx, box.ID, []domain.Message{{
		ID: "msg-merge-" + suffix, CanonicalID: "mid:otp@example.com", RemoteID: "imap:15", UID: "15",
		Subject: "Your code", From: "OpenAI", Body: "OpenAI code 123456", Source: "imap", ReceivedAt: known,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, box.ID, []domain.Message{{
		ID: "msg-merge-late-" + suffix, CanonicalID: "mid:otp@example.com", UID: "web-9",
		Body: "preview only", Source: "web", ReceivedAt: time.Time{},
	}}); err != nil {
		t.Fatal(err)
	}
	messages, err := db.ListMessages(ctx, box.ID, time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || !messages[0].ReceivedAt.Equal(known) || protocol.ExtractOTP(messages[0].Body) != "123456" || messages[0].UID != "15" || messages[0].Source != "imap" {
		t.Fatalf("merged message = %#v", messages)
	}
	if err := db.UpsertMessages(ctx, box.ID, []domain.Message{{
		ID: "msg-unknown-" + suffix, CanonicalID: "mid:unknown@example.com", Body: "OpenAI code 654321", Source: "web",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeleteOldMessages(ctx, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	unknownKept, err := db.ListMessages(ctx, box.ID, time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(unknownKept) != 1 || unknownKept[0].CanonicalID != "mid:unknown@example.com" {
		t.Fatalf("unknown date was deleted: %#v", unknownKept)
	}

	if err := db.TouchSync(ctx, box.ID, "20", now); err != nil {
		t.Fatal(err)
	}
	if err := db.TouchSync(ctx, box.ID, "9", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := db.TouchSync(ctx, box.ID, "web-9", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	synced, ok, err := db.GetMailbox(ctx, box.ID)
	if err != nil || !ok || synced.LastSyncUID != "20" {
		t.Fatalf("cursor = %#v err=%v", synced, err)
	}

	apple := box
	apple.ID = "box-apple-" + suffix
	apple.Email = "apple-" + suffix + "@icloud.com"
	apple.RemoteAnonymousID = "apple-anon"
	apple.RemoteOrigin = domain.OriginAppleAccount
	if err := db.InsertMailbox(ctx, apple); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkRemotePresent(ctx, apple.ID, "web-overwrite", domain.OriginICloudWeb, false); err != nil {
		t.Fatal(err)
	}
	guarded, ok, err := db.GetMailbox(ctx, apple.ID)
	if err != nil || !ok || guarded.RemoteAnonymousID != "apple-anon" || guarded.RemoteOrigin != domain.OriginAppleAccount || !guarded.ICloudActive {
		t.Fatalf("cross-origin overwrite = %#v err=%v", guarded, err)
	}

	if err := db.BeginRemoteDelete(ctx, box.ID, now.Add(-6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NormalizeStalePendingDeletes(ctx, now); err != nil {
		t.Fatal(err)
	}
	stale, ok, err := db.GetMailbox(ctx, box.ID)
	if err != nil || !ok || stale.RemoteDeleteStatus != domain.DeleteUnknown {
		t.Fatalf("stale pending = %#v err=%v", stale, err)
	}
}

func TestClaimCutsCodesBeforeOwnership(t *testing.T) {
	dsn := os.Getenv("ICLOUD_MAIL_TEST_DSN")
	if dsn == "" {
		t.Skip("ICLOUD_MAIL_TEST_DSN is empty")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `TRUNCATE events, messages, mailbox_leases, mailboxes, accounts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	account := domain.Account{ID: "acc-cut", AppleID: "cut@icloud.com", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	box := domain.Mailbox{
		ID: "box-cut", AccountID: account.ID, Email: "cut@icloud.com", Status: domain.StatusAvailable,
		APIToken: "cut-token", APIActive: true, ICloudActive: true, RemoteOrigin: domain.OriginICloudWeb, CreatedAt: now,
	}
	if err := db.InsertMailbox(ctx, box); err != nil {
		t.Fatal(err)
	}
	claimAt := now.Add(time.Minute)
	if err := db.UpsertMessages(ctx, box.ID, []domain.Message{
		{ID: "msg-before", CanonicalID: "mid:before@example.com", Subject: "before", ReceivedAt: claimAt.Add(-time.Second)},
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := db.Claim(ctx, domain.ClaimInput{Project: "reg", RequestID: "req-cut", Now: claimAt, TTL: time.Minute})
	if err != nil || !claimed.Created || claimed.Mailbox.CodeNotBefore == nil || !claimed.Mailbox.CodeNotBefore.Equal(claimAt) {
		t.Fatalf("claim = %#v err=%v", claimed, err)
	}
	old, err := db.ListMessages(ctx, box.ID, time.Time{}, 50)
	if err != nil || len(old) != 0 {
		t.Fatalf("pre-claim messages = %d err=%v", len(old), err)
	}
	if err := db.UpsertMessages(ctx, box.ID, []domain.Message{
		{ID: "msg-after", CanonicalID: "mid:after@example.com", Subject: "after", ReceivedAt: claimAt.Add(time.Second)},
	}); err != nil {
		t.Fatal(err)
	}
	replay, err := db.Claim(ctx, domain.ClaimInput{Project: "reg", RequestID: "req-cut", Now: claimAt.Add(30 * time.Second), TTL: time.Minute})
	if err != nil || replay.Created || !replay.Idempotent {
		t.Fatalf("replay = %#v err=%v", replay, err)
	}
	kept, err := db.ListMessages(ctx, box.ID, time.Time{}, 50)
	if err != nil || len(kept) != 1 || kept[0].ID != "msg-after" {
		t.Fatalf("replay deleted the current code: %#v err=%v", kept, err)
	}
	held, ok, err := db.GetMailbox(ctx, box.ID)
	if err != nil || !ok || held.CodeNotBefore == nil || !held.CodeNotBefore.Equal(claimAt) {
		t.Fatalf("replay moved the floor: %#v err=%v", held.CodeNotBefore, err)
	}
}
