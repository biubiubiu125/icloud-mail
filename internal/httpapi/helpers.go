package httpapi

import (
	"strings"

	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

func sValidate(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.Contains(raw, "://") {
		return errBad
	}
	if _, err := protocol.CheckProxyURL(raw); err != nil {
		return errBad
	}
	return nil
}

type badProxy struct{}

func (badProxy) Error() string { return "bad proxy" }

var errBad = badProxy{}
