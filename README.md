# jsonrpc2

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)
[![Lint](https://github.com/mcriley821/jsonrpc2/actions/workflows/ci.yml/badge.svg)](https://github.com/mcriley821/jsonrpc2/actions/workflows/ci.yml)
[![codecov](https://codecov.io/github/mcriley821/jsonrpc2/graph/badge.svg?token=TJVCJ0K7HN)](https://codecov.io/github/mcriley821/jsonrpc2)

A Go library implementing [JSON-RPC 2.0](https://www.jsonrpc.org/specification) over any `io.ReadWriteCloser` (TCP, Unix socket, stdin/stdout, etc.).

## Installation

```sh
go get github.com/mcriley821/jsonrpc2
```

## Overview

| Concept   | Description                                                                                            |
|-----------|--------------------------------------------------------------------------------------------------------|
| `Stream`  | Wraps an `io.ReadWriteCloser` with JSON encoding/decoding.                                             |
| `Conn`    | Full-duplex connection. Sends requests (`Call`/`Notify`) and dispatches incoming ones to a `Handler`.  |
| `Handler` | Processes incoming requests. Use `Mux` + `Handle`/`HandleNotification` for typed, per-method dispatch. |
| `Error`   | A JSON-RPC error object. `NewError` creates one; return it from a handler to send an error response.   |

## Comparison

| Feature | This | [x/exp][xexp] | [jrpc2][jrpc2] | [sourcegraph][sg] | [AdamSLevy][al] | [bubunyo][bu] |
|---------|:----:|:-------------:|:--------------:|:-----------------:|:---------------:|:-------------:|
| Transport agnostic | ✅ | ✅ | ✅ | ✅ | ❌ | ❌ |
| Bidirectional async | ✅ | ✅ | ✅ | ✅ | ❌ | ❌ |
| Batch requests | ✅ | ✅ | ✅ | ❌ | ✅ | ✅ |
| `Conn` passed to handler | ✅ | ❌ | ❌ | ✅ | ❌ | ❌ |
| Typed handlers (Go generics) | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Mux / per-method routing | ✅ | ❌ | ✅ | ❌ | ✅ | ✅ |
| Replier callback | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Fallback handler | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| `slog`-compatible logger | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Nullable params | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Notification-only handlers | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Concurrency control | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ |

> This table is a best-effort snapshot and may contain errors or omissions. Library features change over time — consult each project's documentation for the authoritative API.
> Contributions are welcome — open an issue to add a library or correct an entry.

[xexp]: https://pkg.go.dev/golang.org/x/exp/jsonrpc2
[jrpc2]: https://github.com/creachadair/jrpc2
[sg]: https://github.com/sourcegraph/jsonrpc2
[al]: https://github.com/AdamSLevy/jsonrpc2
[bu]: https://github.com/bubunyo/go-rpc

## Examples

See [`example_test.go`](./example_test.go) for runnable examples covering the full server-client flow, typed handlers, nullable params, error responses, raw handler registration, and connection lifecycle.

## AI-Assisted Development

This project is developed with AI assistance — not vibe coded. All generated code is reviewed, understood, and tested before being accepted.
