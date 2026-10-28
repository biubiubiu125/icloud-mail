package mailbox

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
)

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
