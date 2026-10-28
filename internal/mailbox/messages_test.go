package mailbox

import (
	"testing"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

func fixedNow() time.Time {
	return time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
}

const timeHour = time.Hour

func TestExtractOTP(t *testing.T) {
	if got := protocol.ExtractOTP("Your OpenAI code is 123456"); got != "123456" {
		t.Fatalf("got %q", got)
	}
	if got := protocol.ExtractOTP("验证码：654321"); got != "654321" {
		t.Fatalf("got %q", got)
	}
	if got := protocol.ExtractOTP("123456 is your OpenAI code"); got != "123456" {
		t.Fatalf("reverse context got %q", got)
	}
	if got := protocol.ExtractOTP("reference 000000 only"); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := protocol.ExtractOTP("order 123456 and ticket 654321"); got != "" {
		t.Fatalf("bare digits were treated as a code: %q", got)
	}
}

func TestCanonicalIDPrefersMessageID(t *testing.T) {
	id := canonicalID(protocol.ICloudSyncedMessage{Body: "Message-ID: <ABC@example.com>\ncode 123456", RemoteID: "imap:1"})
	if id != "mid:abc@example.com" {
		t.Fatalf("got %q", id)
	}
}

func TestCanonicalIDUsesSavedHeaderAcrossChannels(t *testing.T) {
	receivedAt := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	fromIMAP := canonicalID(protocol.ICloudSyncedMessage{
		MessageID: "<Message-42@Example.COM>", RemoteID: "imap:9", From: "sender@example.com", ReceivedAt: receivedAt,
	})
	fromWeb := canonicalID(protocol.ICloudSyncedMessage{
		MessageID: "message-42@example.com", RemoteID: "icloud:INBOX:9", From: "other@example.com", Subject: "不同主题", ReceivedAt: receivedAt.Add(time.Hour),
	})
	if fromIMAP != fromWeb || fromIMAP != "mid:message-42@example.com" {
		t.Fatalf("IMAP=%q Web=%q", fromIMAP, fromWeb)
	}
	if got := canonicalID(protocol.ICloudSyncedMessage{MessageID: "12345", RemoteID: "icloud:INBOX:1"}); got != "remote:icloud:INBOX:1" {
		t.Fatalf("internal id must not become a message id, got %q", got)
	}
}

func TestCodeKeywordFilter(t *testing.T) {
	items := []domain.Message{{
		Subject:    "hello",
		Body:       "code 222333",
		ReceivedAt: fixedNow(),
	}}
	_, ok, _ := codeFromMessages(items, "OpenAI", time.Time{}, time.Time{}, domain.CodeFreshWindow, fixedNow(), false)
	if ok {
		t.Fatal("keyword miss must not return a code")
	}
	result, ok, _ := codeFromMessages(items, "*", time.Time{}, time.Time{}, domain.CodeFreshWindow, fixedNow(), false)
	if !ok || result.Code != "222333" {
		t.Fatalf("result = %#v ok=%v", result, ok)
	}
}

func TestUndatedMessageDoesNotUseSyncTime(t *testing.T) {
	now := fixedNow()
	stored, ok := toStoredMessage("box", protocol.ICloudSyncedMessage{
		MessageID: "otp@example.com",
		Subject:   "OpenAI code 333444",
		RemoteID:  "imap:1",
		UID:       "1",
	})
	if !ok {
		t.Fatal("expected stored")
	}
	if !stored.ReceivedAt.IsZero() || stored.ReceivedAt.Equal(now) {
		t.Fatalf("undated received_at = %s", stored.ReceivedAt)
	}
	if _, ok, _ := codeFromMessages([]domain.Message{stored}, "OpenAI", time.Time{}, now.Add(-time.Minute), domain.CodeFreshWindow, now, true); ok {
		t.Fatal("undated mail must not pass the floor")
	}
}

func TestIMAPCursorDoesNotJumpToNewestSample(t *testing.T) {
	if got := nextSyncUID("5", "imap", "20", []string{"100"}); got != "20" {
		t.Fatalf("cursor = %q", got)
	}
	if got := nextSyncUID("5", "imap", "", []string{"100"}); got != "5" {
		t.Fatalf("empty cursor = %q", got)
	}
	if got := nextSyncUID("5", "web", "", []string{"9"}); got != "5" {
		t.Fatalf("web cursor = %q", got)
	}
	if got := nextSyncUID("", "web", "", []string{"100"}); got != "" {
		t.Fatalf("empty web cursor = %q", got)
	}
}

func TestCodeFloorHidesEarlierMail(t *testing.T) {
	now := fixedNow()
	floor := now.Add(-30 * time.Second)
	old := domain.Message{Subject: "OpenAI code 111111", ReceivedAt: now.Add(-time.Minute)}
	fresh := domain.Message{Subject: "OpenAI code 222222", ReceivedAt: now.Add(-20 * time.Second)}
	result, ok, _ := codeFromMessages([]domain.Message{old, fresh}, "OpenAI", time.Time{}, floor, domain.CodeFreshWindow, now, true)
	if !ok || result.Code != "222222" {
		t.Fatalf("fresh = %#v ok=%v", result, ok)
	}
	_, ok, _ = codeFromMessages([]domain.Message{old}, "OpenAI", time.Unix(0, 0).UTC(), floor, domain.CodeFreshWindow, now, true)
	if ok {
		t.Fatal("after must not see mail at or before the floor")
	}
}
