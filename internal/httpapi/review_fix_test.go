package httpapi

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
)

func TestAuthorizeAPIKeepsDatabaseError(t *testing.T) {
	err := authorizeSettings(errors.New("db down"), domain.Settings{APIKey: "secret"}, "secret")
	var appErr *apperr.Error
	if err == nil || errors.As(err, &appErr) && appErr.Code == "api_key_invalid" {
		t.Fatalf("database error became %v", err)
	}
	err = authorizeSettings(nil, domain.Settings{APIKey: "secret"}, "wrong")
	if err == nil || !errors.As(err, &appErr) || appErr.Code != "api_key_invalid" || appErr.HTTP != 401 {
		t.Fatalf("bad key err = %v", err)
	}
}

func TestWriteErrorLogsInternalCause(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	recorder := httptest.NewRecorder()
	writeError(recorder, logger, errors.New("relation mailboxes does not exist"))
	if !strings.Contains(buf.String(), "relation mailboxes does not exist") {
		t.Fatalf("log = %s", buf.String())
	}
}

func TestWriteErrorRedactsDatabaseURL(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	recorder := httptest.NewRecorder()
	writeError(recorder, logger, errors.New("connect postgres://icloud:secret@db/icloud failed"))
	if strings.Contains(buf.String(), "secret") || strings.Contains(buf.String(), "postgres://") {
		t.Fatalf("secret logged: %s", buf.String())
	}
}

func TestSavedProxyFormRejectsPath(t *testing.T) {
	if sValidate("http://127.0.0.1:8080/path") == nil {
		t.Fatal("proxy path was accepted")
	}
	if sValidate("http://127.0.0.1:8080?x=1") == nil {
		t.Fatal("proxy query was accepted")
	}
	if err := sValidate("http://127.0.0.1:8080"); err != nil {
		t.Fatal(err)
	}
}
