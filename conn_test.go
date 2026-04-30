package jsonrpc2_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/mcriley821/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockingWriteConn wraps a net.Conn and blocks the first Write call until
// released, allowing tests to stall the write goroutine on demand.
type blockingWriteConn struct {
	net.Conn

	writeCalled chan struct{}
	release     chan struct{}
}

func newBlockingWriteConn(t *testing.T) *blockingWriteConn {
	t.Helper()

	c1, c2 := net.Pipe()

	t.Cleanup(func() { _ = c1.Close() })
	t.Cleanup(func() { _ = c2.Close() })

	return &blockingWriteConn{
		Conn:        c1,
		writeCalled: make(chan struct{}),
		release:     make(chan struct{}),
	}
}

func (b *blockingWriteConn) Write(p []byte) (int, error) {
	select {
	case b.writeCalled <- struct{}{}:
	default:
	}

	<-b.release

	return b.Conn.Write(p) //nolint:wrapcheck
}

func assertNotCalledHandler(t *testing.T) jsonrpc2.Handler {
	t.Helper()

	return jsonrpc2.HandlerFunc(
		func(_ context.Context, _ jsonrpc2.Request, _ jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			require.FailNow(t, "handler should not be called")

			return nil
		},
	)
}

func getTestConn(t *testing.T, handler jsonrpc2.Handler) (jsonrpc2.Conn, net.Conn) {
	t.Helper()

	s, p := newTestStream(t)
	stream := jsonrpc2.NewStream(s)
	require.NotNil(t, stream)

	t.Cleanup(func() { _ = stream.Close() })

	conn := jsonrpc2.NewConn(t.Context(), stream, jsonrpc2.WithHandler(handler))
	require.NotNil(t, conn)

	t.Cleanup(func() { _ = conn.Close(t.Context()) })

	return conn, p
}

func pipeNotif(t *testing.T, p net.Conn, notifCh chan<- []byte, errCh chan<- error) {
	t.Helper()

	var raw json.RawMessage

	if err := json.NewDecoder(p).Decode(&raw); err != nil {
		errCh <- err

		return
	}

	notifCh <- []byte(raw)
}

func TestNewConn(t *testing.T) {
	t.Parallel()

	_, _ = getTestConn(t, assertNotCalledHandler(t))
}

func TestConn_Call_Errors(t *testing.T) {
	t.Parallel()

	t.Run("bad params", func(t *testing.T) {
		t.Parallel()

		conn, _ := getTestConn(t, assertNotCalledHandler(t))

		resp, err := conn.Call(t.Context(), "", func() {})
		assert.Nil(t, resp)
		require.Error(t, err)
	})

	t.Run("canceled context", func(t *testing.T) {
		t.Parallel()

		rwc := newBlockingWriteConn(t)
		stream := jsonrpc2.NewStream(rwc)
		conn := jsonrpc2.NewConn(t.Context(), stream, jsonrpc2.WithHandler(assertNotCalledHandler(t)))
		t.Cleanup(func() { _ = conn.Close(t.Context()) })

		go func() { _ = conn.Notify(t.Context(), "", nil) }()

		<-rwc.writeCalled

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		resp, err := conn.Call(ctx, "", nil)
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, resp)

		close(rwc.release)
	})

	t.Run("closed conn", func(t *testing.T) {
		t.Parallel()

		conn, _ := getTestConn(t, assertNotCalledHandler(t))

		require.NoError(t, conn.Close(t.Context()))

		resp, err := conn.Call(t.Context(), "", nil)
		assert.Nil(t, resp)
		require.ErrorIs(t, err, jsonrpc2.ErrClosed)
	})
}

func TestConn_Call(t *testing.T) {
	t.Parallel()

	conn, p := getTestConn(t, assertNotCalledHandler(t))

	idCh := make(chan any, 1)
	errCh := make(chan error, 1)

	go pipeRespond(t, p, idCh, errCh, nil)

	resp, err := conn.Call(t.Context(), "", nil)
	require.NoError(t, err)
	require.NotNil(t, resp)

	select {
	case err := <-errCh:
		require.FailNow(t, err.Error())
	case id := <-idCh:
		assert.Equal(t, id, resp.ID())
	}
}

func TestConn_Notify_BadParams(t *testing.T) {
	t.Parallel()

	conn, _ := getTestConn(t, assertNotCalledHandler(t))

	err := conn.Notify(t.Context(), "", func() {})
	assert.Error(t, err)
}

func TestConn_Notify_CanceledContext(t *testing.T) {
	t.Parallel()

	rwc := newBlockingWriteConn(t)
	stream := jsonrpc2.NewStream(rwc)
	conn := jsonrpc2.NewConn(t.Context(), stream, jsonrpc2.WithHandler(assertNotCalledHandler(t)))
	t.Cleanup(func() { _ = conn.Close(t.Context()) })

	go func() { _ = conn.Notify(t.Context(), "", nil) }()

	<-rwc.writeCalled

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := conn.Notify(ctx, "", nil)
	require.ErrorIs(t, err, context.Canceled)

	close(rwc.release)
}

func TestConn_Notify_Closed(t *testing.T) {
	t.Parallel()

	conn, _ := getTestConn(t, assertNotCalledHandler(t))

	require.NoError(t, conn.Close(t.Context()))

	err := conn.Notify(t.Context(), "", nil)
	require.ErrorIs(t, err, jsonrpc2.ErrClosed)
}

func TestConn(t *testing.T) {
	t.Parallel()

	conn, p := getTestConn(t, assertNotCalledHandler(t))

	ch := make(chan []byte, 1)
	errCh := make(chan error, 1)

	go pipeNotif(t, p, ch, errCh)

	err := conn.Notify(t.Context(), "", nil)
	require.NoError(t, err)

	select {
	case err := <-errCh:
		require.FailNow(t, err.Error())
	case data := <-ch:
		assert.NotEmpty(t, data)
	}
}

func TestConn_Close(t *testing.T) {
	t.Parallel()

	conn, _ := getTestConn(t, assertNotCalledHandler(t))

	require.NoError(t, conn.Close(t.Context()))

	{
		resp, err := conn.Call(t.Context(), "", nil)
		require.ErrorIs(t, err, jsonrpc2.ErrClosed)
		assert.Nil(t, resp)
	}

	{
		err := conn.Notify(t.Context(), "", nil)
		require.ErrorIs(t, err, jsonrpc2.ErrClosed)
	}

	require.NoError(t, conn.Close(t.Context()))
}

func TestConn_Err_NilBeforeClose(t *testing.T) {
	t.Parallel()

	conn, _ := getTestConn(t, assertNotCalledHandler(t))

	assert.NoError(t, conn.Err())
}

func TestConn_Done_ErrClosed(t *testing.T) {
	t.Parallel()

	conn, _ := getTestConn(t, assertNotCalledHandler(t))

	require.NoError(t, conn.Close(t.Context()))

	select {
	case <-time.After(time.Second):
		require.FailNow(t, "Done not closed after Close")
	case <-conn.Done():
	}

	require.ErrorIs(t, conn.Err(), jsonrpc2.ErrClosed)
}

func TestConn_Done_StreamError(t *testing.T) {
	t.Parallel()

	conn, p := getTestConn(t, assertNotCalledHandler(t))

	// Closing the peer causes a read error on the conn side.
	require.NoError(t, p.Close())

	select {
	case <-time.After(time.Second):
		require.FailNow(t, "Done not closed after stream failure")
	case <-conn.Done():
	}

	require.Error(t, conn.Err())
	assert.NotErrorIs(t, conn.Err(), jsonrpc2.ErrClosed)
}

func TestConn_Done_MultipleWaiters(t *testing.T) {
	t.Parallel()

	conn, _ := getTestConn(t, assertNotCalledHandler(t))

	const waiters = 5

	var wg sync.WaitGroup

	wg.Add(waiters)

	for range waiters {
		go func() {
			defer wg.Done()

			<-conn.Done()
		}()
	}

	require.NoError(t, conn.Close(t.Context()))

	doneCh := make(chan struct{})

	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-time.After(time.Second):
		require.FailNow(t, "not all Done waiters unblocked")
	case <-doneCh:
	}
}

// pipeRespondN reads n requests from p, then responds to each in shuffled
// order, echoing each request's params back as the result. Shuffling exercises
// the inflight ID routing: callers must match responses by ID, not arrival
// order. Errors are sent to errCh.
func pipeRespondN(t *testing.T, p net.Conn, n int, errCh chan<- error) {
	t.Helper()

	dec := json.NewDecoder(p)

	type response struct {
		RPC    string          `json:"jsonrpc"`
		ID     any             `json:"id"`
		Result json.RawMessage `json:"result"`
	}

	resps := make([]response, n)

	for i := range n {
		var req struct {
			ID     any             `json:"id"`
			Params json.RawMessage `json:"params"`
		}

		if err := dec.Decode(&req); err != nil {
			errCh <- err

			return
		}

		resps[i] = response{"2.0", req.ID, req.Params}
	}

	rand.Shuffle(len(resps), func(i, j int) { resps[i], resps[j] = resps[j], resps[i] })

	for _, resp := range resps {
		data, err := json.Marshal(resp)
		if err != nil {
			errCh <- err

			return
		}

		if _, err = p.Write(data); err != nil {
			errCh <- err

			return
		}
	}
}

func TestConn_Call_Concurrent(t *testing.T) {
	t.Parallel()

	const n = 20

	conn, p := getTestConn(t, assertNotCalledHandler(t))

	peerErrCh := make(chan error, 1)

	go pipeRespondN(t, p, n, peerErrCh)

	type result struct {
		resp jsonrpc2.Response
		err  error
		idx  int
	}

	resultCh := make(chan result, n)
	wg := sync.WaitGroup{}
	wg.Add(n)

	for i := range n {
		go func(wg_ *sync.WaitGroup) {
			wg_.Wait()

			resp, err := conn.Call(t.Context(), "test", i)
			resultCh <- result{resp, err, i}
		}(&wg)

		wg.Done()
	}

	for range n {
		select {
		case <-t.Context().Done():
			require.FailNow(t, "concurrent call timed out")
		case err := <-peerErrCh:
			require.FailNow(t, err.Error())
		case res := <-resultCh:
			require.NoError(t, res.err)
			require.NotNil(t, res.resp)

			var got int

			require.NoError(t, res.resp.Result(&got))
			assert.Equal(t, res.idx, got)
		}
	}
}

// TestConn_Call_TOCTOU races a Call against a Close. Run with -race to detect
// any unsynchronised access in the window between the done-channel check and
// the inflight-map insertion.
func TestConn_Call_TOCTOU(t *testing.T) {
	t.Parallel()

	conn, _ := getTestConn(t, assertNotCalledHandler(t))

	callDone := make(chan error, 1)

	go func() {
		_, err := conn.Call(t.Context(), "method", nil)
		callDone <- err
	}()

	go func() { _ = conn.Close(t.Context()) }()

	select {
	case <-time.After(time.Second):
		require.FailNow(t, "Call did not return after Close")
	case err := <-callDone:
		if err != nil {
			require.ErrorIs(t, err, jsonrpc2.ErrClosed)
		}
	}
}

func TestConn_Call_UnblockedOnClose(t *testing.T) {
	t.Parallel()

	conn, p := getTestConn(t, assertNotCalledHandler(t))

	callDone := make(chan error, 1)

	go func() {
		_, err := conn.Call(t.Context(), "method", nil)
		callDone <- err
	}()

	// Read the outgoing request from the peer. This blocks until write has
	// flushed it, which means Call has passed its send and is now blocked
	// in its second select waiting for a response — a real sync point.
	var req json.RawMessage

	err := json.NewDecoder(p).Decode(&req)
	require.NoError(t, err)

	require.NoError(t, conn.Close(t.Context()))

	select {
	case <-time.After(time.Second):
		require.FailNow(t, "in-flight Call not unblocked after Close")
	case err := <-callDone:
		require.ErrorIs(t, err, jsonrpc2.ErrClosed)
	}
}

func getTestConnDefault(t *testing.T) net.Conn {
	t.Helper()

	s, p := newTestStream(t)
	stream := jsonrpc2.NewStream(s)
	require.NotNil(t, stream)

	t.Cleanup(func() { _ = stream.Close() })

	conn := jsonrpc2.NewConn(t.Context(), stream)
	require.NotNil(t, conn)

	t.Cleanup(func() { _ = conn.Close(t.Context()) })

	return p
}

func TestNewConn_NoHandler(t *testing.T) {
	t.Parallel()

	_ = getTestConnDefault(t)
}

func TestNewConn_DefaultHandler_MethodNotFound(t *testing.T) {
	t.Parallel()

	p := getTestConnDefault(t)

	respCh := make(chan []byte, 1)

	go func() {
		_, err := p.Write([]byte(`{"jsonrpc":"2.0","id":"1","method":"unknown"}`))
		assert.NoError(t, err)

		var raw json.RawMessage

		err = json.NewDecoder(p).Decode(&raw)
		assert.NoError(t, err)
		assert.NotEmpty(t, raw)

		respCh <- []byte(raw)
	}()

	select {
	case <-t.Context().Done():
		require.FailNow(t, "timeout waiting for default MethodNotFound response")
	case resp := <-respCh:
		expected := []byte(`{"jsonrpc":"2.0","id":"1","error":{"code":-32601,"message":"Method not implemented"}}`)
		assert.Equal(t, expected, resp)
	}
}

func TestNewConn_DefaultHandler_NotificationIgnored(t *testing.T) {
	t.Parallel()

	p := getTestConnDefault(t)

	_, err := p.Write([]byte(`{"jsonrpc":"2.0","method":"unknown"}`))
	require.NoError(t, err)

	// Verify no response is sent back for a notification.
	require.NoError(t, p.SetReadDeadline(time.Now().Add(50*time.Millisecond)))

	buf := make([]byte, 1)
	n, err := p.Read(buf)
	assert.Zero(t, n)
	assert.Error(t, err, "expected no response for notification with no handler")
}

func TestNewConn_CallsWithinHandler(t *testing.T) {
	t.Parallel()

	done := make(chan struct{}, 1)

	_, p := getTestConn(t, jsonrpc2.HandlerFunc(func(
		ctx context.Context,
		req jsonrpc2.Request,
		_ jsonrpc2.Replier,
		conn jsonrpc2.Conn,
	) error {
		assert.Equal(t, "test", req.Method())

		resp, err := conn.Call(ctx, "reply", nil)

		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.False(t, resp.Failed())

		done <- struct{}{}

		return nil
	}))

	_, err := p.Write([]byte(`{"jsonrpc":"2.0","id":"test-id","method":"test"}`))
	require.NoError(t, err)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(p).Decode(&resp))
	require.Contains(t, resp, "id")

	_, err = p.Write(fmt.Appendf(nil, `{"jsonrpc":"2.0","id":"%s","result":null}`, resp["id"]))
	require.NoError(t, err)

	select {
	case <-t.Context().Done():
		require.FailNow(t, "timeout waiting for handler to finish")
	case <-done:
	}
}

func TestConn_HandlerPanic_RequestRepliesInternalError(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ jsonrpc2.Request, _ jsonrpc2.Replier, _ jsonrpc2.Conn) error {
		panic("boom")
	}

	conn, p := getTestConn(t, jsonrpc2.HandlerFunc(handler))

	_, err := p.Write([]byte(`{"jsonrpc":"2.0","id":"panic-1","method":"test","params":null}`))
	require.NoError(t, err)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(p).Decode(&resp))

	assert.Equal(t, "panic-1", resp["id"])
	require.Contains(t, resp, "error")

	errObj, ok := resp["error"].(map[string]any)
	require.True(t, ok, "expected error object")
	assert.Equal(t, float64(jsonrpc2.InternalError), errObj["code"])

	// Connection should still be alive after the panic.
	select {
	case <-conn.Done():
		require.FailNow(t, "connection should remain open after handler panic")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestConn_HandlerPanic_NotificationDropped(t *testing.T) {
	t.Parallel()

	handler := func(_ context.Context, _ jsonrpc2.Request, _ jsonrpc2.Replier, _ jsonrpc2.Conn) error {
		panic("boom")
	}

	conn, p := getTestConn(t, jsonrpc2.HandlerFunc(handler))

	_, err := p.Write([]byte(`{"jsonrpc":"2.0","method":"test","params":null}`))
	require.NoError(t, err)

	// No response is expected for a notification; ensure the connection
	// stays open and a follow-up request still gets processed.
	select {
	case <-conn.Done():
		require.FailNow(t, "connection should remain open after notification panic")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestConn_Replier_DoubleReply(t *testing.T) {
	t.Parallel()

	repliedCh := make(chan error, 2)

	handler := func(ctx context.Context, _ jsonrpc2.Request, reply jsonrpc2.Replier, _ jsonrpc2.Conn) error {
		repliedCh <- reply(ctx, "first")

		repliedCh <- reply(ctx, "second")

		return nil
	}

	_, p := getTestConn(t, jsonrpc2.HandlerFunc(handler))

	// Send a request from the peer side.
	req := []byte(`{"jsonrpc":"2.0","id":"test-1","method":"test","params":null}`)
	_, err := p.Write(req)
	require.NoError(t, err)

	// Drain the one real response so the write loop doesn't stall.
	var resp json.RawMessage
	require.NoError(t, json.NewDecoder(p).Decode(&resp))

	select {
	case <-t.Context().Done():
		require.FailNow(t, "handler did not complete")
	case err := <-repliedCh:
		require.NoError(t, err, "first reply should succeed")
	}

	select {
	case <-t.Context().Done():
		require.FailNow(t, "handler did not complete")
	case err := <-repliedCh:
		require.ErrorIs(t, err, jsonrpc2.ErrReplied, "second reply should return ErrReplied")
	}
}

func TestConn_BatchRequest_Handling(t *testing.T) { //nolint:funlen
	t.Parallel()

	handler := func(ctx context.Context, req jsonrpc2.Request, reply jsonrpc2.Replier, _ jsonrpc2.Conn) error {
		return reply(ctx, req.Params())
	}

	t.Run("empty batch produces no response", func(t *testing.T) {
		t.Parallel()

		_, p := getTestConn(t, jsonrpc2.HandlerFunc(handler))

		_, err := p.Write([]byte(`[]`))
		require.NoError(t, err)

		// Server silently ignores an empty batch; no bytes should arrive.
		require.NoError(t, p.SetReadDeadline(time.Now().Add(50*time.Millisecond)))

		buf := make([]byte, 1)
		n, err := p.Read(buf)
		assert.Zero(t, n)
		assert.Error(t, err, "expected no response for empty batch")
	})

	t.Run("notifications-only batch produces no response", func(t *testing.T) {
		t.Parallel()

		_, p := getTestConn(t, jsonrpc2.HandlerFunc(handler))

		_, err := p.Write([]byte(`[
			{"jsonrpc":"2.0","method":"foo"},
			{"jsonrpc":"2.0","method":"bar"}
		]`))
		require.NoError(t, err)

		require.NoError(t, p.SetReadDeadline(time.Now().Add(50*time.Millisecond)))

		buf := make([]byte, 1)
		n, err := p.Read(buf)
		assert.Zero(t, n)
		assert.Error(t, err, "expected no response for notifications-only batch")
	})

	t.Run("request in response batch closes connection", func(t *testing.T) {
		t.Parallel()

		conn, p := getTestConn(t, jsonrpc2.HandlerFunc(handler))

		_, err := p.Write([]byte(`[
			{"jsonrpc":"2.0","id":"1","result":"ok"},
			{"jsonrpc":"2.0","id":"2","method":"foo"}
		]`))
		require.NoError(t, err)

		select {
		case <-t.Context().Done():
			require.FailNow(t, "conn did not shut down after mixed batch")
		case <-conn.Done():
		}

		assert.Error(t, conn.Err())
	})

	t.Run("response in request batch closes connection", func(t *testing.T) {
		t.Parallel()

		conn, p := getTestConn(t, jsonrpc2.HandlerFunc(handler))

		_, err := p.Write([]byte(`[
			{"jsonrpc":"2.0","id":"1","method":"foo"},
			{"jsonrpc":"2.0","id":"2","result":"ok"}
		]`))
		require.NoError(t, err)

		select {
		case <-t.Context().Done():
			require.FailNow(t, "conn did not shut down after mixed batch")
		case <-conn.Done():
		}

		assert.Error(t, conn.Err())
	})
}

// TestConn_ResponseSentBeforeHandlerReturns verifies that a response is sent
// to the peer as soon as the handler calls the [Replier], even if the handler
// itself continues to block afterwards.
func TestConn_ResponseSentBeforeHandlerReturns(t *testing.T) {
	t.Parallel()

	handlerReplied := make(chan struct{})
	unblockHandler := make(chan struct{})

	handler := jsonrpc2.HandlerFunc(func(
		ctx context.Context,
		_ jsonrpc2.Request,
		reply jsonrpc2.Replier,
		_ jsonrpc2.Conn,
	) error {
		if err := reply(ctx, "done"); err != nil {
			return err
		}

		close(handlerReplied)

		select {
		case <-unblockHandler:
		case <-ctx.Done():
		}

		return nil
	})

	_, p := getTestConn(t, handler)

	_, err := p.Write([]byte(`{"jsonrpc":"2.0","id":"test-1","method":"test"}`))
	require.NoError(t, err)

	respCh := make(chan json.RawMessage, 1)
	peerErrCh := make(chan error, 1)

	go func() {
		var resp json.RawMessage
		if err := json.NewDecoder(p).Decode(&resp); err != nil {
			peerErrCh <- err

			return
		}

		respCh <- resp
	}()

	select {
	case <-t.Context().Done():
		require.FailNow(t, "handler did not call reply")
	case <-handlerReplied:
	}

	// The response must arrive at the peer while the handler is still blocked.
	select {
	case <-t.Context().Done():
		require.FailNow(t, "response not received while handler was blocked after replying")
	case err := <-peerErrCh:
		require.NoError(t, err)
	case resp := <-respCh:
		assert.NotEmpty(t, resp)
	}

	close(unblockHandler)
}

func TestNewConn_BatchResponseDeferredUntilAllHandlersDone(t *testing.T) {
	t.Parallel()

	_, p := getTestConn(t, jsonrpc2.HandlerFunc(func(
		ctx context.Context,
		req jsonrpc2.Request,
		reply jsonrpc2.Replier,
		conn jsonrpc2.Conn,
	) error {
		switch req.Method() {
		case "blocking":
			resp, err := conn.Call(ctx, "inner", nil)
			require.NoError(t, err)
			require.False(t, resp.Failed())

			return reply(ctx, "blocking-done")
		case "immediate":
			return reply(ctx, "immediate-done")
		}

		return nil
	}))

	batch := `[{"jsonrpc":"2.0","id":"b1","method":"blocking"},{"jsonrpc":"2.0","id":"b2","method":"immediate"}]`
	_, err := p.Write([]byte(batch))
	require.NoError(t, err)

	// The "inner" call from the blocking handler arrives first because the
	// batch response is held until both handlers have replied.
	// Reading the batch response before responding here would deadlock.
	var innerCall map[string]any
	require.NoError(t, json.NewDecoder(p).Decode(&innerCall))
	require.Equal(t, "inner", innerCall["method"])

	innerID, ok := innerCall["id"].(string)
	require.True(t, ok)

	_, err = p.Write(fmt.Appendf(nil, `{"jsonrpc":"2.0","id":"%s","result":null}`, innerID))
	require.NoError(t, err)

	// Now both handlers can finish, and the batch response arrives.
	var batchResp []map[string]any
	require.NoError(t, json.NewDecoder(p).Decode(&batchResp))
	require.Len(t, batchResp, 2)
}

func TestConn_Batch_Errors(t *testing.T) {
	t.Parallel()

	t.Run("empty batch returns error", func(t *testing.T) {
		t.Parallel()

		conn, _ := getTestConn(t, assertNotCalledHandler(t))

		resps, err := conn.Batch(t.Context())
		require.Error(t, err)
		assert.Nil(t, resps)
	})

	t.Run("bad params returns error", func(t *testing.T) {
		t.Parallel()

		conn, _ := getTestConn(t, assertNotCalledHandler(t))

		resps, err := conn.Batch(t.Context(), jsonrpc2.BatchCall("test", func() {}))
		require.Error(t, err)
		assert.Nil(t, resps)
	})

	t.Run("canceled context", func(t *testing.T) {
		t.Parallel()

		rwc := newBlockingWriteConn(t)
		stream := jsonrpc2.NewStream(rwc)
		conn := jsonrpc2.NewConn(t.Context(), stream, jsonrpc2.WithHandler(assertNotCalledHandler(t)))
		t.Cleanup(func() { _ = conn.Close(t.Context()) })

		go func() { _ = conn.Notify(t.Context(), "", nil) }()

		<-rwc.writeCalled

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		resps, err := conn.Batch(ctx, jsonrpc2.BatchCall("test", nil))
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, resps)

		close(rwc.release)
	})

	t.Run("closed conn returns ErrClosed", func(t *testing.T) {
		t.Parallel()

		conn, _ := getTestConn(t, assertNotCalledHandler(t))

		require.NoError(t, conn.Close(t.Context()))

		resps, err := conn.Batch(t.Context(), jsonrpc2.BatchCall("test", nil))
		require.ErrorIs(t, err, jsonrpc2.ErrClosed)
		assert.Nil(t, resps)
	})
}

func TestConn_Batch_Notifications(t *testing.T) {
	t.Parallel()

	conn, p := getTestConn(t, assertNotCalledHandler(t))

	peerErrCh := make(chan error, 1)

	go func() {
		var raw json.RawMessage

		if err := json.NewDecoder(p).Decode(&raw); err != nil {
			peerErrCh <- err

			return
		}

		var batch []map[string]any

		if err := json.Unmarshal(raw, &batch); err != nil {
			peerErrCh <- err

			return
		}

		for _, item := range batch {
			_, hasID := item["id"]
			if hasID {
				peerErrCh <- errors.New("expected notification (no id) but found id field")

				return
			}
		}
	}()

	resps, err := conn.Batch(t.Context(),
		jsonrpc2.BatchNotification("method1", nil),
		jsonrpc2.BatchNotification("method2", nil),
	)
	require.NoError(t, err)
	assert.Nil(t, resps)

	select {
	case err := <-peerErrCh:
		require.FailNow(t, err.Error())
	default:
	}
}

func TestConn_Batch_Mixed(t *testing.T) { //nolint:funlen
	t.Parallel()

	conn, p := getTestConn(t, assertNotCalledHandler(t))

	peerErrCh := make(chan error, 1)

	go func() {
		var raw json.RawMessage

		if err := json.NewDecoder(p).Decode(&raw); err != nil {
			peerErrCh <- err

			return
		}

		var batch []struct {
			ID     any             `json:"id"`
			Params json.RawMessage `json:"params"`
		}

		if err := json.Unmarshal(raw, &batch); err != nil {
			peerErrCh <- err

			return
		}

		responses := make([]map[string]any, 0, len(batch))

		for _, item := range batch {
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      item.ID,
				"result":  item.Params,
			}
			responses = append(responses, resp)
		}

		data, err := json.Marshal(responses)
		if err != nil {
			peerErrCh <- err

			return
		}

		if _, err := p.Write(data); err != nil {
			peerErrCh <- err

			return
		}
	}()

	resps, err := conn.Batch(t.Context(),
		jsonrpc2.BatchCall("method1", "params1"),
		jsonrpc2.BatchNotification("method2", nil),
		jsonrpc2.BatchCall("method3", "params3"),
	)
	require.NoError(t, err)
	require.Len(t, resps, 3)

	select {
	case err := <-peerErrCh:
		require.FailNow(t, err.Error())

	default:
	}

	for i, resp := range resps {
		if i != 1 {
			require.NotNil(t, resp, "response %d should not be nil", i)
			assert.False(t, resp.Failed())
		} else {
			assert.Nil(t, resp)
		}
	}
}

func TestConn_Batch_WireFormat(t *testing.T) { //nolint:cyclop,funlen
	t.Parallel()

	conn, p := getTestConn(t, assertNotCalledHandler(t))

	peerErrCh := make(chan error, 1)

	go func() {
		var raw json.RawMessage

		if err := json.NewDecoder(p).Decode(&raw); err != nil {
			peerErrCh <- err

			return
		}

		trimmed := bytes.TrimLeft(raw, " \t\r\n")
		if len(trimmed) == 0 || trimmed[0] != '[' {
			peerErrCh <- errors.New("expected JSON array for batch")

			return
		}

		var batch []struct {
			ID any `json:"id"`
		}

		if err := json.Unmarshal(raw, &batch); err != nil {
			peerErrCh <- err

			return
		}

		responses := make([]map[string]any, 0)

		for _, item := range batch {
			if item.ID != nil {
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      item.ID,
					"result":  nil,
				}
				responses = append(responses, resp)
			}
		}

		data, err := json.Marshal(responses)
		if err != nil {
			peerErrCh <- err

			return
		}

		if _, err := p.Write(data); err != nil {
			peerErrCh <- err

			return
		}
	}()

	resps, err := conn.Batch(t.Context(),
		jsonrpc2.BatchCall("method1", nil),
		jsonrpc2.BatchNotification("method2", nil),
	)
	require.NoError(t, err)
	require.Len(t, resps, 2)
	assert.Nil(t, resps[1])

	select {
	case err := <-peerErrCh:
		require.FailNow(t, err.Error())
	default:
	}
}

func TestConn_Batch_Concurrent(t *testing.T) { //nolint:cyclop,funlen
	t.Parallel()

	conn, p := getTestConn(t, assertNotCalledHandler(t))

	peerErrCh := make(chan error, 1)

	go func() {
		dec := json.NewDecoder(p)

		for range 2 {
			var raw json.RawMessage

			if err := dec.Decode(&raw); err != nil {
				peerErrCh <- err

				return
			}

			var batch []struct {
				ID any `json:"id"`
			}

			if err := json.Unmarshal(raw, &batch); err != nil {
				peerErrCh <- err

				return
			}

			responses := make([]map[string]any, 0, len(batch))

			for _, item := range batch {
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      item.ID,
					"result":  "ok",
				}
				responses = append(responses, resp)
			}

			data, err := json.Marshal(responses)
			if err != nil {
				peerErrCh <- err

				return
			}

			if _, err := p.Write(data); err != nil {
				peerErrCh <- err

				return
			}
		}
	}()

	resultCh := make(chan error, 2)

	for range 2 {
		go func() {
			resps, err := conn.Batch(t.Context(),
				jsonrpc2.BatchCall("method1", nil),
				jsonrpc2.BatchCall("method2", nil),
			)
			if err != nil {
				resultCh <- err

				return
			}

			if len(resps) != 2 {
				resultCh <- errors.New("expected 2 responses")

				return
			}

			resultCh <- nil
		}()
	}

	for range 2 {
		if err := <-resultCh; err != nil {
			require.FailNow(t, err.Error())
		}
	}

	select {
	case err := <-peerErrCh:
		require.FailNow(t, err.Error())
	default:
	}
}
