package mailbox

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

func TestPersistContextSurvivesParentCancel(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	ctx, stop := PersistContext(parent)
	defer stop()
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
}

func TestStopCreateDetachesCanceledRequest(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	old := persistContext
	persistContext = func(ctx context.Context) (context.Context, context.CancelFunc) {
		called = true
		if ctx.Err() == nil {
			t.Error("parent context was not canceled")
		}
		return PersistContext(ctx)
	}
	t.Cleanup(func() { persistContext = old })

	svc := &Service{}
	func() {
		defer func() { _ = recover() }()
		_ = svc.stopCreate(parent, "acc-detach", domain.OriginICloudWeb, "canceled", "anon", "a@icloud.com")
	}()
	if !called {
		t.Fatal("stopCreate did not detach the request context")
	}
}

func TestPersistFailureHaltsFurtherCreates(t *testing.T) {
	provider := &haltProvider{}
	svc := &Service{Provider: provider}
	svc.haltCreate("acc-1")
	if !svc.createHalted("acc-1") {
		t.Fatal("account was not halted")
	}
	if svc.createHalted("acc-2") {
		t.Fatal("unrelated account was halted")
	}
	_, err := svc.CreateMailbox(context.Background(), "acc-1", "", "", "")
	if provider.creates != 0 {
		t.Fatal("halted account still called Apple")
	}
	var app *apperr.Error
	if !errors.As(err, &app) || app.Code != "mailbox_create_reconciliation_persist_failed" {
		t.Fatalf("err=%v", err)
	}
}

func TestAbortUntrackedCreateMarksBeforeReturningCause(t *testing.T) {
	marked := false
	cause := errors.New("token failed")
	err := abortUntrackedCreate(cause, func() error {
		marked = true
		return nil
	})
	if !marked || !errors.Is(err, cause) {
		t.Fatalf("marked=%v err=%v", marked, err)
	}
	markErr := errors.New("mark failed")
	err = abortUntrackedCreate(cause, func() error { return markErr })
	if !errors.Is(err, markErr) {
		t.Fatalf("err=%v", err)
	}
}

func TestRemoteDeleteMarkUsesLiveContext(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	err := persistCall(parent, func(ctx context.Context) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		status, _ := remoteDeleteMark(context.Canceled)
		if status != domain.DeleteUnknown {
			return errors.New(status)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExpireOnceLogsStoreErrors(t *testing.T) {
	var buf bytes.Buffer
	svc := &Service{
		logger:  slog.New(slog.NewTextHandler(&buf, nil)),
		expirer: failExpire{},
		Now:     func() time.Time { return time.Unix(0, 0).UTC() },
	}
	if got := svc.expireOnce(context.Background()); got != 30*time.Second {
		t.Fatalf("wait=%s", got)
	}
	text := buf.String()
	if !strings.Contains(text, "lease db down") || !strings.Contains(text, "retention db down") {
		t.Fatalf("log=%s", text)
	}
	if strings.Contains(text, "postgres://") || strings.Contains(text, "password=") {
		t.Fatalf("secret in log=%s", text)
	}
}

func TestBackgroundLogRedactsDatabaseURL(t *testing.T) {
	var buf bytes.Buffer
	logBackground(slog.New(slog.NewTextHandler(&buf, nil)), "expire", "expire_leases", errors.New("connect postgres://user:password=secret@db/mail failed"))
	text := buf.String()
	if strings.Contains(text, "password=") || strings.Contains(text, "postgres://") {
		t.Fatalf("secret in log=%s", text)
	}
	if !strings.Contains(text, "database error") {
		t.Fatalf("log=%s", text)
	}
}

type haltProvider struct {
	creates int
}

func (p *haltProvider) CheckIMAP(context.Context, string, string, string) error { return nil }
func (p *haltProvider) Delete(context.Context, protocol.ICloudSession, string, string, string) (protocol.ICloudSession, error) {
	return protocol.ICloudSession{}, errors.New("unused")
}
func (p *haltProvider) Create(context.Context, protocol.ICloudSession, string, string, string, string) (protocol.ICloudRemoteMailbox, protocol.ICloudSession, error) {
	p.creates++
	return protocol.ICloudRemoteMailbox{}, protocol.ICloudSession{}, errors.New("should not create")
}
func (p *haltProvider) List(context.Context, protocol.ICloudSession, string, string) ([]protocol.ICloudRemoteMailbox, protocol.ICloudSession, error) {
	return nil, protocol.ICloudSession{}, errors.New("unused")
}
func (p *haltProvider) SyncIMAP(context.Context, protocol.LoginState, []protocol.Mailbox, time.Time, string, int) (map[string][]protocol.ICloudSyncedMessage, string, error) {
	return nil, "", errors.New("unused")
}
func (p *haltProvider) SyncWeb(context.Context, protocol.ICloudSession, protocol.Mailbox, time.Time, string, int) ([]protocol.ICloudSyncedMessage, error) {
	return nil, errors.New("unused")
}

type failExpire struct{}

func (failExpire) ExpireHeldLeases(context.Context, time.Time) (int64, error) {
	return 0, errors.New("lease db down")
}
func (failExpire) GetSettings(context.Context) (domain.Settings, error) {
	return domain.Settings{MessageRetentionDays: 7}, nil
}
func (failExpire) DeleteOldMessages(context.Context, time.Time) (int64, error) {
	return 0, errors.New("retention db down")
}
func (failExpire) NormalizeStalePendingDeletes(context.Context, time.Time) (int64, error) {
	return 0, nil
}
