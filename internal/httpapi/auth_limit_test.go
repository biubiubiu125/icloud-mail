package httpapi

import (
	"net/http"
	"testing"
	"time"
)

func TestLoginLimitCountsFailuresOnly(t *testing.T) {
	server := &Server{}
	request := &http.Request{RemoteAddr: "203.0.113.8:1234"}
	for i := 0; i < 8; i++ {
		if server.loginBlocked(request) {
			t.Fatalf("blocked before 8 failures at %d", i)
		}
		server.recordLoginFailure(request)
	}
	if !server.loginBlocked(request) {
		t.Fatal("eighth failure did not block the next request")
	}
	other := &http.Request{RemoteAddr: "203.0.113.9:1234"}
	if server.loginBlocked(other) {
		t.Fatal("another host was blocked")
	}
}

func TestLoginSuccessClearsFailures(t *testing.T) {
	server := &Server{}
	request := &http.Request{RemoteAddr: "203.0.113.10:1234"}
	for i := 0; i < 7; i++ {
		server.recordLoginFailure(request)
	}
	server.clearLoginFailures(request)
	server.recordLoginFailure(request)
	if server.loginBlocked(request) {
		t.Fatal("successful login did not clear earlier failures")
	}
}

func TestLoginBucketsForgetIdleHosts(t *testing.T) {
	server := &Server{}
	request := &http.Request{RemoteAddr: "203.0.113.11:1234"}
	server.recordLoginFailure(request)
	bucket := server.loginBucket(request)
	bucket.mu.Lock()
	bucket.hits = []time.Time{time.Now().Add(-11 * time.Minute)}
	bucket.mu.Unlock()
	server.pruneLoginBuckets(time.Now())
	if _, ok := server.limits.Load("login:203.0.113.11"); ok {
		t.Fatal("idle login bucket remained")
	}
}
