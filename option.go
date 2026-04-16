package jsonrpc2

import (
	"log/slog"

	"github.com/google/uuid"
)

// Option configures a [Conn] created by [NewConn].
type Option func(*connOptions)

type connOptions struct {
	idGenerator func() string
	logger      *slog.Logger
}

func defaultConnOptions() connOptions {
	return connOptions{
		idGenerator: uuid.NewString,
	}
}

// WithIDGenerator replaces the default uuid.NewString call-ID generator with fn.
// fn must return a unique string per call to avoid collisions in the in-flight
// request map.
func WithIDGenerator(fn func() string) Option {
	return func(o *connOptions) {
		o.idGenerator = fn
	}
}

// WithLogger sets a structured logger for tracing request/response lifecycle
// events at the Debug level. Pass nil to disable logging (the default).
func WithLogger(l *slog.Logger) Option {
	return func(o *connOptions) {
		o.logger = l
	}
}
