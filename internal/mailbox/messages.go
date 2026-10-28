package mailbox

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

const maxStoredBody = 200000

var messageIDPattern = regexp.MustCompile(`(?i)message-id:\s*<?([^>\s]+)>?`)

func canonicalID(msg protocol.ICloudSyncedMessage) string {
	if id := normalizeMessageID(msg.MessageID); id != "" {
		return "mid:" + id
	}
	if match := messageIDPattern.FindStringSubmatch(msg.Body); len(match) == 2 {
		if id := normalizeMessageID(match[1]); id != "" {
			return "mid:" + id
		}
	}
	if remote := strings.TrimSpace(msg.RemoteID); remote != "" {
		return "remote:" + remote
	}
	if uid := strings.TrimSpace(msg.UID); uid != "" {
		return "uid:" + uid
	}
	return ""
}

func normalizeMessageID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Trim(value, "<>")
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t\r\n") || !strings.Contains(value, "@") {
		return ""
	}
	return value
}

func nextSyncUID(current, source, protocolCursor string, _ []string) string {
	if source == "imap" {
		return laterUID(current, protocolCursor)
	}
	// Web metadata UIDs are not IMAP UIDs. Writing them into last_sync_uid
	// makes a later IMAP search start past the real mailbox.
	return current
}

func laterUID(current, next string) string {
	return domain.LaterUID(current, next)
}

func toStoredMessage(mailboxID string, msg protocol.ICloudSyncedMessage) (domain.Message, bool) {
	id := canonicalID(msg)
	if id == "" {
		return domain.Message{}, false
	}
	body := msg.Body
	if utf8.RuneCountInString(body) > maxStoredBody {
		body = string([]rune(body)[:maxStoredBody])
	}
	received := msg.ReceivedAt
	sum := sha256.Sum256([]byte(mailboxID + "|" + id))
	return domain.Message{
		ID:          hex.EncodeToString(sum[:16]),
		MailboxID:   mailboxID,
		RemoteID:    msg.RemoteID,
		CanonicalID: id,
		UID:         msg.UID,
		Subject:     msg.Subject,
		From:        msg.From,
		Body:        body,
		ReceivedAt:  received,
	}, true
}

func codeFromMessages(items []domain.Message, keyword string, after, notBefore time.Time, freshFor time.Duration, now time.Time, allowStale bool) (domain.CodeResult, bool, bool) {
	keyword = strings.TrimSpace(keyword)
	var stale domain.CodeResult
	hasStale := false
	for _, item := range items {
		if !notBefore.IsZero() && !item.ReceivedAt.After(notBefore) {
			continue
		}
		if !after.IsZero() && !item.ReceivedAt.After(after) {
			continue
		}
		text := item.Subject + "\n" + item.From + "\n" + item.Body
		if keyword != "" && keyword != "*" && !strings.Contains(strings.ToLower(text), strings.ToLower(keyword)) {
			continue
		}
		code := protocol.ExtractOTP(text)
		if code == "" {
			continue
		}
		result := domain.CodeResult{
			Code:       code,
			Subject:    item.Subject,
			From:       item.From,
			ReceivedAt: item.ReceivedAt,
			Source:     item.Source,
		}
		if freshFor > 0 && now.Sub(item.ReceivedAt) > freshFor && after.IsZero() {
			if !hasStale {
				stale = result
				hasStale = true
			}
			continue
		}
		return result, true, false
	}
	if allowStale && hasStale {
		stale.StaleCache = true
		return stale, true, true
	}
	return domain.CodeResult{}, false, hasStale
}
