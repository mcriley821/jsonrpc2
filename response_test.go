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

func getTestResp(t *testing.T, result any) jsonrpc2.Response {
	t.Helper()

	s, p := newTestStream(t)

	handler := jsonrpc2.HandlerFunc(
		func(_ context.Context, _ jsonrpc2.Request, _ jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			require.FailNow(t, "handler should not be called")

			return nil
		},
	)

	stream := jsonrpc2.NewStream(s)
	require.NotNil(t, stream)

	conn := jsonrpc2.NewConn(t.Context(), stream, jsonrpc2.WithHandler(handler))
	require.NotNil(t, conn)
	t.Cleanup(func() { _ = conn.Close(t.Context()) })

	idChan := make(chan any, 1)
	errChan := make(chan error, 1)

	go pipeRespond(t, p, idChan, errChan, result)

	// send the call
	resp, err := conn.Call(t.Context(), "", nil)
	require.NoError(t, err)

	select {
	case err := <-errChan:
		require.FailNow(t, err.Error())

		return nil
	case id := <-idChan:
		assert.Equal(t, id, resp.ID())

		return resp
	}
}

func pipeRespond(t *testing.T, p net.Conn, idChan chan<- any, errCh chan<- error, result any) {
	t.Helper()

	var req struct {
		ID any `json:"id"`
	}

	if err := json.NewDecoder(p).Decode(&req); err != nil {
		errCh <- err

		return
	}

	idChan <- req.ID

	respObj := struct { //nolint:exhaustruct
		RPC    string `json:"jsonrpc"`
		ID     any    `json:"id"`
		Result any    `json:"result,omitzero"`
		Error  struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    any    `json:"data,omitzero"`
		} `json:"error,omitzero"`
	}{
		RPC: "2.0",
		ID:  req.ID,
	}

	if jerr, ok := result.(jsonrpc2.Error); ok {
		respObj.Error.Code = jerr.Code()
		respObj.Error.Message = jerr.Message()
		respObj.Error.Data = jerr.Data()
	} else {
		respObj.Result = result
	}

	data, err := json.Marshal(&respObj)
	if err != nil {
		errCh <- err

		return
	}

	if _, err = p.Write(data); err != nil {
		errCh <- err

		return
	}
}

func TestResponse_ID(t *testing.T) {
	t.Parallel()

	_ = getTestResp(t, nil)
}

func TestResponse_Result(t *testing.T) {
	t.Parallel()

	resp := getTestResp(t, "testResult")
	require.False(t, resp.Failed())

	var res string

	require.NoError(t, resp.Result(&res))
	assert.Equal(t, "testResult", res)
}

func TestResponse_Error(t *testing.T) {
	t.Parallel()

	jerr := jsonrpc2.NewError(-1, "test message", "test data")
	resp := getTestResp(t, jerr)

	assert.Equal(t, jerr, resp.Error())
	assert.True(t, resp.Failed())
}
