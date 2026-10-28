package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/mailbox"
)

var loadEmailLease = func(s *Server, ctx context.Context, email string) (domain.Mailbox, bool, domain.Settings, error) {
	item, ok, err := s.DB.GetMailboxByEmail(ctx, email)
	if err != nil {
		return domain.Mailbox{}, false, domain.Settings{}, err
	}
	settings, err := s.DB.GetSettings(ctx)
	return item, ok, settings, err
}

func claimTTL(minutes int) time.Duration {
	if minutes <= 0 {
		return 0
	}
	return domain.LeaseTTL(minutes)
}

func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	if err := s.authorizeAPI(r); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	var payload struct {
		Project   string `json:"project"`
		Purpose   string `json:"purpose"`
		RequestID string `json:"request_id"`
		AccountID string `json:"account_id"`
		TTL       int    `json:"ttl_minutes"`
	}
	if err := decodeJSONLoose(r, &payload); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	result, err := s.Mailbox.Claim(r.Context(), domain.ClaimInput{
		Project:   payload.Project,
		Purpose:   payload.Purpose,
		RequestID: payload.RequestID,
		AccountID: payload.AccountID,
		TTL:       claimTTL(payload.TTL),
	})
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	base := s.baseURL(r)
	writeOK(w, map[string]any{
		"success":    true,
		"id":         result.Mailbox.ID,
		"email":      result.Mailbox.Email,
		"api_token":  result.Mailbox.APIToken,
		"api_url":    s.Mailbox.APIURL(base, result.Mailbox.Email, result.Mailbox.APIToken),
		"mailbox":    publicMailbox(result.Mailbox, base),
		"lease":      result.Lease,
		"created":    result.Created,
		"idempotent": result.Idempotent,
	})
}

func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	if err := s.authorizeAPI(r); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	var payload struct {
		Emails []string `json:"emails"`
	}
	if err := decodeJSONLoose(r, &payload); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	found, missing, err := s.Mailbox.Lookup(r.Context(), payload.Emails)
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true, "mailboxes": found, "missing": missing})
}

func (s *Server) handleCode(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	waitMS, _ := strconv.Atoi(query.Get("wait_ms"))
	var after time.Time
	if raw := strings.TrimSpace(query.Get("after")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, s.Logger, apperr.New(400, "invalid_after", "after 必须是 RFC3339 时间", false))
			return
		}
		after = parsed
	}
	result, err := s.Mailbox.Code(r.Context(), r.PathValue("email"), mailboxKey(r), mailbox.CodeOptions{
		Keyword:    query.Get("keyword"),
		After:      after,
		Wait:       time.Duration(waitMS) * time.Millisecond,
		Cache:      query.Get("cache") == "1",
		AllowStale: query.Get("allow_stale") == "1",
	})
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{
		"success":     true,
		"email":       result.Email,
		"code":        result.Code,
		"subject":     result.Subject,
		"from":        result.From,
		"received_at": result.ReceivedAt,
		"source":      result.Source,
		"stale_cache": result.StaleCache,
	})
}

func (s *Server) handleEmailLease(w http.ResponseWriter, r *http.Request) {
	item, ok, settings, err := loadEmailLease(s, r.Context(), r.PathValue("email"))
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	key := ""
	if !apiKeyMatches(settings, presentedAPIKey(r)) {
		if !ok || !domain.TokenMatches(mailboxKey(r), item.APIToken) {
			writeError(w, s.Logger, apperr.New(401, "api_key_invalid", "接口密钥不正确", false))
			return
		}
		key = mailboxKey(r)
	}
	if !settings.PublicAPIEnabled {
		writeError(w, s.Logger, apperr.New(403, "public_api_disabled", "公开接码接口已关闭", false))
		return
	}
	if !ok || item.ActiveLeaseID == "" {
		writeError(w, s.Logger, apperr.New(404, "lease_not_found", "这个邮箱没有活动租约", false))
		return
	}
	s.leaseAction(w, r, item.ActiveLeaseID, key)
}

func (s *Server) handleLeaseAction(w http.ResponseWriter, r *http.Request) {
	if err := s.authorizeAPI(r); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	s.leaseAction(w, r, r.PathValue("id"), "")
}

func (s *Server) leaseAction(w http.ResponseWriter, r *http.Request, id, mailboxKey string) {
	var payload struct {
		TTLMinutes   int  `json:"ttl_minutes"`
		ReturnToPool bool `json:"return_to_pool"`
	}
	if err := decodeJSONLoose(r, &payload); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	var lease domain.Lease
	var err error
	switch {
	case strings.HasSuffix(r.URL.Path, "/commit"):
		lease, err = s.Mailbox.CommitLease(r.Context(), id, mailboxKey)
	case strings.HasSuffix(r.URL.Path, "/release"):
		lease, err = s.Mailbox.ReleaseLease(r.Context(), id, payload.ReturnToPool, mailboxKey)
	case strings.HasSuffix(r.URL.Path, "/renew"):
		lease, err = s.Mailbox.RenewLease(r.Context(), id, payload.TTLMinutes, mailboxKey)
	default:
		err = apperr.New(404, "not_found", "接口不存在", false)
	}
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true, "lease": lease})
}

func mailboxKey(r *http.Request) string {
	if value := strings.TrimSpace(r.URL.Query().Get("key")); value != "" {
		return value
	}
	return strings.TrimSpace(r.Header.Get("X-Mailbox-Key"))
}

func publicMailbox(item domain.Mailbox, base string) map[string]any {
	return map[string]any{
		"id":         item.ID,
		"email":      item.Email,
		"status":     item.Status,
		"api_token":  item.APIToken,
		"api_url":    mailbox.JoinAPIURL(base, item.Email, item.APIToken),
		"account_id": item.AccountID,
		"created_at": item.CreatedAt,
	}
}
