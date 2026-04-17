package jsonrpc2_test

import (
	"context"
	"encoding/json"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/mcriley821/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	conn := jsonrpc2.NewConn(t.Context(), stream, handler)
	require.NotNil(t, conn)

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

func TestConn_Call(t *testing.T) {
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

		conn, _ := getTestConn(t, assertNotCalledHandler(t))

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		resp, err := conn.Call(ctx, "", nil)
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, resp)
	})

	t.Run("closed conn", func(t *testing.T) {
		t.Parallel()

		conn, _ := getTestConn(t, assertNotCalledHandler(t))

		require.NoError(t, conn.Close(t.Context()))

		resp, err := conn.Call(t.Context(), "", nil)
		assert.Nil(t, resp)
		require.ErrorIs(t, err, jsonrpc2.ErrClosed)
	})

	t.Run("ok", func(t *testing.T) {
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
	})
}

func TestConn_Notify(t *testing.T) {
	t.Parallel()

	t.Run("bad params", func(t *testing.T) {
		t.Parallel()

		conn, _ := getTestConn(t, assertNotCalledHandler(t))

		err := conn.Notify(t.Context(), "", func() {})
		assert.Error(t, err)
	})

	t.Run("canceled context", func(t *testing.T) {
		t.Parallel()

		conn, _ := getTestConn(t, assertNotCalledHandler(t))

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		err := conn.Notify(ctx, "", nil)
		assert.Error(t, err)
	})

	t.Run("closed conn", func(t *testing.T) {
		t.Parallel()

		conn, _ := getTestConn(t, assertNotCalledHandler(t))

		require.NoError(t, conn.Close(t.Context()))

		err := conn.Notify(t.Context(), "", nil)
		assert.Error(t, err)
	})

	t.Run("ok", func(t *testing.T) {
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
	})
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

// TestConn_Call_TOCTOU exercises the race window between the done-channel
// fast-path check and the inflight-map insertion in Call. It repeatedly races a
// Call against a Close so that -race can detect any unsynchronised access, and
// asserts that every Call returns either a valid response or ErrClosed (never
// hangs or returns an unexpected error).
func TestConn_Call_TOCTOU(t *testing.T) {
	t.Parallel()

	const iterations = 500

	for range iterations {
		conn, p := getTestConn(t, assertNotCalledHandler(t))

		callDone := make(chan error, 1)

		go func() {
			_, err := conn.Call(t.Context(), "method", nil)
			callDone <- err
		}()

		// Close races with the Call above. Depending on scheduling, the Call
		// may see the connection already closed (ErrClosed fast-path), insert
		// into the inflight map before shutdown clears it (and get ErrClosed
		// from the done-channel select), or insert after shutdown (which the
		// fix prevents). All outcomes must return ErrClosed, not hang.
		go func() { _ = conn.Close(t.Context()) }()

		select {
		case <-time.After(time.Second):
			require.FailNow(t, "Call did not return after Close")
		case err := <-callDone:
			if err != nil {
				require.ErrorIs(t, err, jsonrpc2.ErrClosed)
			}
		}

		// Drain peer so its goroutines don't leak between iterations.
		_ = p.Close()
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
