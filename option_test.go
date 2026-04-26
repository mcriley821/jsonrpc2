package jsonrpc2_test

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"

	"github.com/mcriley821/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type spyLogger struct {
	mu      sync.Mutex
	entries []string
}

func (s *spyLogger) DebugContext(_ context.Context, msg string, _ ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, msg)
}

func (s *spyLogger) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.entries))
	copy(out, s.entries)
	return out
}

func getTestConnWithLogger(t *testing.T, handler jsonrpc2.Handler, logger jsonrpc2.Logger) (jsonrpc2.Conn, net.Conn) {
	t.Helper()

	s, p := newTestStream(t)
	stream := jsonrpc2.NewStream(s)
	require.NotNil(t, stream)
	t.Cleanup(func() { _ = stream.Close() })

	conn := jsonrpc2.NewConn(t.Context(), stream,
		jsonrpc2.WithHandler(handler),
		jsonrpc2.WithLogger(logger),
	)
	require.NotNil(t, conn)
	t.Cleanup(func() { _ = conn.Close(t.Context()) })

	return conn, p
}

func TestWithLogger_CallLogsRequestSentAndResponseReceived(t *testing.T) {
	t.Parallel()

	spy := &spyLogger{}
	conn, p := getTestConnWithLogger(t, assertNotCalledHandler(t), spy)

	idCh := make(chan any, 1)
	errCh := make(chan error, 1)
	go pipeRespond(t, p, idCh, errCh, nil)

	_, err := conn.Call(t.Context(), "ping", nil)
	require.NoError(t, err)

	select {
	case err := <-errCh:
		require.FailNow(t, err.Error())
	case <-idCh:
	}

	msgs := spy.messages()
	assert.Contains(t, msgs, "request sent")
	assert.Contains(t, msgs, "response received")
}

func TestWithLogger_NotifyLogsNotificationSent(t *testing.T) {
	t.Parallel()

	spy := &spyLogger{}
	conn, p := getTestConnWithLogger(t, assertNotCalledHandler(t), spy)

	notifCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go pipeNotif(t, p, notifCh, errCh)

	err := conn.Notify(t.Context(), "event", nil)
	require.NoError(t, err)

	select {
	case err := <-errCh:
		require.FailNow(t, err.Error())
	case <-notifCh:
	}

	assert.Contains(t, spy.messages(), "notification sent")
}

func TestWithLogger_ServerSideLogsRequestReceivedAndResponseSent(t *testing.T) {
	t.Parallel()

	handlerDone := make(chan struct{})

	handler := jsonrpc2.HandlerFunc(func(ctx context.Context, _ jsonrpc2.Request, reply jsonrpc2.Replier, _ jsonrpc2.Conn) error {
		defer close(handlerDone)
		return reply(ctx, "ok")
	})

	spy := &spyLogger{}
	_, p := getTestConnWithLogger(t, handler, spy)

	_, err := p.Write([]byte(`{"jsonrpc":"2.0","id":"1","method":"doSomething"}`))
	require.NoError(t, err)

	// drain the response
	var raw json.RawMessage
	require.NoError(t, json.NewDecoder(p).Decode(&raw))

	select {
	case <-t.Context().Done():
		require.FailNow(t, "handler did not complete")
	case <-handlerDone:
	}

	msgs := spy.messages()
	assert.Contains(t, msgs, "request received")
	assert.Contains(t, msgs, "response sent")
}

func TestWithLogger_NilLogger_NoPanic(t *testing.T) {
	t.Parallel()

	conn, p := getTestConn(t, assertNotCalledHandler(t))

	idCh := make(chan any, 1)
	errCh := make(chan error, 1)
	go pipeRespond(t, p, idCh, errCh, nil)

	assert.NotPanics(t, func() {
		_, _ = conn.Call(t.Context(), "ping", nil)
	})
}
