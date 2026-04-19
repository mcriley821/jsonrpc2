# jsonrpc2

[![Lint](https://github.com/mcriley821/jsonrpc2/actions/workflows/ci.yml/badge.svg)](https://github.com/mcriley821/jsonrpc2/actions/workflows/ci.yml)
[![codecov](https://codecov.io/github/mcriley821/jsonrpc2/graph/badge.svg?token=TJVCJ0K7HN)](https://codecov.io/github/mcriley821/jsonrpc2)

A Go library implementing [JSON-RPC 2.0](https://www.jsonrpc.org/specification) over any `io.ReadWriteCloser` (TCP, Unix socket, stdin/stdout, etc.).

## Installation

```sh
go get github.com/mcriley821/jsonrpc2
```

## Overview

The library is organized around four concepts:

| Concept   | Description                                                                                            |
|-----------|--------------------------------------------------------------------------------------------------------|
| `Stream`  | Wraps an `io.ReadWriteCloser` with JSON encoding/decoding.                                             |
| `Conn`    | Full-duplex connection. Sends requests (`Call`/`Notify`) and dispatches incoming ones to a `Handler`.  |
| `Handler` | Processes incoming requests. Use `Mux` + `Handle`/`HandleNotification` for typed, per-method dispatch. |
| `Error`   | A JSON-RPC error object. `NewError` creates one; return it from a handler to send an error response.   |

## Quick start

### Server

```go
mux := jsonrpc2.NewMux()

// Typed request handler — params unmarshaled, result marshaled automatically.
jsonrpc2.Handle(mux, "add", func(ctx context.Context, p jsonrpc2.Nullable[AddParams], conn jsonrpc2.Conn) (AddResult, error) {
    return AddResult{Sum: p.Value.A + p.Value.B}, nil
})

// Typed notification handler — no response is sent.
jsonrpc2.HandleNotification(mux, "log", func(ctx context.Context, p jsonrpc2.Nullable[LogParams], conn jsonrpc2.Conn) error {
    slog.Info(p.Value.Message)
    return nil
})

ln, _ := net.Listen("tcp", ":8080")
for {
    nc, _ := ln.Accept()
    stream := jsonrpc2.NewStream(nc)
    conn := jsonrpc2.NewConn(ctx, stream, mux)
    go func() { <-conn.Done() }() // wait for shutdown
}
```

### Client

```go
nc, _ := net.Dial("tcp", "localhost:8080")
stream := jsonrpc2.NewStream(nc)
conn := jsonrpc2.NewConn(ctx, stream, jsonrpc2.HandlerFunc(
    func(_ context.Context, _ jsonrpc2.Request, _ jsonrpc2.Replier, _ jsonrpc2.Conn) error {
        return nil // no incoming requests expected from server
    },
))
defer conn.Close(ctx)

resp, err := conn.Call(ctx, "add", AddParams{A: 1, B: 2})
if err != nil {
    log.Fatal(err)
}

if resp.Failed() {
    log.Fatalf("rpc error %d: %s", resp.Error().Code(), resp.Error().Message())
}

var result AddResult
if err := resp.Result(&result); err != nil {
    log.Fatal(err)
}
fmt.Println(result.Sum) // 3
```

## Typed handler registration

`Handle` and `HandleNotification` are package-level generic functions that wrap a strongly-typed function into a `Handler` and register it on a `Mux`.

```go
func Handle[P, R any](m *Mux, method string, f func(ctx context.Context, params Nullable[P], conn Conn) (R, error))
func HandleNotification[P any](m *Mux, method string, f func(ctx context.Context, params Nullable[P], conn Conn) error)
```

### Params decoding

- When params are present, `req.Params()` is decoded into `P` via `json.Unmarshal` and the handler receives `Nullable[P]{Value: p, Valid: true}`.
- When params are absent (omitted entirely), the handler receives `Nullable[P]{Valid: false}` — no unmarshal is attempted.
- Malformed params produce an `InvalidParams` error response (for requests) or are silently discarded (for notifications, per §6 of the spec).

```go
// Nullable params — check Valid to distinguish absent params from present ones.
jsonrpc2.Handle(mux, "optional", func(ctx context.Context, p jsonrpc2.Nullable[MyParams], conn jsonrpc2.Conn) (MyResult, error) {
    if !p.Valid {
        return defaultResult(), nil
    }
    return process(p.Value), nil
})
```

### Error handling

Return a `*rpcError` (created with `NewError`) to send a JSON-RPC error response. Any other non-nil error closes the connection.

```go
jsonrpc2.Handle(mux, "divide", func(ctx context.Context, p jsonrpc2.Nullable[DivParams], conn jsonrpc2.Conn) (DivResult, error) {
    if p.Value.B == 0 {
        return DivResult{}, jsonrpc2.NewError(jsonrpc2.InvalidParams, "division by zero", nil)
    }
    return DivResult{Quotient: p.Value.A / p.Value.B}, nil
})
```

For notification handlers (`HandleNotification`), `Error` returns are silently discarded; only non-RPC errors propagate.

## Mux

`Mux` implements `Handler` and routes incoming requests by method name.

```go
mux := jsonrpc2.NewMux()

// Register a raw handler.
mux.Handle("raw", jsonrpc2.HandlerFunc(func(ctx context.Context, req jsonrpc2.Request, reply jsonrpc2.Replier, conn jsonrpc2.Conn) error {
    return reply(ctx, "ok")
}))

// Set a catch-all fallback (default: MethodNotFound).
mux.Fallback(jsonrpc2.HandlerFunc(func(ctx context.Context, req jsonrpc2.Request, reply jsonrpc2.Replier, conn jsonrpc2.Conn) error {
    return reply(ctx, jsonrpc2.NewError(jsonrpc2.MethodNotFound, req.Method(), nil))
}))
```

Registering the same method name twice panics.

## Error codes

Standard JSON-RPC 2.0 error codes are provided as constants:

| Constant         | Code   | Description                |
|------------------|--------|----------------------------|
| `ParseError`     | -32700 | Invalid JSON received      |
| `InvalidRequest` | -32600 | Not a valid Request object |
| `MethodNotFound` | -32601 | Method does not exist      |
| `InvalidParams`  | -32602 | Invalid method parameters  |
| `InternalError`  | -32603 | Internal server error      |

Application-defined codes should be outside the range -32768 to -32000 (reserved by the spec).

## Connection lifecycle

`NewConn` starts background goroutines for reading, writing, and error coordination. The connection shuts down when:

- `conn.Close(ctx)` is called explicitly.
- The underlying stream fails (read or write error).
- The parent context is cancelled.

`conn.Done()` returns a channel that is closed when the connection has fully shut down and all background goroutines have exited. It is safe for multiple goroutines to wait on it. `conn.Err()` returns the terminal error after `Done` closes.

```go
conn := jsonrpc2.NewConn(ctx, stream, mux)

go func() {
    <-conn.Done()
    if err := conn.Err(); err != nil && !errors.Is(err, jsonrpc2.ErrClosed) {
        log.Printf("connection error: %v", err)
    }
}()
```

## Sending outbound requests from a handler

The `conn Conn` parameter passed to every handler can be used to make outbound calls or send notifications back to the peer from within the handler:

```go
jsonrpc2.Handle(mux, "subscribe", func(ctx context.Context, p jsonrpc2.Nullable[SubParams], conn jsonrpc2.Conn) (SubResult, error) {
    // Send a notification to the peer without waiting for a response.
    _ = conn.Notify(ctx, "event", EventParams{Type: "subscribed"})
    return SubResult{OK: true}, nil
})
```
