package jsonrpc2

import "context"

// Handler processes incoming requests.
type Handler interface {
	// ServeRPC handles a request. Use reply to send the response and conn for
	// outbound calls from within the handler. Returning an error will shutdown
	// the connection.
	ServeRPC(ctx context.Context, req Request, reply Replier, conn Conn) error
}

// Replier sends the response to a request. Pass an [Error] to send an error response;
// any other value sends a success response. It is a no-op for notifications.
type Replier func(ctx context.Context, result any) error

// HandlerFunc is a function that implements [Handler].
type HandlerFunc func(ctx context.Context, req Request, reply Replier, conn Conn) error

// ServeRPC delegates to the wrapped function.
func (f HandlerFunc) ServeRPC(ctx context.Context, req Request, reply Replier, conn Conn) error {
	return f(ctx, req, reply, conn)
}
