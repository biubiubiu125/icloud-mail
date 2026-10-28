package domain

import (
	"strconv"
	"strings"
)

// ParseIMAPCursor reads a stored IMAP cursor.
// A legacy value is a positive UID. A validity-aware value is uv:<validity>:<uid>.
func ParseIMAPCursor(value string) (validity uint64, uid uint64, ok bool) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "imap:")
	if strings.HasPrefix(value, "uv:") {
		parts := strings.Split(value, ":")
		if len(parts) != 3 {
			return 0, 0, false
		}
		parsedValidity, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil || parsedValidity == 0 {
			return 0, 0, false
		}
		parsedUID, err := strconv.ParseUint(parts[2], 10, 64)
		if err != nil || parsedUID == 0 {
			return 0, 0, false
		}
		return parsedValidity, parsedUID, true
	}
	parsedUID, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsedUID == 0 {
		return 0, 0, false
	}
	return 0, parsedUID, true
}

// FormatIMAPCursor stores a UID, and the UIDVALIDITY when it is known.
func FormatIMAPCursor(validity, uid uint64) string {
	if uid == 0 {
		return ""
	}
	if validity == 0 {
		return strconv.FormatUint(uid, 10)
	}
	return "uv:" + strconv.FormatUint(validity, 10) + ":" + strconv.FormatUint(uid, 10)
}

// LaterUID keeps the newer IMAP cursor.
// A UIDVALIDITY change replaces the old UID space. A non-cursor cannot move a cursor backward.
func LaterUID(current, next string) string {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if next == "" {
		return current
	}
	if current == "" {
		return next
	}
	currentValidity, currentUID, currentOK := ParseIMAPCursor(current)
	nextValidity, nextUID, nextOK := ParseIMAPCursor(next)
	if !nextOK {
		if currentOK {
			return current
		}
		return next
	}
	if !currentOK {
		return FormatIMAPCursor(nextValidity, nextUID)
	}
	if currentValidity != 0 && nextValidity != 0 && currentValidity != nextValidity {
		return FormatIMAPCursor(nextValidity, nextUID)
	}
	validity := nextValidity
	if validity == 0 {
		validity = currentValidity
	}
	if nextUID > currentUID {
		return FormatIMAPCursor(validity, nextUID)
	}
	if validity != 0 && currentValidity == 0 {
		return FormatIMAPCursor(validity, currentUID)
	}
	return current
}
