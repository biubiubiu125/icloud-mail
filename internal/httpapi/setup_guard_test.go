package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetupRejectsRemoteAddress(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"username":"admin","password":"password1"}`))
	request.RemoteAddr = "203.0.113.8:1234"
	recorder := httptest.NewRecorder()
	server.handleSetup(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "setup_local_only") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestSetupAllowsLoopbackAddress(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:1234", "[::1]:1234"} {
		request := httptest.NewRequest(http.MethodPost, "/api/auth/setup", nil)
		request.RemoteAddr = addr
		if !setupAllowed(request) {
			t.Fatalf("%s was rejected", addr)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/setup", nil)
	request.RemoteAddr = "203.0.113.8:1234"
	if setupAllowed(request) {
		t.Fatal("remote address was allowed")
	}
}
