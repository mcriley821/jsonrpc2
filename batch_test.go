package jsonrpc2_test

import (
	"context"
	"encoding/json"
	"net"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/mcriley821/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pipeBatchRespond reads one batch request from p and replies with a batch
// response that echoes each request's params as its result. Notifications
// (items missing an id) do not contribute an entry. Errors go to errCh and
// the list of IDs that appeared in the batch is reported on idsCh.
func pipeBatchRespond(t *testing.T, p net.Conn, idsCh chan<- []any, errCh chan<- error) {
	t.Helper()

	var batch []struct {
		ID     any             `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}

	if err := json.NewDecoder(p).Decode(&batch); err != nil {
		errCh <- err

		return
	}

	ids := make([]any, 0, len(batch))

	type respObj struct {
		RPC    string          `json:"jsonrpc"`
		ID     any             `json:"id"`
		Result json.RawMessage `json:"result"`
	}

	resps := make([]respObj, 0, len(batch))

	for _, item := range batch {
		if item.ID == nil {
			continue // notification; no response entry
		}

		ids = append(ids, item.ID)
		resps = append(resps, respObj{RPC: "2.0", ID: item.ID, Result: item.Params})
	}

	idsCh <- ids

	if len(resps) == 0 {
		return
	}

	data, err := json.Marshal(resps)
	if err != nil {
		errCh <- err

		return
	}

	if _, err := p.Write(data); err != nil {
		errCh <- err

		return
	}
}

func TestConn_Batch_Empty(t *testing.T) {
	t.Parallel()

	conn, _ := getTestConn(t, assertNotCalledHandler(t))

	resp, err := conn.Batch(t.Context(), nil)
	require.Error(t, err)
	assert.Nil(t, resp)
}

func TestConn_Batch_BadParams(t *testing.T) {
	t.Parallel()

	conn, _ := getTestConn(t, assertNotCalledHandler(t))

	resp, err := conn.Batch(t.Context(), []jsonrpc2.BatchItem{
		{Method: "broken", Params: func() {}, Notification: false},
	})
	require.Error(t, err)
	assert.Nil(t, resp)
}

func TestConn_Batch_CanceledContext(t *testing.T) {
	t.Parallel()

	conn, _ := getTestConn(t, assertNotCalledHandler(t))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	resp, err := conn.Batch(ctx, []jsonrpc2.BatchItem{{Method: "m", Params: nil, Notification: false}})
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, resp)
}

func TestConn_Batch_ClosedConn(t *testing.T) {
	t.Parallel()

	conn, _ := getTestConn(t, assertNotCalledHandler(t))

	require.NoError(t, conn.Close(t.Context()))

	resp, err := conn.Batch(t.Context(), []jsonrpc2.BatchItem{{Method: "m", Params: nil, Notification: false}})
	require.ErrorIs(t, err, jsonrpc2.ErrClosed)
	assert.Nil(t, resp)
}

func TestConn_Batch_AllNotifications(t *testing.T) {
	t.Parallel()

	conn, p := getTestConn(t, assertNotCalledHandler(t))

	idsCh := make(chan []any, 1)
	errCh := make(chan error, 1)

	go pipeBatchRespond(t, p, idsCh, errCh)

	resp, err := conn.Batch(t.Context(), []jsonrpc2.BatchItem{
		{Method: "m1", Params: nil, Notification: true},
		{Method: "m2", Params: nil, Notification: true},
	})
	require.NoError(t, err)
	assert.Nil(t, resp)

	select {
	case err := <-errCh:
		require.FailNow(t, err.Error())
	case ids := <-idsCh:
		assert.Empty(t, ids)
	}
}

func TestConn_Batch_Mixed(t *testing.T) {
	t.Parallel()

	conn, p := getTestConn(t, assertNotCalledHandler(t))

	idsCh := make(chan []any, 1)
	errCh := make(chan error, 1)

	go pipeBatchRespond(t, p, idsCh, errCh)

	items := []jsonrpc2.BatchItem{
		{Method: "a", Params: 1, Notification: false},
		{Method: "log", Params: "hi", Notification: true},
		{Method: "b", Params: 2, Notification: false},
		{Method: "c", Params: 3, Notification: false},
	}

	resp, err := conn.Batch(t.Context(), items)
	require.NoError(t, err)
	require.Len(t, resp, 3, "expected 3 responses (one per non-notification)")

	// Responses must arrive in input order of non-notification items.
	wants := []int{1, 2, 3}

	for i, r := range resp {
		require.False(t, r.Failed())

		var got int

		require.NoError(t, r.Result(&got))
		assert.Equal(t, wants[i], got)
	}

	select {
	case err := <-errCh:
		require.FailNow(t, err.Error())
	case ids := <-idsCh:
		// The batch must have contained exactly 3 IDs (one per non-notification).
		assert.Len(t, ids, 3)
	}
}

// TestConn_Batch_WireFormat asserts the on-wire framing of a batch request:
// a single JSON array containing one object per item, with notifications
// omitting the id field.
func TestConn_Batch_WireFormat(t *testing.T) {
	t.Parallel()

	conn, p := getTestConn(t, assertNotCalledHandler(t))

	rawCh := make(chan json.RawMessage, 1)
	errCh := make(chan error, 1)

	go func() {
		var raw json.RawMessage

		if err := json.NewDecoder(p).Decode(&raw); err != nil {
			errCh <- err

			return
		}

		rawCh <- raw
	}()

	// Fire Batch in the background because it blocks waiting for replies.
	go func() {
		_, _ = conn.Batch(t.Context(), []jsonrpc2.BatchItem{
			{Method: "one", Params: nil, Notification: false},
			{Method: "two", Params: nil, Notification: true},
		})
	}()

	select {
	case <-time.After(time.Second):
		require.FailNow(t, "timeout waiting for batch on wire")
	case err := <-errCh:
		require.FailNow(t, err.Error())
	case raw := <-rawCh:
		var items []map[string]any
		require.NoError(t, json.Unmarshal(raw, &items))
		require.Len(t, items, 2)

		assert.Equal(t, "one", items[0]["method"])
		assert.Contains(t, items[0], "id")

		assert.Equal(t, "two", items[1]["method"])
		assert.NotContains(t, items[1], "id", "notifications must omit id")
	}
}

func TestConn_HandleBatch_OK(t *testing.T) {
	t.Parallel()

	handler := jsonrpc2.HandlerFunc(
		func(ctx context.Context, req jsonrpc2.Request, reply jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			// Echo params as the result.
			var v any

			if raw := req.Params(); raw != nil {
				_ = json.Unmarshal(raw, &v)
			}

			return reply(ctx, v)
		},
	)

	_, p := getTestConn(t, handler)

	// Send a batch with 3 requests and 1 notification.
	batch := []byte(`[
		{"jsonrpc":"2.0","id":"a","method":"echo","params":1},
		{"jsonrpc":"2.0","method":"note","params":"ignore"},
		{"jsonrpc":"2.0","id":"b","method":"echo","params":2},
		{"jsonrpc":"2.0","id":"c","method":"echo","params":3}
	]`)
	_, err := p.Write(batch)
	require.NoError(t, err)

	var resps []struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
	}

	require.NoError(t, p.SetReadDeadline(time.Now().Add(2*time.Second)))
	require.NoError(t, json.NewDecoder(p).Decode(&resps))
	require.Len(t, resps, 3, "expected 3 responses (notification produces none)")

	sort.Slice(resps, func(i, j int) bool { return resps[i].ID < resps[j].ID })

	expected := map[string]string{"a": "1", "b": "2", "c": "3"}

	for _, r := range resps {
		assert.Equal(t, expected[r.ID], string(r.Result))
	}
}

func TestConn_HandleBatch_AllNotifications_NoResponse(t *testing.T) {
	t.Parallel()

	called := make(chan struct{}, 2)

	handler := jsonrpc2.HandlerFunc(
		func(_ context.Context, req jsonrpc2.Request, _ jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			assert.Nil(t, req.ID())

			called <- struct{}{}

			return nil
		},
	)

	_, p := getTestConn(t, handler)

	batch := []byte(`[
		{"jsonrpc":"2.0","method":"a"},
		{"jsonrpc":"2.0","method":"b"}
	]`)
	_, err := p.Write(batch)
	require.NoError(t, err)

	// Both notifications must be delivered.
	for range 2 {
		select {
		case <-time.After(time.Second):
			require.FailNow(t, "notification handler not called")
		case <-called:
		}
	}

	// No batch response must appear on the wire.
	require.NoError(t, p.SetReadDeadline(time.Now().Add(100*time.Millisecond)))

	buf := make([]byte, 1)
	n, err := p.Read(buf)
	assert.Zero(t, n)
	require.Error(t, err)
}

func TestConn_HandleBatch_Empty(t *testing.T) {
	t.Parallel()

	_, p := getTestConn(t, assertNotCalledHandler(t))

	_, err := p.Write([]byte(`[]`))
	require.NoError(t, err)

	var resp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	require.NoError(t, p.SetReadDeadline(time.Now().Add(2*time.Second)))
	require.NoError(t, json.NewDecoder(p).Decode(&resp))

	assert.Equal(t, "2.0", resp.JSONRPC)
	assert.Nil(t, resp.ID)
	assert.Equal(t, jsonrpc2.InvalidRequest, resp.Error.Code)
}

func TestConn_HandleBatch_InvalidItems(t *testing.T) {
	t.Parallel()

	handler := jsonrpc2.HandlerFunc(
		func(ctx context.Context, _ jsonrpc2.Request, reply jsonrpc2.Replier, _ jsonrpc2.Conn) error {
			return reply(ctx, "ok")
		},
	)

	_, p := getTestConn(t, handler)

	// First item valid, second item invalid (method has wrong type), third valid.
	batch := []byte(`[
		{"jsonrpc":"2.0","id":"a","method":"m"},
		{"jsonrpc":"2.0","id":"b","method":123},
		{"jsonrpc":"2.0","id":"c","method":"m"}
	]`)
	_, err := p.Write(batch)
	require.NoError(t, err)

	var resps []struct {
		ID    any             `json:"id"`
		Error json.RawMessage `json:"error"`
	}

	require.NoError(t, p.SetReadDeadline(time.Now().Add(2*time.Second)))
	require.NoError(t, json.NewDecoder(p).Decode(&resps))
	require.Len(t, resps, 3)

	// Exactly one invalid-request error must appear; connection must stay open.
	errCount := 0

	for _, r := range resps {
		if len(r.Error) > 0 {
			errCount++
		}
	}

	assert.Equal(t, 1, errCount)
}

// peerEchoBatches reads n batch requests from p and writes back an echoing
// batch response for each.
func peerEchoBatches(p net.Conn, n int, errCh chan<- error) {
	dec := json.NewDecoder(p)

	type respObj struct {
		RPC    string          `json:"jsonrpc"`
		ID     any             `json:"id"`
		Result json.RawMessage `json:"result"`
	}

	for range n {
		var batch []struct {
			ID     any             `json:"id"`
			Params json.RawMessage `json:"params"`
		}

		if err := dec.Decode(&batch); err != nil {
			errCh <- err

			return
		}

		resps := make([]respObj, 0, len(batch))
		for _, item := range batch {
			resps = append(resps, respObj{RPC: "2.0", ID: item.ID, Result: item.Params})
		}

		data, err := json.Marshal(resps)
		if err != nil {
			errCh <- err

			return
		}

		if _, err := p.Write(data); err != nil {
			errCh <- err

			return
		}
	}
}

// TestConn_Batch_Concurrent verifies that two concurrent Batch calls on the
// same connection do not cross-route responses.
func TestConn_Batch_Concurrent(t *testing.T) {
	t.Parallel()

	conn, p := getTestConn(t, assertNotCalledHandler(t))

	peerErr := make(chan error, 1)
	go peerEchoBatches(p, 2, peerErr)

	var (
		wg      sync.WaitGroup
		results [2][]jsonrpc2.Response
		errs    [2]error
	)

	wg.Add(2)

	for i := range 2 {
		go func(i int) {
			defer wg.Done()

			results[i], errs[i] = conn.Batch(t.Context(), []jsonrpc2.BatchItem{
				{Method: "m", Params: i*10 + 1, Notification: false},
				{Method: "m", Params: i*10 + 2, Notification: false},
			})
		}(i)
	}

	wg.Wait()

	select {
	case err := <-peerErr:
		require.FailNow(t, err.Error())
	default:
	}

	for i := range 2 {
		require.NoError(t, errs[i])
		require.Len(t, results[i], 2)

		for j, r := range results[i] {
			var got int

			require.NoError(t, r.Result(&got))
			assert.Equal(t, i*10+j+1, got)
		}
	}
}
