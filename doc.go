// Package jsonrpc2 implements JSON-RPC 2.0 (https://www.jsonrpc.org/specification).
//
// To use, create a [Stream] over an [io.ReadWriteCloser] and pass it to [NewConn].
// The returned [Conn] is safe for concurrent use: call [Conn.Call] to send requests,
// [Conn.Notify] to send notifications, and [Conn.Done] to observe shutdown.
// Use [WithHandler] to register a [Handler] for incoming requests; without it,
// requests receive a [MethodNotFound] error response and notifications are silently ignored.
//
// The [Mux] type provides per-method dispatch for both request handlers and notification handlers.
package jsonrpc2
