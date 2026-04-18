// Package jsonrpc2 implements JSON-RPC 2.0 (https://www.jsonrpc.org/specification).
//
// To use, create a [Stream] over an [io.ReadWriteCloser], implement a [Handler] to process
// incoming requests, and pass both to [NewConn]. The returned [Conn] is safe for concurrent
// use: call [Conn.Call] to send requests, [Conn.Notify] to send notifications, and
// [Conn.Done] to observe shutdown.
//
// The [Mux] type provides per-method dispatch for both request handlers and notification handlers.
//
// Batch requests (JSON arrays of request objects) are supported. Each element in the batch
// is dispatched to the handler concurrently, and all responses are collected and sent as a
// single JSON array. Notifications within a batch are processed but not included in the
// response array. An empty batch array is rejected with [InvalidRequest].
package jsonrpc2
