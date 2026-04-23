package jsonrpc2_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"

	"github.com/mcriley821/jsonrpc2"
)

type addParams struct {
	A int `json:"a"`
	B int `json:"b"`
}

type addResult struct {
	Sum int `json:"sum"`
}

type divParams struct {
	A int `json:"a"`
	B int `json:"b"`
}

type divResult struct {
	Quotient int `json:"quotient"`
}

type greetParams struct {
	Name string `json:"name"`
}

type greetResult struct {
	Greeting string `json:"greeting"`
}

type logParams struct {
	Message string `json:"message"`
}

type subscribeParams struct {
	Topic string `json:"topic"`
}

type subscribeResult struct {
	OK bool `json:"ok"`
}

type eventParams struct {
	Type string `json:"type"`
}

// ExampleNewMux shows the full server-client flow: register a typed handler,
// wire up connections over an in-process pipe, and call the method.
func ExampleNewMux() {
	ctx := context.Background()

	mux := jsonrpc2.NewMux()
	jsonrpc2.Handle(mux, "add",
		func(_ context.Context, p jsonrpc2.Nullable[addParams], _ jsonrpc2.Conn) (addResult, error) {
			return addResult{Sum: p.Value.A + p.Value.B}, nil
		},
	)

	sc, cc := net.Pipe()
	server := jsonrpc2.NewConn(ctx, jsonrpc2.NewStream(sc), jsonrpc2.WithHandler(mux))
	client := jsonrpc2.NewConn(ctx, jsonrpc2.NewStream(cc))
	defer func() {
		_ = client.Close(ctx)
		_ = server.Close(ctx)
	}()

	resp, err := client.Call(ctx, "add", addParams{A: 1, B: 2})
	if err != nil {
		log.Fatal(err)
	}
	if resp.Failed() {
		log.Fatalf("rpc error: %s", resp.Error().Message())
	}

	var result addResult
	if err := resp.Result(&result); err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Sum)

	// Output:
	// 3
}

// ExampleHandle_rpcError shows how returning NewError from a handler sends a
// JSON-RPC error response to the caller.
func ExampleHandle_rpcError() {
	ctx := context.Background()

	mux := jsonrpc2.NewMux()
	jsonrpc2.Handle(mux, "divide",
		func(_ context.Context, p jsonrpc2.Nullable[divParams], _ jsonrpc2.Conn) (divResult, error) {
			if p.Value.B == 0 {
				return divResult{}, jsonrpc2.NewError(jsonrpc2.InvalidParams, "division by zero", nil)
			}
			return divResult{Quotient: p.Value.A / p.Value.B}, nil
		},
	)

	sc, cc := net.Pipe()
	server := jsonrpc2.NewConn(ctx, jsonrpc2.NewStream(sc), jsonrpc2.WithHandler(mux))
	client := jsonrpc2.NewConn(ctx, jsonrpc2.NewStream(cc))
	defer func() {
		_ = client.Close(ctx)
		_ = server.Close(ctx)
	}()

	resp, err := client.Call(ctx, "divide", divParams{A: 10, B: 0})
	if err != nil {
		log.Fatal(err)
	}
	if resp.Failed() {
		fmt.Printf("error %d: %s\n", resp.Error().Code(), resp.Error().Message())
	}

	// Output:
	// error -32602: division by zero
}

// ExampleHandle_nullable shows how to distinguish absent params (Valid == false)
// from present ones (Valid == true).
func ExampleHandle_nullable() {
	mux := jsonrpc2.NewMux()
	jsonrpc2.Handle(mux, "greet",
		func(_ context.Context, p jsonrpc2.Nullable[greetParams], _ jsonrpc2.Conn) (greetResult, error) {
			if !p.Valid {
				return greetResult{Greeting: "hello, world"}, nil
			}
			return greetResult{Greeting: "hello, " + p.Value.Name}, nil
		},
	)
	_ = mux
}

// ExampleHandleNotification shows how to register a handler for fire-and-forget
// notifications; no response is ever sent.
func ExampleHandleNotification() {
	mux := jsonrpc2.NewMux()
	jsonrpc2.HandleNotification(mux, "log",
		func(_ context.Context, p jsonrpc2.Nullable[logParams], _ jsonrpc2.Conn) error {
			slog.Info(p.Value.Message)
			return nil
		},
	)
	_ = mux
}

// ExampleMux_Handle shows how to register a raw handler that works directly
// with the Request and Replier instead of typed params and results.
func ExampleMux_Handle() {
	mux := jsonrpc2.NewMux()
	mux.Handle("ping", jsonrpc2.HandlerFunc(
		func(ctx context.Context, _ jsonrpc2.Request, reply jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			return reply(ctx, "pong")
		},
	))
	_ = mux
}

// ExampleMux_Fallback shows how to replace the default MethodNotFound response
// with a custom catch-all handler.
func ExampleMux_Fallback() {
	mux := jsonrpc2.NewMux()
	mux.Fallback(jsonrpc2.HandlerFunc(
		func(ctx context.Context, req jsonrpc2.Request, reply jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			return reply(ctx, jsonrpc2.NewError(jsonrpc2.MethodNotFound, req.Method(), nil))
		},
	))
	_ = mux
}

// ExampleConn_Done shows how to wait for a connection to finish shutting down
// and inspect the terminal error.
func ExampleConn_Done() {
	ctx := context.Background()
	sc, cc := net.Pipe()
	_ = cc

	conn := jsonrpc2.NewConn(ctx, jsonrpc2.NewStream(sc))

	shutdown := make(chan struct{})
	go func() {
		defer close(shutdown)
		<-conn.Done()
		if err := conn.Err(); err != nil && !errors.Is(err, jsonrpc2.ErrClosed) {
			log.Printf("connection error: %v", err)
		}
	}()

	_ = conn.Close(ctx)
	<-shutdown
}

// ExampleConn_Notify shows how a handler can push a notification back to the
// peer without waiting for a response.
func ExampleConn_Notify() {
	mux := jsonrpc2.NewMux()
	jsonrpc2.Handle(mux, "subscribe",
		func(ctx context.Context, _ jsonrpc2.Nullable[subscribeParams], conn jsonrpc2.Conn) (subscribeResult, error) {
			_ = conn.Notify(ctx, "event", eventParams{Type: "subscribed"})
			return subscribeResult{OK: true}, nil
		},
	)
	_ = mux
}
