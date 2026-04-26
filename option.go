package jsonrpc2

import "context"

// Option configures a [Conn] created by [NewConn].
type Option func(*connOptions)

// Logger is the interface accepted by [WithLogger].
// [*slog.Logger] satisfies this interface automatically.
type Logger interface {
	Debug(msg string, args ...any)
	DebugContext(ctx context.Context, msg string, args ...any)
}

type connOptions struct {
	handler Handler
	logger  Logger
}

func defaultConnOptions() connOptions {
	return connOptions{
		handler: HandlerFunc(defaultHandler),
	}
}

// defaultHandler replies with [MethodNotFound] for requests; notifications are
// silently ignored because the replier for notifications is a no-op.
func defaultHandler(ctx context.Context, _ Request, reply Replier, _ Conn) error {
	return reply(ctx, NewError(MethodNotFound, "Method not implemented", nil))
}

// WithHandler sets the [Handler] used to dispatch incoming requests.
// When no WithHandler option is provided, any incoming request receives a
// [MethodNotFound] error response and notifications are silently ignored.
func WithHandler(h Handler) Option {
	return func(o *connOptions) {
		o.handler = h
	}
}

// WithLogger sets the [Logger] used to trace the request/response lifecycle.
// Log entries are emitted at Debug level for outgoing requests and
// notifications, incoming requests, outgoing responses, and incoming
// responses. When no WithLogger option is provided, logging is disabled.
// [*slog.Logger] satisfies [Logger] without an adapter.
func WithLogger(l Logger) Option {
	return func(o *connOptions) {
		o.logger = l
	}
}
