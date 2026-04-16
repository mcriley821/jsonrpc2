package jsonrpc2_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mcriley821/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubRequest is a minimal Request implementation for Mux unit tests.
type stubRequest struct {
	id     any
	method string
	params json.RawMessage
}

func (r *stubRequest) ID() any                 { return r.id }
func (r *stubRequest) Method() string          { return r.method }
func (r *stubRequest) Params() json.RawMessage { return r.params }

func noopReplier(_ context.Context, _ any) error { return nil }

func TestNewMux(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()
	require.NotNil(t, mux)
}

func TestMux_Handle_PanicOnDuplicate(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()
	h := jsonrpc2.HandlerFunc(
		func(_ context.Context, _ jsonrpc2.Request, _ jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			return nil
		},
	)

	mux.Handle("test", h)
	assert.Panics(t, func() {
		mux.Handle("test", h)
	})
}

func TestMux_HandleFunc(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()
	called := false

	mux.HandleFunc("test",
		func(_ context.Context, _ jsonrpc2.Request, _ jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			called = true

			return nil
		},
	)

	req := &stubRequest{id: "1", method: "test", params: nil}
	err := mux.ServeRPC(t.Context(), req, noopReplier, nil)
	require.NoError(t, err)
	assert.True(t, called)
}

func TestMux_ServeRPC_Registered(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()
	called := false

	mux.Handle("test", jsonrpc2.HandlerFunc(
		func(_ context.Context, _ jsonrpc2.Request, _ jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			called = true

			return nil
		},
	))

	req := &stubRequest{id: "1", method: "test", params: nil}
	err := mux.ServeRPC(t.Context(), req, noopReplier, nil)
	require.NoError(t, err)
	assert.True(t, called)
}

func TestMux_ServeRPC_MethodNotFound(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()

	var repliedWith any

	reply := jsonrpc2.Replier(func(_ context.Context, result any) error {
		repliedWith = result

		return nil
	})

	req := &stubRequest{id: "1", method: "unknown", params: nil}
	err := mux.ServeRPC(t.Context(), req, reply, nil)
	require.NoError(t, err)

	rpcErr, ok := repliedWith.(jsonrpc2.Error)
	require.True(t, ok, "expected an Error reply for unregistered method")
	assert.Equal(t, jsonrpc2.MethodNotFound, rpcErr.Code())
}

func TestMux_ServeRPC_Fallback(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()
	called := false

	mux.Fallback(jsonrpc2.HandlerFunc(
		func(_ context.Context, _ jsonrpc2.Request, _ jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			called = true

			return nil
		},
	))

	req := &stubRequest{id: "1", method: "unknown", params: nil}
	err := mux.ServeRPC(t.Context(), req, noopReplier, nil)
	require.NoError(t, err)
	assert.True(t, called)
}

func TestMux_Fallback_Replace(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()
	firstCalled := false
	secondCalled := false

	mux.Fallback(jsonrpc2.HandlerFunc(
		func(_ context.Context, _ jsonrpc2.Request, _ jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			firstCalled = true

			return nil
		},
	))

	mux.Fallback(jsonrpc2.HandlerFunc(
		func(_ context.Context, _ jsonrpc2.Request, _ jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			secondCalled = true

			return nil
		},
	))

	req := &stubRequest{id: "1", method: "unknown", params: nil}
	err := mux.ServeRPC(t.Context(), req, noopReplier, nil)
	require.NoError(t, err)

	assert.False(t, firstCalled, "first fallback should have been replaced")
	assert.True(t, secondCalled)
}
