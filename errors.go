package toolsclient

import "net/http"

// ErrorClass is the closed set of failure categories ds-tools may
// surface. The class drives HTTP status mapping on the wire and
// dispatcher behaviour in ds-mcp (e.g. whether to retry).
type ErrorClass string

const (
	ErrorClassValidation          ErrorClass = "validation"
	ErrorClassUnauthorised        ErrorClass = "unauthorised"
	ErrorClassNotFound            ErrorClass = "not_found"
	ErrorClassTimeout             ErrorClass = "timeout"
	ErrorClassCancelled           ErrorClass = "cancelled"
	ErrorClassInternal            ErrorClass = "internal"
	ErrorClassUpstreamUnavailable ErrorClass = "upstream_unavailable"
)

// Error is the canonical failure payload, both on the wire (JSON body
// of 4xx/5xx HTTP responses and SSE `error` frames) and in Go (it
// satisfies the error interface).
//
// HTTPStatus is populated by the HTTP client when the error is parsed
// from an HTTP response; it is zero when the error originates from an
// SSE `error` frame or from server-side code. It is not serialised.
type Error struct {
	Class      ErrorClass `json:"class"`
	Message    string     `json:"message"`
	HTTPStatus int        `json:"-"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message == "" {
		return string(e.Class)
	}
	return string(e.Class) + ": " + e.Message
}

// Is allows errors.Is matching by ErrorClass. The sentinel values below
// each carry a Class and a zero Message; comparing by Class lets
// callers do `errors.Is(err, toolsclient.ErrTimeout)` regardless of the
// concrete Message.
func (e *Error) Is(target error) bool {
	if e == nil {
		return target == nil
	}
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return e.Class == t.Class
}

// Sentinel errors keyed by ErrorClass. Use with errors.Is.
//
//nolint:errname // these are class sentinels, not stack-attached errors.
var (
	ErrValidation          = &Error{Class: ErrorClassValidation}
	ErrUnauthorised        = &Error{Class: ErrorClassUnauthorised}
	ErrNotFound            = &Error{Class: ErrorClassNotFound}
	ErrTimeout             = &Error{Class: ErrorClassTimeout}
	ErrCancelled           = &Error{Class: ErrorClassCancelled}
	ErrInternal            = &Error{Class: ErrorClassInternal}
	ErrUpstreamUnavailable = &Error{Class: ErrorClassUpstreamUnavailable}
)

// HTTPStatusFor maps an ErrorClass to the canonical HTTP status code.
// Used by ds-tools handlers; ds-mcp can use it in the reverse direction
// via ErrorClassFromHTTPStatus if a server returns an unparseable body.
func HTTPStatusFor(class ErrorClass) int {
	switch class {
	case ErrorClassValidation:
		return http.StatusBadRequest
	case ErrorClassUnauthorised:
		return http.StatusUnauthorized
	case ErrorClassNotFound:
		return http.StatusNotFound
	case ErrorClassTimeout:
		return http.StatusGatewayTimeout
	case ErrorClassCancelled:
		// 499 (nginx convention for client closed request). Echo will
		// emit it verbatim; ALB tolerates non-standard 4xx codes.
		return 499
	case ErrorClassUpstreamUnavailable:
		return http.StatusBadGateway
	case ErrorClassInternal:
		fallthrough
	default:
		return http.StatusInternalServerError
	}
}

// ErrorClassFromHTTPStatus is the inverse of HTTPStatusFor for cases
// where the server returned a recognisable status but an empty or
// unparseable body. Anything unrecognised maps to ErrorClassInternal.
func ErrorClassFromHTTPStatus(status int) ErrorClass {
	switch status {
	case http.StatusBadRequest:
		return ErrorClassValidation
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrorClassUnauthorised
	case http.StatusNotFound:
		return ErrorClassNotFound
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return ErrorClassTimeout
	case 499:
		return ErrorClassCancelled
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return ErrorClassUpstreamUnavailable
	default:
		return ErrorClassInternal
	}
}
