# jsonrpc2

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

## Examples

See [`example_test.go`](./example_test.go) for runnable examples covering the full server-client flow, typed handlers, nullable params, error responses, raw handler registration, and connection lifecycle.

## AI-Assisted Development

This project is developed with AI assistance — not vibe coded. All generated code is reviewed, understood, and tested before being accepted.
