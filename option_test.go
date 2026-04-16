package jsonrpc2_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"testing"

	"github.com/mcriley821/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithIDGenerator(t *testing.T) {
	t.Parallel()

	counter := 0
	gen := func() string {
		counter++
		return fmt.Sprintf("id-%d", counter)
	}

	s, p := newTestStream(t)
	stream := jsonrpc2.NewStream(s)
	conn := jsonrpc2.NewConn(t.Context(), stream, assertNotCalledHandler(t), jsonrpc2.WithIDGenerator(gen))
	t.Cleanup(func() { _ = conn.Close(t.Context()) })

	idCh := make(chan any, 1)
	errCh := make(chan error, 1)

	go pipeRespond(t, p, idCh, errCh, nil)

	resp, err := conn.Call(t.Context(), "method", nil)
	require.NoError(t, err)
	require.NotNil(t, resp)

	select {
	case err := <-errCh:
		require.FailNow(t, err.Error())
	case id := <-idCh:
		assert.Equal(t, "id-1", id)
		assert.Equal(t, id, resp.ID())
	}
}

func TestWithLogger(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	s, p := newTestStream(t)
	stream := jsonrpc2.NewStream(s)
	conn := jsonrpc2.NewConn(t.Context(), stream, assertNotCalledHandler(t), jsonrpc2.WithLogger(logger))

	idCh := make(chan any, 1)
	errCh := make(chan error, 1)

	go pipeRespond(t, p, idCh, errCh, nil)

	_, err := conn.Call(t.Context(), "method", nil)
	require.NoError(t, err)

	select {
	case err := <-errCh:
		require.FailNow(t, err.Error())
	case <-idCh:
	}

	require.NoError(t, conn.Close(t.Context()))
	<-conn.Done()

	output := buf.String()
	assert.Contains(t, output, "call sent")
	assert.Contains(t, output, "response received")
	assert.Contains(t, output, "connection shutdown")
}

func TestWithLogger_Nil_NoLogs(t *testing.T) {
	t.Parallel()

	s, p := newTestStream(t)
	stream := jsonrpc2.NewStream(s)
	// nil logger should not panic
	conn := jsonrpc2.NewConn(t.Context(), stream, assertNotCalledHandler(t), jsonrpc2.WithLogger(nil))
	t.Cleanup(func() { _ = conn.Close(t.Context()) })

	idCh := make(chan any, 1)
	errCh := make(chan error, 1)

	go pipeRespond(t, p, idCh, errCh, nil)

	_, err := conn.Call(t.Context(), "method", nil)
	require.NoError(t, err)

	select {
	case err := <-errCh:
		require.FailNow(t, err.Error())
	case <-idCh:
	}
}
