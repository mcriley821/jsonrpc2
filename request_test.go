package jsonrpc2_test

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	"github.com/mcriley821/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getTestReq(t *testing.T) (net.Conn, <-chan jsonrpc2.Request) {
	t.Helper()

	reqCh := make(chan jsonrpc2.Request, 1)

	handler := jsonrpc2.HandlerFunc(
		func(_ context.Context, req jsonrpc2.Request, _ jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			reqCh <- req

			return nil
		},
	)

	s, p := newTestStream(t)
	stream := jsonrpc2.NewStream(s)
	require.NotNil(t, stream)

	conn := jsonrpc2.NewConn(t.Context(), stream, handler)
	require.NotNil(t, conn)
	t.Cleanup(func() { _ = conn.Close(t.Context()) })

	return p, reqCh
}

func TestRequest(t *testing.T) {
	t.Parallel()

	p, reqCh := getTestReq(t)

	go func() {
		_, err := p.Write([]byte(`{"jsonrpc":"2.0","id":"testID","method":"testMethod","params":{"key":"value"}}`))
		assert.NoError(t, err)
	}()

	select {
	case req := <-reqCh:
		assert.Equal(t, "testID", req.ID())
		assert.Equal(t, "testMethod", req.Method())

		var params map[string]string

		require.NoError(t, json.Unmarshal(req.Params(), &params))
		assert.Equal(t, map[string]string{"key": "value"}, params)

	case <-t.Context().Done():
		require.FailNow(t, "context deadline exceeded")
	}
}
