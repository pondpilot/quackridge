package quackridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	CodeValidation        ErrorCode = "QR_VALIDATION"
	CodeKeychainDenied    ErrorCode = "QR_KEYCHAIN_DENIED"
	CodeInteractionNeeded ErrorCode = "QR_INTERACTION_REQUIRED"
	CodeUnavailableHost   ErrorCode = "QR_UNAVAILABLE_HOST"
	CodeTLSFailure        ErrorCode = "QR_TLS_FAILURE"
	CodeConflict          ErrorCode = "QR_CONFLICT"
	CodeExtensionMismatch ErrorCode = "QR_EXTENSION_MISMATCH"
	CodeStaleSocket       ErrorCode = "QR_STALE_SOCKET"
	CodeIncompatible      ErrorCode = "QR_INCOMPATIBLE_PROTOCOL"
	CodePairingExpired    ErrorCode = "QR_PAIRING_EXPIRED"
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

// ClassifyError converts driver, context, and transport failures into the
// stable public error contract without returning SQL, credentials, or paths.
func ClassifyError(err error) error {
	if err == nil {
		return nil
	}
	var public *Error
	if errors.As(err, &public) {
		return public
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Code: CodeTimeout, Message: "query timed out", Cause: err}
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Code: CodeCancelled, Message: "query cancelled", Cause: err}
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "out of memory"),
		strings.Contains(lower, "memory limit"),
		strings.Contains(lower, "temp directory"),
		strings.Contains(lower, "temporary directory"),
		strings.Contains(lower, "resource exhausted"):
		return &Error{Code: CodeResourceExhausted, Message: "query resource limit exceeded", Cause: err}
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "authorization"),
		strings.Contains(lower, "not authorized"):
		return &Error{Code: CodeRejectedStatement, Message: "statement rejected by policy", Cause: err}
	case strings.Contains(lower, "connection refused"), strings.Contains(lower, "failed to connect"),
		strings.Contains(lower, "connection closed"), strings.Contains(lower, "server closed the connection"):
		return &Error{Code: CodeSourceUnavailable, Message: "source is unavailable", Cause: err}
	case strings.Contains(lower, "authentication"), strings.Contains(lower, "invalid token"):
		return &Error{Code: CodeAuthentication, Message: "authentication failed", Cause: err}
	default:
		return &Error{Code: CodeInternal, Message: "query failed", Cause: err}
	}
}
