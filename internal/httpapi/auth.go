package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

const sessionCookie = "icloud_mail_session"

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	count, err := s.DB.AdminCount(r.Context())
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	_, ok := s.adminFromRequest(r)
	writeOK(w, map[string]any{"setup_required": count == 0, "authenticated": ok})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !setupAllowed(r) {
		writeError(w, s.Logger, apperr.New(403, "setup_local_only", "首次初始化只允许本机访问，请用环境变量创建管理员", false))
		return
	}
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	username := strings.TrimSpace(payload.Username)
	if len(username) < 3 || len(payload.Password) < 8 {
		writeError(w, s.Logger, apperr.New(400, "admin_invalid", "用户名至少 3 位，密码至少 8 位", false))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	id, err := protocol.RandomID()
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	if err := s.DB.CreateAdmin(r.Context(), domain.Admin{ID: id, Username: username, PasswordHash: string(hash), CreatedAt: time.Now()}); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	if err := s.startSession(w, r, id); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.loginBlocked(r) {
		writeError(w, s.Logger, apperr.New(429, "rate_limited", "尝试次数过多，请稍后再试", true))
		return
	}
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	admin, ok, err := s.DB.GetAdminByUsername(r.Context(), strings.TrimSpace(payload.Username))
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	if !ok || bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(payload.Password)) != nil {
		s.recordLoginFailure(r)
		writeError(w, s.Logger, apperr.New(401, "admin_invalid", "用户名或密码不正确", false))
		return
	}
	s.clearLoginFailures(r)
	if err := s.startSession(w, r, admin.ID); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		_ = s.DB.DeleteAdminSession(r.Context(), hashToken(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.Config.CookieSecure})
	writeOK(w, map[string]any{"success": true})
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, adminID string) error {
	token, err := protocol.RandomToken(32)
	if err != nil {
		return err
	}
	now := time.Now()
	expires := now.Add(time.Duration(s.Config.SessionHours) * time.Hour)
	if err := s.DB.CreateAdminSession(r.Context(), domain.AdminSession{TokenHash: hashToken(token), AdminID: adminID, ExpiresAt: expires, CreatedAt: now}); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.Config.CookieSecure,
	})
	return nil
}

func (s *Server) adminFromRequest(r *http.Request) (domain.AdminSession, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return domain.AdminSession{}, false
	}
	session, ok, err := s.DB.GetAdminSession(r.Context(), hashToken(cookie.Value))
	if err != nil || !ok || time.Now().After(session.ExpiresAt) {
		return domain.AdminSession{}, false
	}
	return session, true
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.adminFromRequest(r); !ok {
			writeError(w, s.Logger, apperr.New(401, "admin_required", "请先登录", false))
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Requested-With") != "icloud-mail" {
			writeError(w, s.Logger, apperr.New(403, "csrf_rejected", "缺少管理请求头", false))
			return
		}
		next(w, r)
	}
}

func (s *Server) authorizeAPI(r *http.Request) error {
	settings, err := s.DB.GetSettings(r.Context())
	return authorizeSettings(err, settings, presentedAPIKey(r))
}

func authorizeSettings(settingsErr error, settings domain.Settings, presented string) error {
	if settingsErr != nil {
		return settingsErr
	}
	if !apiKeyMatches(settings, presented) {
		return apperr.New(401, "api_key_invalid", "接口密钥不正确", false)
	}
	return nil
}

func apiKeyMatches(settings domain.Settings, presented string) bool {
	if strings.TrimSpace(settings.APIKey) == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(settings.APIKey)) == 1
}

func presentedAPIKey(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-API-Key")); value != "" {
		return value
	}
	header := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return strings.TrimSpace(r.URL.Query().Get("api_key"))
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
