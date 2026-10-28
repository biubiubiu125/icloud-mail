package store

import (
	"strings"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

const stalePendingDeleteAfter = 5 * time.Minute

func knownReceivedAt(value time.Time) bool {
	return !value.IsZero() && value.Year() > 1
}

func mergeObservedMessage(existing, incoming domain.Message) domain.Message {
	merged := existing
	merged.RemoteID = preferObservedRemoteID(existing.RemoteID, incoming.RemoteID)
	merged.UID = domain.LaterUID(existing.UID, incoming.UID)
	merged.Subject = preferObservedText(existing.Subject, incoming.Subject)
	merged.From = preferObservedText(existing.From, incoming.From)
	merged.Body = preferObservedBody(existing.Body, incoming.Body)
	merged.Source = preferObservedSource(existing.Source, incoming.Source)
	merged.ReceivedAt = mergeReceivedAt(existing.ReceivedAt, incoming.ReceivedAt)
	return merged
}

func mergeReceivedAt(existing, incoming time.Time) time.Time {
	existingKnown := knownReceivedAt(existing)
	incomingKnown := knownReceivedAt(incoming)
	switch {
	case existingKnown && incomingKnown:
		if incoming.Before(existing) {
			return incoming
		}
		return existing
	case incomingKnown:
		return incoming
	default:
		return existing
	}
}

func preferObservedBody(existing, incoming string) string {
	if strings.TrimSpace(incoming) == "" {
		return existing
	}
	if strings.TrimSpace(existing) == "" {
		return incoming
	}
	if protocol.ExtractOTP(existing) != "" && protocol.ExtractOTP(incoming) == "" {
		return existing
	}
	return incoming
}

func preferObservedText(existing, incoming string) string {
	if strings.TrimSpace(incoming) == "" {
		return existing
	}
	return incoming
}

func preferObservedSource(existing, incoming string) string {
	if strings.TrimSpace(existing) != "" {
		return existing
	}
	return strings.TrimSpace(incoming)
}

func preferObservedRemoteID(existing, incoming string) string {
	if strings.TrimSpace(incoming) == "" {
		return existing
	}
	return strings.TrimSpace(incoming)
}

func messageExpired(receivedAt, before time.Time) bool {
	if !knownReceivedAt(receivedAt) {
		return false
	}
	return receivedAt.Before(before)
}

func appleIDChanged(current, next string) bool {
	return !strings.EqualFold(strings.TrimSpace(current), strings.TrimSpace(next))
}

// sessionBaseForLogin drops saved login kinds when a successful login changes
// the Apple ID. The incoming kind is merged afterwards. A blank next ID, or a
// case/space-only change, keeps the saved session.
func sessionBaseForLogin(existing protocol.ICloudSession, storedAppleID, nextAppleID string) protocol.ICloudSession {
	if strings.TrimSpace(nextAppleID) == "" || !appleIDChanged(storedAppleID, nextAppleID) {
		return existing
	}
	return protocol.ICloudSession{}
}

func pendingDeleteStale(markedAt, now time.Time) bool {
	if now.IsZero() {
		return false
	}
	if markedAt.IsZero() || !knownReceivedAt(markedAt) {
		return true
	}
	return markedAt.Before(now.Add(-stalePendingDeleteAfter))
}
