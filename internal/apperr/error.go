package apperr

import "fmt"

// Error is a stable, caller-safe failure. Message must not contain mailbox bodies, passwords, or codes.
type Error struct {
	Code      string
	Message   string
	HTTP      int
	Retryable bool
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func New(httpStatus int, code, message string, retryable bool) *Error {
	return &Error{HTTP: httpStatus, Code: code, Message: message, Retryable: retryable}
}

func Wrap(httpStatus int, code, message string, err error) *Error {
	if err == nil {
		return New(httpStatus, code, message, false)
	}
	return New(httpStatus, code, fmt.Sprintf("%s：%s", message, err.Error()), false)
}
