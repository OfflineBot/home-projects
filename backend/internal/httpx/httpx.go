// Package httpx carries the error shape the whole API answers with.
//
// Every failure reaches the client as {"error":{"code":…,"message":…}} with a
// message a human can read. Nothing is swallowed, and an empty list is never
// used to paper over a failure.
package httpx

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/gofiber/fiber/v2"
)

type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  any    `json:"detail,omitempty"`
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.cause)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.cause }

// WithCause keeps the underlying error for the log without leaking it to the
// client.
func (e *Error) WithCause(err error) *Error {
	c := *e
	c.cause = err
	return &c
}

func (e *Error) WithDetail(d any) *Error {
	c := *e
	c.Detail = d
	return &c
}

func New(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

func BadRequest(format string, args ...any) *Error {
	return New(fiber.StatusBadRequest, "bad_request", format, args...)
}

func Unauthorized(format string, args ...any) *Error {
	return New(fiber.StatusUnauthorized, "unauthorized", format, args...)
}

func Forbidden(format string, args ...any) *Error {
	return New(fiber.StatusForbidden, "forbidden", format, args...)
}

func NotFound(format string, args ...any) *Error {
	return New(fiber.StatusNotFound, "not_found", format, args...)
}

func Conflict(format string, args ...any) *Error {
	return New(fiber.StatusConflict, "conflict", format, args...)
}

func TooMany(format string, args ...any) *Error {
	return New(fiber.StatusTooManyRequests, "too_many_requests", format, args...)
}

func Internal(format string, args ...any) *Error {
	return New(fiber.StatusInternalServerError, "internal", format, args...)
}

// StepUpRequired tells the UI to ask for the password again before repeating
// the request.
func StepUpRequired(action string) *Error {
	return &Error{
		Status:  fiber.StatusForbidden,
		Code:    "step_up_required",
		Message: "This step needs your password again: " + action,
	}
}

// ReadOnly is the one message every write path uses when something is frozen,
// so UI, API and git say the same thing.
func ReadOnly(what string) *Error {
	return &Error{
		Status:  fiber.StatusForbidden,
		Code:    "read_only",
		Message: what + " is read-only. Turn read-only off in its settings to make changes.",
	}
}

// ErrorHandler is Fiber's central handler; it is the only place that turns an
// error into a response body.
func ErrorHandler(c *fiber.Ctx, err error) error {
	var he *Error
	if errors.As(err, &he) {
		if he.Status >= 500 {
			slog.Error("request failed", "path", c.Path(), "method", c.Method(), "error", he.Error())
		}
		return c.Status(he.Status).JSON(fiber.Map{"error": he})
	}

	var fe *fiber.Error
	if errors.As(err, &fe) {
		return c.Status(fe.Code).JSON(fiber.Map{"error": &Error{
			Code:    "http_error",
			Message: fe.Message,
		}})
	}

	slog.Error("unhandled error", "path", c.Path(), "method", c.Method(), "error", err)
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": &Error{
		Code:    "internal",
		Message: "Something went wrong on the server. The detail is in the server log.",
	}})
}

// OK is the shorthand for a plain success body.
func OK(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"ok": true})
}
