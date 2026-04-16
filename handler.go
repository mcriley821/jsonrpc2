// Package jsonrpc2 implements JSON-RPC 2.0 (https://www.jsonrpc.org/specification)
// over any io.ReadWriteCloser.
//
// Create a connection with NewConn, passing a Stream (created with NewStream)
// and a Handler to process incoming requests. Use Mux with Handle and HandleNotification
// for typed, per-method dispatch.
package jsonrpc2

import "context"

// Handler processes an incoming JSON-RPC request.
// Returning a non-nil error from ServeRPC closes the connection and surfaces
// the error on Conn.Done().
type Handler interface {
	// ServeRPC handles an incoming JSON-RPC request. Use reply to send the
	// response and conn to make outbound calls or notifications from within
	// the handler. Returning a non-nil error closes the connection and
	// surfaces the error on Conn.Done().
	ServeRPC(ctx context.Context, req Request, reply Replier, conn Conn) error
}

// Replier sends the response for a handled request. Pass an Error as result
// to send a JSON-RPC error response; any other value is marshaled as the
// success result. Returns an error if marshaling fails or ctx is cancelled.
//
// Note: this is a no-op when called for a notification (req.ID() == nil).
type Replier func(ctx context.Context, result any) error

// HandlerFunc is a function that implements Handler.
type HandlerFunc func(ctx context.Context, req Request, reply Replier, conn Conn) error

// ServeRPC calls f(ctx, req, reply, conn).
func (f HandlerFunc) ServeRPC(ctx context.Context, req Request, reply Replier, conn Conn) error {
	return f(ctx, req, reply, conn)
}
