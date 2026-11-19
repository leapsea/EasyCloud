// Package errs defines application errors that carry an HTTP status and an
// optional UI action ("where to fix this").
package errs

import (
	"errors"
	"net/http"
)

// Action is rendered by the frontend as a button that navigates to Route and focuses Focus.
type Action struct {
	Key   string `json:"key,omitempty"` // stable id the UI translates; Label is the fallback text
	Label string `json:"label"`
	Route string `json:"route"`
	Focus string `json:"focus,omitempty"`
	Role  string `json:"role,omitempty"`
}

type AppError struct {
	Code   string  `json:"code"`
	Status int     `json:"-"`
	Detail any     `json:"detail,omitempty"`
	Action *Action `json:"action,omitempty"`
	cause  error
}

func (e *AppError) Error() string {
	if e.cause != nil {
		return e.Code + ": " + e.cause.Error()
	}
	return e.Code
}
func (e *AppError) Unwrap() error { return e.cause }

func New(status int, code string) *AppError { return &AppError{Code: code, Status: status} }

func (e *AppError) WithDetail(d any) *AppError    { c := *e; c.Detail = d; return &c }
func (e *AppError) WithAction(a Action) *AppError { c := *e; c.Action = &a; return &c }
func (e *AppError) Wrap(err error) *AppError      { c := *e; c.cause = err; return &c }

var (
	ErrNotFound     = New(http.StatusNotFound, "NOT_FOUND")
	ErrInvalidPath  = New(http.StatusBadRequest, "INVALID_PATH")
	ErrInvalidInput = New(http.StatusBadRequest, "INVALID_INPUT")
	ErrUnauthorized = New(http.StatusUnauthorized, "UNAUTHORIZED")
	ErrConflict     = New(http.StatusConflict, "CONFLICT")
	ErrTooLarge     = New(http.StatusRequestEntityTooLarge, "UPLOAD_TOO_LARGE")
)

// As returns the AppError in err's chain, if any.
func As(err error) (*AppError, bool) {
	var ae *AppError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}
