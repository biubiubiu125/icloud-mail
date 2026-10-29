package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/mailbox"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
	"github.com/biubiubiu125/icloud-mail/internal/store"
)

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	items, err := s.DB.ListAccounts(r.Context())
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	listed := make([]accountListItem, 0, len(items))
	for _, item := range items {
		listed = append(listed, accountListItemFrom(item))
	}
	writeOK(w, map[string]any{"success": true, "accounts": listed})
}

func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	item, ok, err := s.DB.GetAccount(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	if !ok {
		writeError(w, s.Logger, apperr.New(404, "account_not_found", "账号不存在", false))
		return
	}
	detail, err := accountDetailFrom(item)
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true, "account": detail})
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Label    string `json:"label"`
		AppleID  string `json:"apple_id"`
		Password string `json:"password"`
		ProxyURL string `json:"proxy_url"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	id, err := protocol.RandomID()
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	now := time.Now()
	item := domain.Account{
		ID:            id,
		Label:         strings.TrimSpace(payload.Label),
		AppleID:       strings.TrimSpace(payload.AppleID),
		ApplePassword: payload.Password,
		ProxyURL:      strings.TrimSpace(payload.ProxyURL),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if item.ProxyURL != "" {
		if err := validateProxyForCreate(item.ProxyURL); err != nil {
			writeError(w, s.Logger, err)
			return
		}
	}
	if err := s.DB.CreateAccount(r.Context(), item); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	item.ApplePassword = ""
	item.HasPassword = payload.Password != ""
	writeOK(w, map[string]any{"success": true, "account": item})
}

func validateProxyForCreate(proxyURL string) error {
	if err := sValidate(proxyURL); err != nil {
		return apperr.New(400, "proxy_invalid", "代理地址无效，只支持 http、https、socks5 和 socks5h", false)
	}
	return nil
}

func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Label          string `json:"label"`
		AppleID        string `json:"apple_id"`
		Password       string `json:"password"`
		UpdatePassword bool   `json:"update_password"`
		UpdateProxy    bool   `json:"update_proxy"`
		ProxyScheme    string `json:"proxy_scheme"`
		ProxyHost      string `json:"proxy_host"`
		ProxyPort      string `json:"proxy_port"`
		ProxyUsername  string `json:"proxy_username"`
		ProxyPassword  string `json:"proxy_password"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	plan, err := accountPatchPlan(accountPatch{
		Label: payload.Label, AppleID: payload.AppleID, Password: payload.Password,
		UpdatePassword: payload.UpdatePassword, UpdateProxy: payload.UpdateProxy,
		ProxyScheme: payload.ProxyScheme, ProxyHost: payload.ProxyHost, ProxyPort: payload.ProxyPort,
		ProxyUsername: payload.ProxyUsername, ProxyPassword: payload.ProxyPassword,
	})
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	current, ok, err := s.DB.GetAccount(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	if !ok {
		writeError(w, s.Logger, apperr.New(404, "account_not_found", "账号不存在", false))
		return
	}
	current.Label = plan.Label
	current.AppleID = plan.AppleID
	if plan.WritePassword {
		current.ApplePassword = plan.Password
	}
	current.UpdatedAt = time.Now()
	if err := s.DB.SaveAccountPatch(r.Context(), current, plan.WritePassword, plan.WriteProxy, plan.ProxyURL); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true})
}

func (s *Server) handleUpdateProxy(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		ProxyURL string `json:"proxy_url"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	if err := s.Mailbox.UpdateProxy(r.Context(), r.PathValue("id"), payload.ProxyURL); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true})
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.Mailbox.DeleteAccount(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true})
}

func (s *Server) handleWebLoginStart(w http.ResponseWriter, r *http.Request) {
	s.handleLoginStart(w, r, true)
}

func (s *Server) handleAppleLoginStart(w http.ResponseWriter, r *http.Request) {
	s.handleLoginStart(w, r, false)
}

func (s *Server) handleLoginStart(w http.ResponseWriter, r *http.Request, web bool) {
	var payload struct {
		AppleID  string `json:"apple_id"`
		Password string `json:"password"`
		Method   string `json:"two_factor_method"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	var result any
	var err error
	if web {
		result, err = s.Mailbox.StartWebLogin(r.Context(), r.PathValue("id"), payload.AppleID, payload.Password, payload.Method)
	} else {
		result, err = s.Mailbox.StartAppleLogin(r.Context(), r.PathValue("id"), payload.AppleID, payload.Password, payload.Method)
	}
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true, "login": result})
}

func (s *Server) handleWebLoginFinish(w http.ResponseWriter, r *http.Request) {
	s.handleLoginFinish(w, r, true)
}

func (s *Server) handleAppleLoginFinish(w http.ResponseWriter, r *http.Request) {
	s.handleLoginFinish(w, r, false)
}

func (s *Server) handleLoginFinish(w http.ResponseWriter, r *http.Request, web bool) {
	var payload struct {
		PendingID string `json:"pending_id"`
		Code      string `json:"code"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	var result any
	var err error
	if web {
		result, err = s.Mailbox.FinishWebLogin(r.Context(), r.PathValue("id"), payload.PendingID, payload.Code)
	} else {
		result, err = s.Mailbox.FinishAppleLogin(r.Context(), r.PathValue("id"), payload.PendingID, payload.Code)
	}
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true, "login": result})
}

func (s *Server) handleSaveIMAP(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Email       string `json:"email"`
		AppPassword string `json:"app_password"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	if err := s.Mailbox.SaveIMAP(r.Context(), r.PathValue("id"), payload.Email, payload.AppPassword); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true})
}

func (s *Server) handleClearReconciliation(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Confirm bool `json:"confirm"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	if err := s.Mailbox.ClearReconciliation(r.Context(), r.PathValue("id"), payload.Confirm); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true})
}

func syncFailureBody(n int, err error) (int, map[string]any) {
	status, code, message := publicError(err)
	body := map[string]any{
		"success": false,
		"code":    code,
		"error":   message,
	}
	if n > 0 {
		body["imported"] = n
	}
	return status, body
}

func (s *Server) handleSyncAccount(w http.ResponseWriter, r *http.Request) {
	n, err := s.Mailbox.SyncAccountMailboxes(r.Context(), r.PathValue("id"))
	if err != nil {
		status, body := syncFailureBody(n, err)
		if status >= 500 && s.Logger != nil {
			s.Logger.Error("request failed", "code", body["code"], "status", status, "error", safeErrorText(err))
		}
		writeJSON(w, status, body)
		return
	}
	writeOK(w, map[string]any{"success": true, "imported": n})
}

func (s *Server) handleListMailboxes(w http.ResponseWriter, r *http.Request) {
	items, err := s.DB.ListMailboxes(r.Context(), r.URL.Query().Get("account_id"), r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true, "mailboxes": items})
}

func (s *Server) handleCreateMailbox(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		AccountID string `json:"account_id"`
		Label     string `json:"label"`
		Note      string `json:"note"`
		Channel   string `json:"channel"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	item, err := s.Mailbox.CreateMailbox(r.Context(), payload.AccountID, payload.Label, payload.Note, payload.Channel)
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true, "mailbox": item, "api_url": s.Mailbox.APIURL(s.baseURL(r), item.Email, item.APIToken)})
}

func (s *Server) handleUpdateMailbox(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Label     string `json:"label"`
		Note      string `json:"note"`
		APIActive *bool  `json:"api_active"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	if payload.APIActive == nil {
		writeError(w, s.Logger, apperr.New(400, "mailbox_update_invalid", "必须同时给出标签、备注和接口开关", false))
		return
	}
	if err := s.DB.UpdateMailboxAdmin(r.Context(), r.PathValue("id"), payload.Label, payload.Note, *payload.APIActive, time.Now()); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true})
}

func (s *Server) handleDeleteMailbox(w http.ResponseWriter, r *http.Request) {
	opts := mailbox.DeleteOptions{Remote: true}
	var payload struct {
		DeleteRemote   *bool `json:"delete_remote"`
		ConfirmUnknown bool  `json:"confirm_unknown"`
		ConfirmFailed  bool  `json:"confirm_failed"`
		ConfirmLocal   bool  `json:"confirm_local"`
	}
	if err := decodeJSONOptions(r, &payload, true, true); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	if payload.DeleteRemote != nil {
		opts.Remote = *payload.DeleteRemote
	}
	opts.ConfirmUnknown = payload.ConfirmUnknown
	opts.ConfirmFailed = payload.ConfirmFailed
	opts.ConfirmLocal = payload.ConfirmLocal
	if r.URL.Query().Get("confirm_unknown") == "1" {
		opts.ConfirmUnknown = true
	}
	if r.URL.Query().Get("delete_remote") == "0" {
		opts.Remote = false
	}
	if err := s.Mailbox.DeleteMailbox(r.Context(), r.PathValue("id"), opts); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true})
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		IDs []string `json:"ids"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	items, previous, err := s.Mailbox.Export(r.Context(), payload.IDs, s.baseURL(r))
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	payloadBytes, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		s.restoreExport(r.Context(), previous)
		writeError(w, s.Logger, err)
		return
	}
	payloadBytes = append(payloadBytes, '\n')
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="mailbox-apis.json"`)
	if _, err := w.Write(payloadBytes); err != nil {
		s.restoreExport(r.Context(), previous)
		if s.Logger != nil {
			s.Logger.Error("export write failed")
		}
	}
}

func (s *Server) restoreExport(parent context.Context, previous map[string]store.ExportStamp) {
	ctx, cancel := mailbox.RestoreContext(parent)
	defer cancel()
	if err := s.DB.RestoreExported(ctx, previous); err != nil && s.Logger != nil {
		s.Logger.Error("export restore failed", "error", err.Error())
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	item, err := s.DB.GetSettings(r.Context())
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, s.settingsPayload(item))
}

func (s *Server) settingsPayload(item domain.Settings) map[string]any {
	return map[string]any{
		"success":                   true,
		"settings":                  item,
		"effective_public_base_url": mailbox.EffectivePublicBaseURL(item.PublicBaseURL, s.Config.PublicBaseURL),
	}
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var item domain.Settings
	if err := decodeJSON(r, &item); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	if err := s.DB.SaveSettings(r.Context(), item); err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	items, err := s.DB.ListEvents(r.Context(), 100)
	if err != nil {
		writeError(w, s.Logger, err)
		return
	}
	writeOK(w, map[string]any{"success": true, "events": items})
}

func (s *Server) baseURL(r *http.Request) string {
	return s.Mailbox.PublicBaseURL(r.Context(), s.Config.PublicBaseURL)
}
