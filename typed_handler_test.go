package jsonrpc2_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mcriley821/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type handleTestParams struct {
	Value string `json:"value"`
}

type handleTestResult struct {
	Result string `json:"result"`
}

func TestHandle_HappyPath(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()

	jsonrpc2.Handle(mux, "test",
		func(_ context.Context, p jsonrpc2.Nullable[handleTestParams], _ jsonrpc2.Conn) (handleTestResult, error) {
			return handleTestResult{Result: p.Value.Value}, nil
		},
	)

	params, err := json.Marshal(handleTestParams{Value: "hello"})
	require.NoError(t, err)

	var repliedWith any

	reply := jsonrpc2.Replier(func(_ context.Context, result any) error {
		repliedWith = result

		return nil
	})

	req := &stubRequest{id: "1", method: "test", params: json.RawMessage(params)}
	err = mux.ServeRPC(t.Context(), req, reply, nil)
	require.NoError(t, err)
	assert.Equal(t, handleTestResult{Result: "hello"}, repliedWith)
}

func TestHandle_RPCError(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()
	rpcErr := jsonrpc2.NewError(jsonrpc2.InvalidParams, "bad params", nil)

	jsonrpc2.Handle(mux, "test",
		func(_ context.Context, _ jsonrpc2.Nullable[handleTestParams], _ jsonrpc2.Conn) (handleTestResult, error) {
			return handleTestResult{}, rpcErr
		},
	)

	var repliedWith any

	reply := jsonrpc2.Replier(func(_ context.Context, result any) error {
		repliedWith = result

		return nil
	})

	req := &stubRequest{id: "1", method: "test", params: json.RawMessage(`{"value":"test"}`)}
	err := mux.ServeRPC(t.Context(), req, reply, nil)
	require.NoError(t, err)

	gotErr, ok := repliedWith.(jsonrpc2.Error)
	require.True(t, ok)
	assert.Equal(t, jsonrpc2.InvalidParams, gotErr.Code())
}

func TestHandle_FatalError(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()
	fatalErr := errors.New("fatal")

	jsonrpc2.Handle(mux, "test",
		func(_ context.Context, _ jsonrpc2.Nullable[handleTestParams], _ jsonrpc2.Conn) (handleTestResult, error) {
			return handleTestResult{}, fatalErr
		},
	)

	req := &stubRequest{id: "1", method: "test", params: json.RawMessage(`{"value":"test"}`)}
	err := mux.ServeRPC(t.Context(), req, noopReplier, nil)
	require.ErrorIs(t, err, fatalErr)
}

func TestHandle_InvalidParams_UnmarshalFailure(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()
	called := false

	jsonrpc2.Handle(mux, "test",
		func(_ context.Context, _ jsonrpc2.Nullable[handleTestParams], _ jsonrpc2.Conn) (handleTestResult, error) {
			called = true

			return handleTestResult{Result: ""}, nil
		},
	)

	var repliedWith any

	reply := jsonrpc2.Replier(func(_ context.Context, result any) error {
		repliedWith = result

		return nil
	})

	req := &stubRequest{id: "1", method: "test", params: json.RawMessage(`not json`)}
	err := mux.ServeRPC(t.Context(), req, reply, nil)
	require.NoError(t, err)
	assert.False(t, called)

	gotErr, ok := repliedWith.(jsonrpc2.Error)
	require.True(t, ok)
	assert.Equal(t, jsonrpc2.InvalidParams, gotErr.Code())
}

func TestHandle_OmittedParams_CallsHandler(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()

	var gotParams jsonrpc2.Nullable[handleTestParams]

	jsonrpc2.Handle(mux, "test",
		func(_ context.Context, p jsonrpc2.Nullable[handleTestParams], _ jsonrpc2.Conn) (handleTestResult, error) {
			gotParams = p

			return handleTestResult{Result: ""}, nil
		},
	)

	req := &stubRequest{id: "1", method: "test", params: json.RawMessage(nil)}
	err := mux.ServeRPC(t.Context(), req, noopReplier, nil)
	require.NoError(t, err)
	assert.False(t, gotParams.Valid)
}

func TestHandle_NullParams(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()

	var gotParams jsonrpc2.Nullable[*handleTestParams]

	jsonrpc2.Handle(mux, "test",
		func(_ context.Context, p jsonrpc2.Nullable[*handleTestParams], _ jsonrpc2.Conn) (handleTestResult, error) {
			gotParams = p

			return handleTestResult{Result: ""}, nil
		},
	)

	req := &stubRequest{id: "1", method: "test", params: json.RawMessage("null")}
	err := mux.ServeRPC(t.Context(), req, noopReplier, nil)
	require.NoError(t, err)
	assert.True(t, gotParams.Valid)
	assert.Nil(t, gotParams.Value)
}

func TestHandle_Notification_NoReply(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()
	called := false

	jsonrpc2.Handle(mux, "test",
		func(_ context.Context, p jsonrpc2.Nullable[handleTestParams], _ jsonrpc2.Conn) (handleTestResult, error) {
			called = true

			return handleTestResult{Result: p.Value.Value}, nil
		},
	)

	reply := jsonrpc2.Replier(func(_ context.Context, _ any) error {
		require.Fail(t, "reply should not be called for a notification")

		return nil
	})

	params, err := json.Marshal(handleTestParams{Value: "hello"})
	require.NoError(t, err)

	req := &stubRequest{id: nil, method: "test", params: json.RawMessage(params)}
	err = mux.ServeRPC(t.Context(), req, reply, nil)
	require.NoError(t, err)
	assert.True(t, called)
}

func TestHandle_Notification_RPCError_NoReply(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()
	rpcErr := jsonrpc2.NewError(jsonrpc2.InternalError, "oops", nil)

	jsonrpc2.Handle(mux, "test",
		func(_ context.Context, _ jsonrpc2.Nullable[handleTestParams], _ jsonrpc2.Conn) (handleTestResult, error) {
			return handleTestResult{}, rpcErr
		},
	)

	reply := jsonrpc2.Replier(func(_ context.Context, _ any) error {
		require.Fail(t, "reply should not be called for a notification")

		return nil
	})

	params, err := json.Marshal(handleTestParams{Value: "hello"})
	require.NoError(t, err)

	req := &stubRequest{id: nil, method: "test", params: json.RawMessage(params)}
	err = mux.ServeRPC(t.Context(), req, reply, nil)
	require.NoError(t, err)
}

func TestHandleNotification_HappyPath(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()
	called := false

	jsonrpc2.HandleNotification(mux, "test",
		func(_ context.Context, p jsonrpc2.Nullable[handleTestParams], _ jsonrpc2.Conn) error {
			called = true

			assert.True(t, p.Valid)
			assert.Equal(t, "hello", p.Value.Value)

			return nil
		})

	params, err := json.Marshal(handleTestParams{Value: "hello"})
	require.NoError(t, err)

	req := &stubRequest{id: nil, method: "test", params: json.RawMessage(params)}
	err = mux.ServeRPC(t.Context(), req, noopReplier, nil)
	require.NoError(t, err)
	assert.True(t, called)
}

func TestHandleNotification_RPCError_Discarded(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()
	rpcErr := jsonrpc2.NewError(jsonrpc2.InternalError, "oops", nil)

	jsonrpc2.HandleNotification(mux, "test",
		func(_ context.Context, _ jsonrpc2.Nullable[handleTestParams], _ jsonrpc2.Conn) error {
			return rpcErr
		})

	params, err := json.Marshal(handleTestParams{Value: "hello"})
	require.NoError(t, err)

	req := &stubRequest{id: nil, method: "test", params: json.RawMessage(params)}
	err = mux.ServeRPC(t.Context(), req, noopReplier, nil)
	require.NoError(t, err)
}

func TestHandleNotification_FatalError(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()
	fatalErr := errors.New("fatal")

	jsonrpc2.HandleNotification(mux, "test",
		func(_ context.Context, _ jsonrpc2.Nullable[handleTestParams], _ jsonrpc2.Conn) error {
			return fatalErr
		})

	params, err := json.Marshal(handleTestParams{Value: "hello"})
	require.NoError(t, err)

	req := &stubRequest{id: nil, method: "test", params: json.RawMessage(params)}
	err = mux.ServeRPC(t.Context(), req, noopReplier, nil)
	require.ErrorIs(t, err, fatalErr)
}

func TestHandleNotification_UnmarshalFailure_Discarded(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()
	called := false

	jsonrpc2.HandleNotification(mux, "test",
		func(_ context.Context, _ jsonrpc2.Nullable[handleTestParams], _ jsonrpc2.Conn) error {
			called = true

			return nil
		})

	req := &stubRequest{id: nil, method: "test", params: json.RawMessage(`not json`)}
	err := mux.ServeRPC(t.Context(), req, noopReplier, nil)
	require.NoError(t, err)
	assert.False(t, called)
}

func TestHandleNotification_OmittedParams_CallsHandler(t *testing.T) {
	t.Parallel()

	mux := jsonrpc2.NewMux()

	var gotParams jsonrpc2.Nullable[handleTestParams]

	jsonrpc2.HandleNotification(mux, "test",
		func(_ context.Context, p jsonrpc2.Nullable[handleTestParams], _ jsonrpc2.Conn) error {
			gotParams = p

			return nil
		})

	req := &stubRequest{id: nil, method: "test", params: json.RawMessage(nil)}
	err := mux.ServeRPC(t.Context(), req, noopReplier, nil)
	require.NoError(t, err)
	assert.False(t, gotParams.Valid)
}
