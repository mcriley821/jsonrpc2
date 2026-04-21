package jsonrpc2

import "context"

// Option configures a [Conn] created by [NewConn].
type Option func(*connOptions)

type connOptions struct {
	handler Handler
}

func defaultConnOptions() connOptions {
	return connOptions{
		handler: HandlerFunc(defaultHandler),
	}
}

// defaultHandler replies with [MethodNotFound] for requests; notifications are
// silently ignored because the replier for notifications is a no-op.
func defaultHandler(ctx context.Context, _ Request, reply Replier, _ Conn) error {
	return reply(ctx, NewError(MethodNotFound, "Method not found", nil))
}

// WithHandler sets the [Handler] used to dispatch incoming requests.
// When no WithHandler option is provided, any incoming request receives a
// [MethodNotFound] error response and notifications are silently ignored.
func WithHandler(h Handler) Option {
	return func(o *connOptions) {
		o.handler = h
	}
}
