package mailbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

type channelAuth struct {
	webCalls   int
	appleCalls int
}

func (a *channelAuth) StartICloudWebLogin(context.Context, string, string, string, string, string, string, *protocol.PendingLogins) (protocol.LoginStart, error) {
	return protocol.LoginStart{}, nil
}
func (a *channelAuth) FinishICloudWebLogin(context.Context, string, string, *protocol.PendingLogins) (protocol.ICloudSession, error) {
	a.webCalls++
	return protocol.ICloudSession{}, errors.New("should not finish")
}
func (a *channelAuth) StartAppleAccountLogin(context.Context, string, string, string, string, *protocol.PendingLogins) (protocol.LoginStart, error) {
	return protocol.LoginStart{}, nil
}
func (a *channelAuth) FinishAppleAccountLogin(context.Context, string, string, *protocol.PendingLogins) (protocol.ICloudSession, error) {
	a.appleCalls++
	return protocol.ICloudSession{}, errors.New("should not finish")
}

func TestPendingFinishRejectsOtherChannel(t *testing.T) {
	now := time.Now()
	service := &Service{Now: func() time.Time { return now }}
	service.bindLoginPending("pending-web", pendingOwner{
		accountID: "acc-1",
		channel:   loginChannelWeb,
		expiresAt: now.Add(time.Minute),
	})
	if err := service.pendingOwnedByChannel("pending-web", "acc-1", loginChannelWeb); err != nil {
		t.Fatal(err)
	}
	err := service.pendingOwnedByChannel("pending-web", "acc-1", loginChannelApple)
	var appErr *apperr.Error
	if err == nil || !errors.As(err, &appErr) || appErr.Code != "login_pending_channel_mismatch" {
		t.Fatalf("other channel err = %v", err)
	}
}

func TestFinishLoginDoesNotCrossChannel(t *testing.T) {
	now := time.Now()
	auth := &channelAuth{}
	service := &Service{Now: func() time.Time { return now }, Auth: auth}
	service.bindLoginPending("pending-web", pendingOwner{
		accountID: "acc-1",
		channel:   loginChannelWeb,
		expiresAt: now.Add(time.Minute),
	})
	_, err := service.FinishAppleLogin(context.Background(), "acc-1", "pending-web", "123456")
	var appErr *apperr.Error
	if err == nil || !errors.As(err, &appErr) || appErr.Code != "login_pending_channel_mismatch" || auth.appleCalls != 0 || auth.webCalls != 0 {
		t.Fatalf("err=%v web=%d apple=%d", err, auth.webCalls, auth.appleCalls)
	}
}

func TestEffectivePublicBaseURLMatchesExportFallback(t *testing.T) {
	if got := EffectivePublicBaseURL("https://settings.example/", "https://env.example"); got != "https://settings.example" {
		t.Fatalf("stored base = %q", got)
	}
	if got := EffectivePublicBaseURL("  ", "https://env.example/"); got != "https://env.example" {
		t.Fatalf("fallback base = %q", got)
	}
	if got := EffectivePublicBaseURL("", ""); got != "" {
		t.Fatalf("empty base = %q", got)
	}
}

func TestPendingOwnerRejectsOtherAccount(t *testing.T) {
	now := time.Now()
	service := &Service{Now: func() time.Time { return now }}
	service.bindPending("pending-1", "acc-1")
	if err := service.pendingOwnedBy("pending-1", "acc-1"); err != nil {
		t.Fatal(err)
	}
	err := service.pendingOwnedBy("pending-1", "acc-2")
	var appErr *apperr.Error
	if err == nil || !errors.As(err, &appErr) || appErr.Code != "login_pending_account_mismatch" {
		t.Fatalf("other account err = %v", err)
	}
	service.Now = func() time.Time { return now.Add(11 * time.Minute) }
	err = service.pendingOwnedBy("pending-1", "acc-1")
	if err == nil || !errors.As(err, &appErr) || appErr.Code != "login_pending_expired" {
		t.Fatalf("expired err = %v", err)
	}
}

func TestLaterUIDKeepsNumericCursor(t *testing.T) {
	if got := laterUID("12", ""); got != "12" {
		t.Fatalf("empty next = %s", got)
	}
	if got := laterUID("12", "9"); got != "12" {
		t.Fatalf("older next = %s", got)
	}
	if got := laterUID("12", "20"); got != "20" {
		t.Fatalf("newer next = %s", got)
	}
	if got := laterUID("12", "not-a-uid"); got != "12" {
		t.Fatalf("non numeric replaced cursor = %s", got)
	}
	if got := laterUID("12", "uv:9:20"); got != "uv:9:20" {
		t.Fatalf("validity cursor = %s", got)
	}
	if got := laterUID("uv:9:20", "uv:8:100"); got != "uv:8:100" {
		t.Fatalf("validity reset = %s", got)
	}
	if got := laterUID("uv:9:20", "15"); got != "uv:9:20" {
		t.Fatalf("older plain uid replaced cursor = %s", got)
	}
}

func TestUnresolvedTargetDoesNotMatchEmptyClear(t *testing.T) {
	id, email, err := reconciliationMarkTarget("", "")
	if err != nil {
		t.Fatal(err)
	}
	if email != "" || !strings.HasPrefix(id, "unresolved:") || id == "unresolved:" {
		t.Fatalf("target = %s %s", id, email)
	}
	account := domain.Account{ReconciliationAnonymousID: id, ReconciliationEmail: email}
	if reconciliationTargetFound(account, nil) {
		t.Fatal("sentinel matched an empty remote list")
	}
}

func TestAcceptCreateOriginDoesNotGuessWeb(t *testing.T) {
	origin, err := acceptCreateOrigin("weird", "icloud_web")
	if err == nil || origin == domain.OriginICloudWeb {
		t.Fatalf("origin=%s err=%v", origin, err)
	}
	origin, err = acceptCreateOrigin("", "apple_account")
	if err != nil || origin != domain.OriginAppleAccount {
		t.Fatalf("empty channel origin=%s err=%v", origin, err)
	}
	if _, err := knownListOrigin("weird"); err == nil {
		t.Fatal("unknown list origin was accepted")
	}
	origin, err = knownListOrigin("")
	if err != nil || origin != domain.OriginICloudWeb {
		t.Fatalf("empty list origin=%s err=%v", origin, err)
	}
}

func TestAPIURLWithoutBaseIsRelative(t *testing.T) {
	got := JoinAPIURL("", "a@icloud.com", "tok")
	if strings.Contains(got, "://") || !strings.HasPrefix(got, "/api/v1/mailboxes/") {
		t.Fatal(got)
	}
}
