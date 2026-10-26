package schema

import (
	"errors"
	"strings"
	"time"
)

type ErrorCode string

const (
	ErrInvalidCapability     ErrorCode = "invalid_capability"
	ErrInvalidInput          ErrorCode = "invalid_input"
	ErrInvalidModelInput     ErrorCode = "invalid_model_input"
	ErrUnsupportedModelInput ErrorCode = "unsupported_model_input"
	ErrModelInputLimit       ErrorCode = "model_input_limit"
	ErrUnknownOffering       ErrorCode = "unknown_offering"
	ErrNoOffering            ErrorCode = "no_offering"
	ErrIdempotencyConflict   ErrorCode = "idempotency_conflict"
	ErrNotFound              ErrorCode = "not_found"
	ErrForbidden             ErrorCode = "forbidden"
	ErrConflict              ErrorCode = "conflict"
	ErrTooManyRequests       ErrorCode = "too_many_requests"
	ErrUnavailable           ErrorCode = "unavailable"
	ErrInternal              ErrorCode = "internal"
)

type Error struct {
	Code      ErrorCode
	Operation string
	Detail    string
	// UserFacing marks Detail as complete text written for the person who
	// sent the request, rendered from a Product Schema message template with
	// only that person's own input facts (sizes, dimensions, Schema labels
	// and limits) filled in. Transports may return such a Detail as the
	// public reason of a 4xx answer (#288). Detail is otherwise diagnostic:
	// it can name internal parameters, IDs or causes and stays in logs.
	UserFacing bool
	// DetailTemplate is the Schema text a UserFacing Detail was filled from
	// and DetailValues its replacer pairs ("{name}", value), so a transport
	// can write the reason in the requester's language (#329 note 99647);
	// Detail keeps the Schema's own (default-language) text. Empty when the
	// Detail was not filled from a template.
	DetailTemplate string
	DetailValues   []string
	RetryAfter     time.Duration
	Cause          error
}

// UserFacingDetail returns the Detail of the first error in err's chain that
// is marked UserFacing, or "" when none is. Wrappers that are not marked are
// skipped; the caller decides whether the outer error's class allows a
// requester-visible reason at all.
func UserFacingDetail(err error) string {
	if source := UserFacingError(err); source != nil {
		return source.Detail
	}
	return ""
}

// UserFacingError is the first error in err's chain whose Detail is marked
// UserFacing, or nil (see UserFacingDetail).
func UserFacingError(err error) *Error {
	for err != nil {
		var domainErr *Error
		if !errors.As(err, &domainErr) {
			return nil
		}
		if domainErr.UserFacing && strings.TrimSpace(domainErr.Detail) != "" {
			return domainErr
		}
		err = domainErr.Cause
	}
	return nil
}

// LocalizedDetail is the UserFacing Detail written again with text mapping
// each Schema text (the template and each value) to the reader's language;
// values that are not Schema texts, such as numbers, map to themselves. It is
// Detail when the error keeps no template or text is nil.
func (e *Error) LocalizedDetail(text func(string) string) string {
	if e == nil {
		return ""
	}
	if e.DetailTemplate == "" || text == nil || len(e.DetailValues)%2 != 0 {
		return e.Detail
	}
	pairs := make([]string, len(e.DetailValues))
	for index, value := range e.DetailValues {
		if index%2 == 1 {
			value = text(value)
		}
		pairs[index] = value
	}
	return strings.NewReplacer(pairs...).Replace(text(e.DetailTemplate))
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail != "" {
		return e.Detail
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error { return e.Cause }

func IsCode(err error, code ErrorCode) bool {
	var domainErr *Error
	return errors.As(err, &domainErr) && domainErr.Code == code
}
