package store

import (
	"testing"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

func TestMergeObservedMessageKeepsKnownDateAndOTP(t *testing.T) {
	known := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	existing := domain.Message{
		ReceivedAt: known,
		Body:       "OpenAI code 123456",
		Subject:    "Your code",
		Source:     "imap",
		UID:        "15",
		RemoteID:   "imap:15",
	}
	incoming := domain.Message{
		ReceivedAt: time.Time{},
		Body:       "preview only",
		Subject:    "",
		Source:     "web",
		UID:        "web-9",
		RemoteID:   "",
	}
	got := mergeObservedMessage(existing, incoming)
	if !got.ReceivedAt.Equal(known) {
		t.Fatalf("received_at = %s, want %s", got.ReceivedAt, known)
	}
	if protocol.ExtractOTP(got.Body) != "123456" {
		t.Fatalf("body lost otp: %q", got.Body)
	}
	if got.Source != "imap" {
		t.Fatalf("source = %q", got.Source)
	}
	if got.UID != "15" {
		t.Fatalf("uid = %q", got.UID)
	}
	if got.RemoteID != "imap:15" {
		t.Fatalf("remote id cleared: %q", got.RemoteID)
	}
}

func TestMergeObservedMessageRepairsUnknownDate(t *testing.T) {
	known := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	existing := domain.Message{ReceivedAt: time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), Body: ""}
	incoming := domain.Message{ReceivedAt: known, Body: "OpenAI code 654321", Source: "imap"}
	got := mergeObservedMessage(existing, incoming)
	if !got.ReceivedAt.Equal(known) {
		t.Fatalf("received_at = %s", got.ReceivedAt)
	}
	if protocol.ExtractOTP(got.Body) != "654321" {
		t.Fatalf("body = %q", got.Body)
	}
	if got.Source != "imap" {
		t.Fatalf("source = %q", got.Source)
	}
}

func TestMergeObservedMessageDoesNotMoveDateLater(t *testing.T) {
	earlier := time.Date(2026, 10, 19, 11, 0, 0, 0, time.UTC)
	later := earlier.Add(time.Hour)
	got := mergeObservedMessage(
		domain.Message{ReceivedAt: later, Body: "OpenAI code 123456"},
		domain.Message{ReceivedAt: earlier, Body: "OpenAI code 123456"},
	)
	if !got.ReceivedAt.Equal(earlier) {
		t.Fatalf("received_at = %s, want earlier %s", got.ReceivedAt, earlier)
	}
}

func TestUnknownReceivedAtIsNotExpired(t *testing.T) {
	cutoff := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if messageExpired(time.Time{}, cutoff) || messageExpired(time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), cutoff) {
		t.Fatal("unknown date was treated as expired")
	}
	if !messageExpired(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), cutoff) {
		t.Fatal("real old date was kept")
	}
	if messageExpired(cutoff, cutoff) {
		t.Fatal("cutoff itself was deleted")
	}
}

func TestAppleIDChangeClearsSession(t *testing.T) {
	if !appleIDChanged("Old@icloud.com", "new@icloud.com") {
		t.Fatal("apple id change was ignored")
	}
	if appleIDChanged("A@icloud.com", " a@icloud.com ") {
		t.Fatal("case or space cleared the session")
	}
}

func TestPendingDeleteBecomesUnknownAfterDeadline(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	if !pendingDeleteStale(now.Add(-6*time.Minute), now) {
		t.Fatal("old pending stayed pending")
	}
	if pendingDeleteStale(now.Add(-time.Minute), now) {
		t.Fatal("fresh pending was swept")
	}
	if !pendingDeleteStale(time.Time{}, now) {
		t.Fatal("pending without a timestamp was left stuck")
	}
}
