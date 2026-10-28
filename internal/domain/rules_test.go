package domain

import (
	"testing"
	"time"
)

func TestClassifyClaimReplay(t *testing.T) {
	now := time.Now()
	lease := Lease{ID: "lease-1", MailboxID: "box-1", State: LeaseHeld, ExpiresAt: now.Add(time.Minute)}
	mailbox := Mailbox{ID: "box-1", AccountID: "acc-1", Status: StatusUsed, ActiveLeaseID: "lease-1", APIActive: true, ICloudActive: true}
	if got := ClassifyClaimReplay(lease, mailbox, now, ""); got != ClaimReplayReturn {
		t.Fatalf("held exclusive = %d", got)
	}
	if got := ClassifyClaimReplay(lease, mailbox, now, "other"); got != ClaimReplayRefuse {
		t.Fatalf("account mismatch = %d", got)
	}
	expired := lease
	expired.ExpiresAt = now.Add(-time.Minute)
	if got := ClassifyClaimReplay(expired, mailbox, now, ""); got != ClaimReplayRefuse {
		t.Fatalf("expired pinned must not extend, got %d", got)
	}
	released := lease
	released.State = LeaseReleased
	free := mailbox
	free.Status = StatusAvailable
	free.ActiveLeaseID = ""
	if got := ClassifyClaimReplay(released, free, now, ""); got != ClaimReplayRehold {
		t.Fatalf("reclaimable = %d", got)
	}
	taken := free
	taken.Status = StatusUsed
	taken.ActiveLeaseID = "other-lease"
	if got := ClassifyClaimReplay(released, taken, now, ""); got != ClaimReplayRefuse {
		t.Fatalf("taken = %d", got)
	}
}

func TestResolveLeaseTTLUsesSettingsWhenOmitted(t *testing.T) {
	if got := ResolveLeaseTTL(0, 5); got != 5*time.Minute {
		t.Fatalf("omitted = %s", got)
	}
	if got := ResolveLeaseTTL(120, 5); got != 120*time.Minute {
		t.Fatalf("explicit = %s", got)
	}
}
