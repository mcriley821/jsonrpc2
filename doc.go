// Package jsonrpc2 implements JSON-RPC 2.0 (https://www.jsonrpc.org/specification).
//
// To use, create a [Stream] over an [io.ReadWriteCloser], implement a [Handler] to process
// incoming requests, and pass both to [NewConn]. The returned [Conn] is safe for concurrent
// use: call [Conn.Call] to send requests, [Conn.Notify] to send notifications,
// [Conn.Batch] to send a batch, and [Conn.Done] to observe shutdown.
//
// Batch requests are supported in both directions: [Conn.Batch] sends an array
// of requests and/or notifications as a single message, and the [Handler] will
// be invoked once per request received in an incoming batch — the library
// gathers the responses and emits them as a single batch reply.
//
// The [Mux] type provides per-method dispatch for both request handlers and notification handlers.
package jsonrpc2
