package protocol

import (
	"errors"
	"time"
)

type codedError struct {
	code      string
	message   string
	retryable bool
}

func (e codedError) Error() string { return e.message }

func (e codedError) Code() string { return e.code }

func (e codedError) Retryable() bool { return e.retryable }

func errCode(code, message string, retryable bool) error {
	return codedError{code: code, message: message, retryable: retryable}
}

// NewError builds an error the HTTP layer can classify without importing unexported types.
func NewError(code, message string, retryable bool) error {
	return errCode(code, message, retryable)
}

// IsCode reports whether err carries the given stable code.
func IsCode(err error, code string) bool {
	return isCodedError(err, code)
}

// CodeOf returns the stable error code, or an empty string.
func CodeOf(err error) string {
	var coded codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ""
}

// PublicMessage returns a caller-safe message. It never includes a mailbox address.
func PublicMessage(err error) string {
	if err == nil {
		return ""
	}
	var coded codedError
	if errors.As(err, &coded) && coded.message != "" {
		return coded.message
	}
	return "操作失败"
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
