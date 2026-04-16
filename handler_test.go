package jsonrpc2_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/mcriley821/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandlerFunc_ServeRPC verifies that HandlerFunc.ServeRPC delegates to the
// underlying function and that HandlerFunc satisfies the Handler interface.
func TestHandlerFunc_ServeRPC(t *testing.T) {
	t.Parallel()

	called := false
	h := jsonrpc2.HandlerFunc(
		func(_ context.Context, _ jsonrpc2.Request, _ jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			called = true

			return nil
		},
	)

	err := h.ServeRPC(t.Context(), nil, nil, nil)
	require.NoError(t, err)
	assert.True(t, called)
}

func TestConnHandlerMessageErrors(t *testing.T) {
	t.Parallel()

	subtest := func(data []byte, errContains string) {
		conn, p := getTestConn(t, assertNotCalledHandler(t))

		errCh := make(chan error, 1)

		go func() {
			if _, err := p.Write(data); err != nil {
				errCh <- err
			}
		}()

		select {
		case <-time.After(time.Second):
			require.FailNow(t, "timeout waiting for conn.Done")
		case err := <-errCh:
			require.FailNow(t, err.Error())
		case <-conn.Done():
			err := conn.Err()
			require.Error(t, err)
			assert.ErrorContains(t, err, errContains)
		}
	}

	t.Run("not json", func(t *testing.T) {
		t.Parallel()
		subtest([]byte("not json"), "stream read")
	})

	t.Run("not jsonrpc 2.0", func(t *testing.T) {
		t.Parallel()
		subtest([]byte("{}"), "unsupported jsonrpc")
	})

	t.Run("invalid message", func(t *testing.T) {
		t.Parallel()
		subtest([]byte(`{"jsonrpc":"2.0","method":12,"params":"test"}`), "message unmarshal")
	})
}

func TestConnHandler_ErrorCloses(t *testing.T) {
	t.Parallel()

	conn, p := getTestConn(t, jsonrpc2.HandlerFunc(
		func(_ context.Context, req jsonrpc2.Request, _ jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			assert.Equal(t, "1", req.ID())
			assert.Equal(t, "test", req.Method())
			assert.Equal(t, json.RawMessage(nil), req.Params())

			return errors.New("test error")
		},
	))

	go func() {
		_, err := p.Write([]byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`))
		assert.NoError(t, err)
	}()

	select {
	case <-time.After(time.Second):
		require.FailNow(t, "timeout waiting for conn.Done")
	case <-conn.Done():
		err := conn.Err()
		require.Error(t, err)
		assert.ErrorContains(t, err, "test error")
	}
}

func TestConnHandler_ValidReply(t *testing.T) {
	t.Parallel()

	_, p := getTestConn(t, jsonrpc2.HandlerFunc(
		func(ctx context.Context, req jsonrpc2.Request, reply jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			assert.Equal(t, "1", req.ID())
			assert.Equal(t, "test", req.Method())
			assert.Equal(t, json.RawMessage(nil), req.Params())

			return reply(ctx, "testReplyResult")
		},
	))

	respCh := make(chan []byte, 1)

	go func() {
		_, err := p.Write([]byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`))
		assert.NoError(t, err)

		var raw json.RawMessage

		err = json.NewDecoder(p).Decode(&raw)
		assert.NoError(t, err)
		assert.NotEmpty(t, raw)

		respCh <- []byte(raw)
	}()

	select {
	case <-time.After(time.Second):
		require.FailNow(t, "timeout waiting for response")
	case resp := <-respCh:
		expected := []byte(`{"jsonrpc":"2.0","id":"1","result":"testReplyResult"}`)
		assert.Equal(t, expected, resp)
	}
}

func TestConnHandler_ErrorReply(t *testing.T) {
	t.Parallel()

	jerr := jsonrpc2.NewError(jsonrpc2.ErrorCode(5), "test message", "test data")

	_, p := getTestConn(t, jsonrpc2.HandlerFunc(
		func(ctx context.Context, req jsonrpc2.Request, reply jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			assert.Equal(t, "1", req.ID())
			assert.Equal(t, "test", req.Method())
			assert.Equal(t, json.RawMessage(nil), req.Params())

			return reply(ctx, jerr)
		},
	))

	respCh := make(chan []byte, 1)

	go func() {
		_, err := p.Write([]byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`))
		assert.NoError(t, err)

		var raw json.RawMessage

		err = json.NewDecoder(p).Decode(&raw)
		assert.NoError(t, err)
		assert.NotEmpty(t, raw)

		respCh <- []byte(raw)
	}()

	select {
	case <-time.After(time.Second):
		require.FailNow(t, "timeout waiting for response")
	case resp := <-respCh:
		expected := []byte(`{"jsonrpc":"2.0","id":"1","error":{"code":5,"message":"test message","data":"test data"}}`)
		assert.Equal(t, expected, resp)
	}
}

func TestConnHandler_Notification(t *testing.T) {
	t.Parallel()

	handlerCalled := make(chan struct{})

	_, p := getTestConn(t, jsonrpc2.HandlerFunc(
		func(ctx context.Context, req jsonrpc2.Request, reply jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			defer close(handlerCalled)

			assert.Nil(t, req.ID())
			assert.Equal(t, "notify", req.Method())
			assert.Equal(t, json.RawMessage(nil), req.Params())

			// Calling reply on a notification must be a no-op per JSON-RPC 2.0 §6.
			return reply(ctx, "ignored")
		},
	))

	_, err := p.Write([]byte(`{"jsonrpc":"2.0","method":"notify"}`))
	require.NoError(t, err)

	select {
	case <-time.After(time.Second):
		require.FailNow(t, "timeout waiting for handler to be called")
	case <-handlerCalled:
	}

	// Verify no response was sent back to the peer.
	require.NoError(t, p.SetReadDeadline(time.Now().Add(50*time.Millisecond)))

	buf := make([]byte, 1)
	n, err := p.Read(buf)
	assert.Zero(t, n)
	assert.Error(t, err, "expected no data written to peer after notification")
}

func TestConnHandler_ConnCall(t *testing.T) {
	t.Parallel()

	_, p := getTestConn(t, jsonrpc2.HandlerFunc(
		func(ctx context.Context, req jsonrpc2.Request, reply jsonrpc2.Replier, conn jsonrpc2.Conn) error {
			assert.Equal(t, "1", req.ID())
			assert.Equal(t, "test", req.Method())
			assert.Equal(t, json.RawMessage(nil), req.Params())

			resp, err := conn.Call(ctx, "handler", nil)
			require.NoError(t, err)
			require.False(t, resp.Failed())

			var res string

			require.NoError(t, resp.Result(&res))
			assert.Equal(t, "result2", res)

			return reply(ctx, "result1")
		},
	))

	respCh := make(chan []byte, 1)
	errCh := make(chan error, 1)

	go connRespondInterleaved(t, p, respCh, errCh)

	select {
	case <-time.After(time.Second):
		require.FailNow(t, "timeout waiting for response")
	case err := <-errCh:
		require.FailNow(t, err.Error())
	case resp := <-respCh:
		expected := []byte(`{"jsonrpc":"2.0","id":"1","result":"result1"}`)
		assert.Equal(t, expected, resp)
	}
}

func connRespondInterleaved(t *testing.T, p net.Conn, respCh chan<- []byte, errCh chan<- error) {
	t.Helper()

	if _, err := p.Write([]byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`)); err != nil {
		errCh <- err

		return
	}

	dec := json.NewDecoder(p)

	var rawReq json.RawMessage

	if err := dec.Decode(&rawReq); err != nil {
		errCh <- err

		return
	}

	req := []byte(rawReq)
	assert.NotEmpty(t, req)

	reqObj := make(map[string]any)

	if err := json.Unmarshal(req, &reqObj); err != nil {
		errCh <- err

		return
	}

	if !assert.Contains(t, reqObj, "id") {
		errCh <- assert.AnError

		return
	}

	expected := fmt.Sprintf(`{"jsonrpc":"2.0","id":"%s","method":"handler"}`, reqObj["id"])
	if !assert.Equal(t, expected, string(req)) {
		errCh <- assert.AnError

		return
	}

	resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":"%s","result":"result2"}`, reqObj["id"])

	if _, err := p.Write([]byte(resp)); err != nil {
		errCh <- err

		return
	}

	var rawResp json.RawMessage

	if err := dec.Decode(&rawResp); err != nil {
		errCh <- err

		return
	}

	respCh <- []byte(rawResp)
}
