package domain

import (
	"crypto/subtle"
	"strings"
	"time"
)

// TokenMatches compares a presented mailbox key with the stored key.
// An empty value never matches. The compare is the same constant-time
// algorithm used for the global API key.
func TokenMatches(given, stored string) bool {
	if given == "" || stored == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(given), []byte(stored)) == 1
}

// ProviderOrigin normalizes a stored remote origin.
// Empty historical values become ICLOUD_WEB. Any other unknown value is refused.
func ProviderOrigin(raw string) (string, error) {
	origin := strings.ToUpper(strings.TrimSpace(raw))
	if origin == "" {
		return OriginICloudWeb, nil
	}
	switch origin {
	case OriginICloudWeb, OriginAppleAccount:
		return origin, nil
	default:
		return "", errOrigin
	}
}

type originError struct{}

func (originError) Error() string { return "未知远端来源，已拒绝访问 Apple" }

var errOrigin = originError{}

func IsUnknownOrigin(err error) bool {
	return err == errOrigin
}

func NormalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func LeaseTTL(minutes int) time.Duration {
	if minutes <= 0 {
		minutes = DefaultLeaseTTLMinutes
	}
	if minutes > MaxLeaseTTLMinutes {
		minutes = MaxLeaseTTLMinutes
	}
	return time.Duration(minutes) * time.Minute
}

// ResolveLeaseTTL uses the settings value when the request omits a duration.
func ResolveLeaseTTL(minutes, settingsMinutes int) time.Duration {
	if minutes <= 0 {
		return LeaseTTL(settingsMinutes)
	}
	return LeaseTTL(minutes)
}

func ClaimBlocked(status, deleteStatus string, apiActive, icloudActive bool, activeLeaseID string) bool {
	if status != StatusAvailable || !apiActive || !icloudActive || strings.TrimSpace(activeLeaseID) != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(deleteStatus)) {
	case DeletePending, DeleteUnknown, DeleteFailed, DeleteSucceeded:
		return true
	default:
		return false
	}
}

const (
	ClaimReplayRefuse = iota
	ClaimReplayReturn
	ClaimReplayRefresh // kept for stable numbers; expired holds must not use it
	ClaimReplayRehold
)

// ClassifyClaimReplay decides what a repeated request_id may do.
// A token is returned only while this lease still exclusively owns the mailbox.
func ClassifyClaimReplay(lease Lease, mailbox Mailbox, now time.Time, accountID string) int {
	if strings.TrimSpace(accountID) != "" && mailbox.AccountID != accountID {
		return ClaimReplayRefuse
	}
	if claimStillExclusive(lease, mailbox, now) {
		return ClaimReplayReturn
	}
	// An expired hold is no longer exclusive. Do not extend it here.
	// The expire task returns that mailbox to the pool.
	if !ClaimBlocked(mailbox.Status, mailbox.RemoteDeleteStatus, mailbox.APIActive, mailbox.ICloudActive, mailbox.ActiveLeaseID) {
		return ClaimReplayRehold
	}
	return ClaimReplayRefuse
}

func claimStillExclusive(lease Lease, mailbox Mailbox, now time.Time) bool {
	if mailbox.Status != StatusUsed {
		return false
	}
	switch lease.State {
	case LeaseHeld:
		return lease.ExpiresAt.After(now) && mailbox.ActiveLeaseID == lease.ID
	case LeaseCommitted:
		return mailbox.ActiveLeaseID == lease.ID || mailbox.ActiveLeaseID == ""
	case LeaseReleased:
		return mailbox.ActiveLeaseID == ""
	default:
		return false
	}
}
