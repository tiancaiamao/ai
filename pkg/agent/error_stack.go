package agent

import (
	"errors"
	"runtime/debug"
	"strings"
)

type stackTraceCarrier interface {
	StackTrace() string
}

// ErrorWithStack keeps the original error while attaching a stack trace.
type ErrorWithStack struct {
	err   error
	stack string
}

func (e *ErrorWithStack) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *ErrorWithStack) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *ErrorWithStack) StackTrace() string {
	if e == nil {
		return ""
	}
	return e.stack
}

// WithErrorStack wraps err with a stack trace if it does not already have one.
func WithErrorStack(err error) error {
	if err == nil {
		return nil
	}
	var carrier stackTraceCarrier
	if errors.As(err, &carrier) && strings.TrimSpace(carrier.StackTrace()) != "" {
		return err
	}
	return &ErrorWithStack{
		err:   err,
		stack: string(debug.Stack()),
	}
}

// ErrorStack returns the stack trace from an error when available.
func ErrorStack(err error) string {
	if err == nil {
		return ""
	}
	var carrier stackTraceCarrier
	if errors.As(err, &carrier) {
		return strings.TrimSpace(carrier.StackTrace())
	}
	return ""
}
