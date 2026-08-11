package quackridge

import (
	"errors"
	"fmt"
)

// ErrorCode is a stable machine-readable failure category.
type ErrorCode string

const (
	CodeAuthentication    ErrorCode = "QR_AUTHENTICATION"
	CodeProtocolMismatch  ErrorCode = "QR_PROTOCOL_MISMATCH"
	CodeSourceUnavailable ErrorCode = "QR_SOURCE_UNAVAILABLE"
	CodeRejectedStatement ErrorCode = "QR_REJECTED_STATEMENT"
	CodeCancelled         ErrorCode = "QR_CANCELLED"
	CodeTimeout           ErrorCode = "QR_TIMEOUT"
	CodeResourceExhausted ErrorCode = "QR_RESOURCE_EXHAUSTED"
	CodeInternal          ErrorCode = "QR_INTERNAL"
)

// Error carries a stable code while keeping internal details out of its public
// message. Cause is available locally through errors.Unwrap.
type Error struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

func IsCode(err error, code ErrorCode) bool {
	var target *Error
	return errors.As(err, &target) && target.Code == code
}
