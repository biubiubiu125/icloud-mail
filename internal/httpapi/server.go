package httpapi

import (
	"embed"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/config"
	"github.com/biubiubiu125/icloud-mail/internal/mailbox"
	"github.com/biubiubiu125/icloud-mail/internal/store"
)

//go:embed admin.html
var adminHTML embed.FS

type Server struct {
	Config  config.Config
	DB      *store.Store
	Mailbox *mailbox.Service
	Logger  *slog.Logger
	mux     *http.ServeMux
	limits  sync.Map
}

func New(cfg config.Config, db *store.Store, svc *mailbox.Service, logger *slog.Logger) *Server {
	s := &Server{Config: cfg, DB: db, Mailbox: svc, Logger: logger, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.withSecurity(s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.handleAdmin)
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
	s.mux.HandleFunc("POST /api/auth/setup", s.handleSetup)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/auth/logout", s.requireAdmin(s.handleLogout))

	s.mux.HandleFunc("GET /api/accounts", s.requireAdmin(s.handleListAccounts))
	s.mux.HandleFunc("GET /api/accounts/{id}", s.requireAdmin(s.handleGetAccount))
	s.mux.HandleFunc("POST /api/accounts", s.requireAdmin(s.handleCreateAccount))
	s.mux.HandleFunc("PATCH /api/accounts/{id}", s.requireAdmin(s.handleUpdateAccount))
	s.mux.HandleFunc("PUT /api/accounts/{id}/proxy", s.requireAdmin(s.handleUpdateProxy))
	s.mux.HandleFunc("DELETE /api/accounts/{id}", s.requireAdmin(s.handleDeleteAccount))
	s.mux.HandleFunc("POST /api/accounts/{id}/login/web/start", s.requireAdmin(s.handleWebLoginStart))
	s.mux.HandleFunc("POST /api/accounts/{id}/login/web/finish", s.requireAdmin(s.handleWebLoginFinish))
	s.mux.HandleFunc("POST /api/accounts/{id}/login/apple/start", s.requireAdmin(s.handleAppleLoginStart))
	s.mux.HandleFunc("POST /api/accounts/{id}/login/apple/finish", s.requireAdmin(s.handleAppleLoginFinish))
	s.mux.HandleFunc("POST /api/accounts/{id}/imap", s.requireAdmin(s.handleSaveIMAP))
	s.mux.HandleFunc("POST /api/accounts/{id}/sync", s.requireAdmin(s.handleSyncAccount))
	s.mux.HandleFunc("POST /api/accounts/{id}/reconciliation/clear", s.requireAdmin(s.handleClearReconciliation))

	s.mux.HandleFunc("GET /api/mailboxes", s.requireAdmin(s.handleListMailboxes))
	s.mux.HandleFunc("POST /api/mailboxes", s.requireAdmin(s.handleCreateMailbox))
	s.mux.HandleFunc("PATCH /api/mailboxes/{id}", s.requireAdmin(s.handleUpdateMailbox))
	s.mux.HandleFunc("POST /api/mailboxes/{id}/delete", s.requireAdmin(s.handleDeleteMailbox))
	s.mux.HandleFunc("DELETE /api/mailboxes/{id}", s.requireAdmin(s.handleDeleteMailbox))
	s.mux.HandleFunc("POST /api/mailboxes/export", s.requireAdmin(s.handleExport))
	s.mux.HandleFunc("GET /api/settings", s.requireAdmin(s.handleGetSettings))
	s.mux.HandleFunc("PUT /api/settings", s.requireAdmin(s.handleSaveSettings))
	s.mux.HandleFunc("GET /api/events", s.requireAdmin(s.handleEvents))

	s.mux.HandleFunc("POST /api/v1/mailboxes/claim", s.handleClaim)
	s.mux.HandleFunc("POST /api/v1/mailboxes/lookup", s.handleLookup)
	s.mux.HandleFunc("GET /api/v1/mailboxes/{email}/code", s.handleCode)
	s.mux.HandleFunc("POST /api/v1/mailboxes/{email}/commit", s.handleEmailLease)
	s.mux.HandleFunc("POST /api/v1/mailboxes/{email}/release", s.handleEmailLease)
	s.mux.HandleFunc("POST /api/v1/mailboxes/{email}/renew", s.handleEmailLease)
	s.mux.HandleFunc("POST /api/v1/mailbox-leases/{id}/commit", s.handleLeaseAction)
	s.mux.HandleFunc("POST /api/v1/mailbox-leases/{id}/release", s.handleLeaseAction)
	s.mux.HandleFunc("POST /api/v1/mailbox-leases/{id}/renew", s.handleLeaseAction)
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(adminHTML, "admin.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	_, _ = w.Write(data)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "code": "database_unavailable"})
		return
	}
	writeOK(w, map[string]any{"success": true})
}

func (s *Server) withSecurity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

type limitBucket struct {
	mu   sync.Mutex
	hits []time.Time
}

func (s *Server) loginBlocked(r *http.Request) bool {
	bucket := s.loginBucket(r)
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	bucket.hits = recentHits(bucket.hits, time.Now())
	return len(bucket.hits) >= 8
}

func (s *Server) recordLoginFailure(r *http.Request) {
	bucket := s.loginBucket(r)
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	now := time.Now()
	bucket.hits = append(recentHits(bucket.hits, now), now)
}

func (s *Server) clearLoginFailures(r *http.Request) {
	s.limits.Delete("login:" + loginHost(r))
}

func (s *Server) loginBucket(r *http.Request) *limitBucket {
	now := time.Now()
	s.pruneLoginBuckets(now)
	key := "login:" + loginHost(r)
	value, _ := s.limits.LoadOrStore(key, &limitBucket{})
	s.evictLoginBuckets(key, 256)
	return value.(*limitBucket)
}

func loginHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return host
}

func (s *Server) pruneLoginBuckets(now time.Time) {
	s.limits.Range(func(key, value any) bool {
		name, ok := key.(string)
		if !ok || !strings.HasPrefix(name, "login:") {
			return true
		}
		bucket, ok := value.(*limitBucket)
		if !ok {
			s.limits.Delete(key)
			return true
		}
		bucket.mu.Lock()
		bucket.hits = recentHits(bucket.hits, now)
		empty := len(bucket.hits) == 0
		bucket.mu.Unlock()
		if empty {
			s.limits.Delete(key)
		}
		return true
	})
}

func (s *Server) evictLoginBuckets(keep string, max int) {
	excess := 0
	s.limits.Range(func(key, _ any) bool {
		if name, ok := key.(string); ok && strings.HasPrefix(name, "login:") {
			excess++
		}
		return true
	})
	if excess <= max {
		return
	}
	s.limits.Range(func(key, _ any) bool {
		name, ok := key.(string)
		if !ok || !strings.HasPrefix(name, "login:") || name == keep {
			return true
		}
		s.limits.Delete(key)
		excess--
		return excess > max
	})
}

func setupAllowed(r *http.Request) bool {
	ip := net.ParseIP(loginHost(r))
	return ip != nil && ip.IsLoopback()
}

func recentHits(hits []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-10 * time.Minute)
	kept := hits[:0]
	for _, hit := range hits {
		if hit.After(cutoff) {
			kept = append(kept, hit)
		}
	}
	return kept
}
