package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
)

type coded interface {
	error
	Code() string
	HTTPStatus() int
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOK(w http.ResponseWriter, value any) {
	writeJSON(w, http.StatusOK, value)
}

func writeError(w http.ResponseWriter, logger *slog.Logger, err error) {
	status, code, message := publicError(err)
	if status >= 500 && logger != nil {
		logger.Error("request failed", "code", code, "status", status, "error", safeErrorText(err))
	}
	writeJSON(w, status, map[string]any{
		"success": false,
		"code":    code,
		"error":   message,
	})
}

func safeErrorText(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	lower := strings.ToLower(text)
	if strings.Contains(lower, "password=") || strings.Contains(lower, "postgres://") || strings.Contains(lower, "postgresql://") {
		return "database error"
	}
	if len(text) > 500 {
		return text[:500]
	}
	return text
}

func publicError(err error) (int, string, string) {
	if err == nil {
		return http.StatusOK, "", ""
	}
	var appErr *apperr.Error
	if errors.As(err, &appErr) {
		status := appErr.HTTP
		if status == 0 {
			status = http.StatusBadRequest
		}
		return status, appErr.Code, appErr.Message
	}
	if code := protocol.CodeOf(err); code != "" {
		return http.StatusBadGateway, code, protocol.PublicMessage(err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, "timeout", "操作超时"
	}
	if errors.Is(err, context.Canceled) {
		return http.StatusRequestTimeout, "canceled", "操作已取消"
	}
	return http.StatusInternalServerError, "internal_error", "操作失败"
}

func decodeJSON(r *http.Request, dest any) error {
	return decodeJSONOptions(r, dest, true, false)
}

func decodeJSONLoose(r *http.Request, dest any) error {
	return decodeJSONOptions(r, dest, false, true)
}

func decodeJSONOptions(r *http.Request, dest any, strict, allowEmpty bool) error {
	if r.Body == nil {
		if allowEmpty {
			return nil
		}
		return apperr.New(http.StatusBadRequest, "invalid_json", "请求格式不正确", false)
	}
	decoder := json.NewDecoder(r.Body)
	if strict {
		decoder.DisallowUnknownFields()
	}
	err := decoder.Decode(dest)
	if allowEmpty && errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return apperr.New(http.StatusBadRequest, "invalid_json", "请求格式不正确", false)
	}
	return nil
}
