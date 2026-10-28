package mailbox

import (
	"testing"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/domain"
)

func TestPlanDeleteRefusesUnknownOrigin(t *testing.T) {
	plan := planDelete(domain.Mailbox{RemoteOrigin: "OTHER", RemoteAnonymousID: "anon"}, DeleteOptions{Remote: true})
	if plan.Action != deleteRefuse {
		t.Fatalf("action = %v, want refuse", plan.Action)
	}
	if plan.Err == nil || plan.Err.Code != "remote_origin_unknown" {
		t.Fatalf("unexpected error: %#v", plan.Err)
	}
}

func TestPlanDeleteEmptyOriginUsesWeb(t *testing.T) {
	plan := planDelete(domain.Mailbox{RemoteAnonymousID: "anon"}, DeleteOptions{Remote: true})
	if plan.Action != deleteProvider || plan.Origin != domain.OriginICloudWeb {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanDeleteUnknownNeedsConfirm(t *testing.T) {
	mailbox := domain.Mailbox{RemoteOrigin: domain.OriginAppleAccount, RemoteAnonymousID: "anon", RemoteDeleteStatus: domain.DeleteUnknown}
	blocked := planDelete(mailbox, DeleteOptions{Remote: true})
	if blocked.Action != deleteConflict {
		t.Fatalf("blocked action = %v", blocked.Action)
	}
	allowed := planDelete(mailbox, DeleteOptions{Remote: true, ConfirmUnknown: true})
	if allowed.Action != deleteProvider {
		t.Fatalf("allowed action = %v", allowed.Action)
	}
}

func TestPlanDeleteLocalOnlyNeedsConfirm(t *testing.T) {
	mailbox := domain.Mailbox{RemoteOrigin: domain.OriginICloudWeb, RemoteAnonymousID: "anon"}
	blocked := planDelete(mailbox, DeleteOptions{Remote: false})
	if blocked.Err == nil || blocked.Err.Code != "remote_delete_required" {
		t.Fatalf("unexpected: %#v", blocked.Err)
	}
	allowed := planDelete(mailbox, DeleteOptions{Remote: false, ConfirmLocal: true})
	if allowed.Action != deleteLocal {
		t.Fatalf("action = %v", allowed.Action)
	}
}

func TestCodeFromMessagesRejectsZeroCode(t *testing.T) {
	items := []domain.Message{{Subject: "OpenAI code 000000", Body: "验证码 000000", ReceivedAt: fixedNow()}}
	_, ok, _ := codeFromMessages(items, "OpenAI", time.Time{}, time.Time{}, domain.CodeFreshWindow, fixedNow(), true)
	if ok {
		t.Fatal("000000 must not be a code")
	}
}
