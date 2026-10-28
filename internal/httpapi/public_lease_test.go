package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/biubiubiu125/icloud-mail/internal/domain"
)

func TestEmailLeaseClosedAPIRejectsBeforeMissingLease(t *testing.T) {
	original := loadEmailLease
	t.Cleanup(func() { loadEmailLease = original })
	loadEmailLease = func(_ *Server, _ context.Context, _ string) (domain.Mailbox, bool, domain.Settings, error) {
		return domain.Mailbox{}, false, domain.Settings{PublicAPIEnabled: false, APIKey: "global-secret"}, nil
	}
	server := &Server{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/mailboxes/alias@icloud.com/release", nil)
	request.SetPathValue("email", "alias@icloud.com")
	request.Header.Set("X-API-Key", "global-secret")
	recorder := httptest.NewRecorder()

	server.handleEmailLease(recorder, request)

	if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "public_api_disabled" {
		t.Fatalf("closed api without lease = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestEmailLeaseOpenAPIStillReportsMissingLease(t *testing.T) {
	original := loadEmailLease
	t.Cleanup(func() { loadEmailLease = original })
	loadEmailLease = func(_ *Server, _ context.Context, _ string) (domain.Mailbox, bool, domain.Settings, error) {
		return domain.Mailbox{}, false, domain.Settings{PublicAPIEnabled: true, APIKey: "global-secret"}, nil
	}
	server := &Server{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/mailboxes/alias@icloud.com/release", nil)
	request.SetPathValue("email", "alias@icloud.com")
	request.Header.Set("X-API-Key", "global-secret")
	recorder := httptest.NewRecorder()

	server.handleEmailLease(recorder, request)

	if recorder.Code != http.StatusNotFound || errorCode(t, recorder) != "lease_not_found" {
		t.Fatalf("open api without lease = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestEmailLeaseMailboxKeyClosedAPIRejectsBeforeMissingLease(t *testing.T) {
	original := loadEmailLease
	t.Cleanup(func() { loadEmailLease = original })
	loadEmailLease = func(_ *Server, _ context.Context, _ string) (domain.Mailbox, bool, domain.Settings, error) {
		return domain.Mailbox{APIToken: "box-secret"}, true, domain.Settings{PublicAPIEnabled: false, APIKey: "global-secret"}, nil
	}
	server := &Server{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/mailboxes/alias@icloud.com/release", nil)
	request.SetPathValue("email", "alias@icloud.com")
	request.Header.Set("X-Mailbox-Key", "box-secret")
	recorder := httptest.NewRecorder()

	server.handleEmailLease(recorder, request)

	if recorder.Code != http.StatusForbidden || errorCode(t, recorder) != "public_api_disabled" {
		t.Fatalf("closed api mailbox key without lease = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestEmailLeaseInvalidKeyStaysUnauthorizedWhenAPIClosed(t *testing.T) {
	original := loadEmailLease
	t.Cleanup(func() { loadEmailLease = original })
	loadEmailLease = func(_ *Server, _ context.Context, _ string) (domain.Mailbox, bool, domain.Settings, error) {
		return domain.Mailbox{}, false, domain.Settings{PublicAPIEnabled: false, APIKey: "global-secret"}, nil
	}
	server := &Server{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/mailboxes/alias@icloud.com/release", nil)
	request.SetPathValue("email", "alias@icloud.com")
	request.Header.Set("X-API-Key", "wrong")
	recorder := httptest.NewRecorder()

	server.handleEmailLease(recorder, request)

	if recorder.Code != http.StatusUnauthorized || errorCode(t, recorder) != "api_key_invalid" {
		t.Fatalf("invalid key = %d %s", recorder.Code, recorder.Body.String())
	}
}

func errorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error: %v body=%s", err, recorder.Body.String())
	}
	return payload.Code
}
