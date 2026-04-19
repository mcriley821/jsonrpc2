package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
)

// Conn is a full-duplex connection over a [Stream].
// All methods are safe for concurrent use.
type Conn interface {
	// Call sends a request and waits for a response from the peer.
	// It blocks until the response arrives, ctx is cancelled, or the connection closes.
	// Returns an error if the connection is closed, ctx expires, or if marshaling the request fails.
	Call(ctx context.Context, method string, params any) (Response, error)

	// Notify sends a request without expecting a response.
	// Returns an error if the connection is closed, ctx expires, or if marshaling the request fails.
	Notify(ctx context.Context, method string, params any) error

	// Batch sends items as a single batch and returns one [Response] per
	// non-notification item, in input order. Returns nil responses if every
	// item is a notification.
	Batch(ctx context.Context, items ...BatchItem) ([]Response, error)

	// Close gracefully shuts down the connection and waits for shutdown to complete.
	// Safe to call multiple times. Returns an error if ctx expires before shutdown finishes.
	Close(ctx context.Context) error

	// Done returns a channel that closes when the connection has fully shut down.
	// Use [Conn.Err] to retrieve the terminal error.
	Done() <-chan struct{}

	// Err returns the terminal error, or nil if the connection is still running.
	// Check [Conn.Done] first; Err is only valid after [Conn.Done] closes.
	Err() error
}

// BatchItem is a single entry in an outbound batch sent via [Conn.Batch].
// Construct one with [BatchCall] or [BatchNotification].
type BatchItem struct {
	method       string
	params       any
	notification bool
}

// BatchCall returns a [BatchItem] that, when sent via [Conn.Batch], behaves
// like [Conn.Call]: it produces a response. Pass nil for params to omit the field.
func BatchCall(method string, params any) BatchItem {
	return BatchItem{method: method, params: params, notification: false}
}

// BatchNotification returns a [BatchItem] that, when sent via [Conn.Batch],
// behaves like [Conn.Notify]: no response is produced. Pass nil for params to
// omit the field.
func BatchNotification(method string, params any) BatchItem {
	return BatchItem{method: method, params: params, notification: true}
}

// conn is an implementation of [Conn].
type conn struct {
	cancel context.CancelFunc

	stream  Stream
	handler Handler

	outgoing chan any
	done     chan struct{}

	shutdownOnce   sync.Once
	termErr        error
	streamCloseErr error

	wg sync.WaitGroup

	inflight map[string]chan *response

	closed bool

	// mu protects access to inflight and closed.
	mu sync.Mutex
}

var _ Conn = (*conn)(nil)

// NewConn creates and starts a new [Conn] over stream that uses handler to dispatch
// incoming requests. The connection runs until the peer closes, the handler returns
// an error, or ctx is cancelled. Use [Conn.Done] to wait for shutdown.
func NewConn(ctx context.Context, stream Stream, handler Handler, opts ...Option) Conn {
	ctx, cancel := context.WithCancel(ctx)

	o := defaultConnOptions()
	for _, opt := range opts {
		opt(&o)
	}

	c := &conn{
		cancel:         cancel,
		stream:         stream,
		handler:        handler,
		outgoing:       make(chan any),
		done:           make(chan struct{}),
		shutdownOnce:   sync.Once{},
		termErr:        nil,
		streamCloseErr: nil,
		wg:             sync.WaitGroup{},
		inflight:       make(map[string]chan *response),
		closed:         false,
		mu:             sync.Mutex{},
	}

	go c.run(ctx)

	return c
}

// Call sends a request and waits for a response. Pass nil for params to omit the field.
func (c *conn) Call(ctx context.Context, method string, params any) (Response, error) {
	id := uuid.NewString()

	req, err := newRequest(id, method, params)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	respCh := make(chan *response, 1)

	if err := c.registerRequests(map[string]chan *response{id: respCh}); err != nil {
		return nil, err
	}

	defer c.unregisterRequests([]string{id})

	select {
	case <-ctx.Done():
		return nil, ctx.Err() //nolint:wrapcheck
	case <-c.done:
		return nil, c.termErr
	case c.outgoing <- req:
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err() //nolint:wrapcheck
	case <-c.done:
		return nil, c.termErr
	case resp := <-respCh:
		return resp, nil
	}
}

// Notify sends a notification. Pass nil for params to omit the field.
func (c *conn) Notify(ctx context.Context, method string, params any) error {
	select {
	case <-c.done:
		return c.termErr
	default:
	}

	req, err := newRequest(nil, method, params)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck
	case <-c.done:
		return c.termErr
	case c.outgoing <- req:
		return nil
	}
}

// Batch sends items as a single batch. See [Conn.Batch] for semantics.
func (c *conn) Batch(ctx context.Context, items ...BatchItem) ([]Response, error) {
	if len(items) == 0 {
		return nil, errors.New("empty batch")
	}

	reqs, pending, order, err := buildBatchRequests(items)
	if err != nil {
		return nil, err
	}

	if err := c.registerRequests(pending); err != nil {
		return nil, err
	}

	defer c.unregisterRequests(order)

	select {
	case <-ctx.Done():
		return nil, ctx.Err() //nolint:wrapcheck
	case <-c.done:
		return nil, c.termErr
	case c.outgoing <- reqs:
	}

	if len(order) == 0 {
		return nil, nil
	}

	return c.awaitBatchResponses(ctx, order, pending)
}

// Close gracefully shuts down the connection. It waits for all goroutines to exit
// and returns any error from closing the underlying stream, or ctx.Err() if ctx expires first.
func (c *conn) Close(ctx context.Context) error {
	c.shutdown(ErrClosed)

	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck
	case <-c.done:
		return c.streamCloseErr
	}
}

// Done returns a channel that closes when the connection has fully shut down.
// Use [Conn.Err] to retrieve the terminal error.
func (c *conn) Done() <-chan struct{} {
	return c.done
}

// Err returns the terminal error, or nil if the connection is still running.
// Check [Conn.Done] first; Err is only valid after [Conn.Done] closes.
func (c *conn) Err() error {
	select {
	case <-c.done:
		return c.termErr
	default:
		return nil
	}
}

// buildBatchRequests converts items into a slice of wire requests, assigning
// unique IDs to non-notification entries and returning the registration map
// and response-collection order.
func buildBatchRequests(items []BatchItem) ([]*request, map[string]chan *response, []string, error) {
	reqs := make([]*request, len(items))
	pending := make(map[string]chan *response)
	order := make([]string, 0, len(items))

	for i, item := range items {
		var id any

		if !item.notification {
			idStr := uuid.NewString()
			id = idStr
			ch := make(chan *response, 1)
			pending[idStr] = ch
			order = append(order, idStr)
		}

		req, err := newRequest(id, item.method, item.params)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("creating batch item %d: %w", i, err)
		}

		reqs[i] = req
	}

	return reqs, pending, order, nil
}

// awaitBatchResponses waits for one response per ID in order, in the order
// those IDs were assigned.
func (c *conn) awaitBatchResponses(
	ctx context.Context, order []string, pending map[string]chan *response,
) ([]Response, error) {
	results := make([]Response, 0, len(order))

	for _, id := range order {
		select {
		case <-ctx.Done():
			return nil, ctx.Err() //nolint:wrapcheck
		case <-c.done:
			return nil, c.termErr
		case resp := <-pending[id]:
			results = append(results, resp)
		}
	}

	return results, nil
}

// registerRequests registers each (id, ch) pair in the inflight map atomically.
// It holds mu for the duration so that the closed check and the map insertion
// are a single critical section, eliminating the TOCTOU window against shutdown.
func (c *conn) registerRequests(pending map[string]chan *response) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return c.termErr
	}

	maps.Copy(c.inflight, pending)

	return nil
}

// unregisterRequests removes ids from the inflight map.
func (c *conn) unregisterRequests(ids []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, id := range ids {
		delete(c.inflight, id)
	}
}

// shutdown initiates connection shutdown. The first error recorded becomes
// the terminal error. It is safe to call concurrently.
func (c *conn) shutdown(err error) {
	c.shutdownOnce.Do(func() {
		c.termErr = err
		c.cancel()
		c.streamCloseErr = c.stream.Close()

		c.mu.Lock()
		c.closed = true

		for id := range c.inflight {
			delete(c.inflight, id)
		}

		c.mu.Unlock()
	})
}

// run manages the connection lifecycle. It exits when ctx is cancelled, a read error occurs,
// or a write error occurs, then waits for all handler goroutines to finish before closing done.
func (c *conn) run(ctx context.Context) {
	defer func() {
		c.wg.Wait()
		close(c.done)
	}()

	readDone := make(chan error, 1)
	writeDone := make(chan error, 1)

	//nolint:mnd // read and write goroutines
	c.wg.Add(2)

	go c.read(ctx, readDone)
	go c.write(ctx, writeDone)

	select {
	case <-ctx.Done():
		c.shutdown(ctx.Err())
	case err := <-readDone:
		c.shutdown(fmt.Errorf("read: %w", err))
	case err := <-writeDone:
		c.shutdown(fmt.Errorf("write: %w", err))
	}
}

// partialMessage classifies an incoming message without full deserialization.
// Method distinguishes requests from responses.
type partialMessage struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
}

// isBatch reports whether raw is a JSON array (i.e. a batch message) by
// checking the first non-whitespace byte.
func isBatch(raw json.RawMessage) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '[':
			return true
		default:
			return false
		}
	}

	return false
}

// read dispatches incoming messages until ctx is cancelled or an error occurs.
func (c *conn) read(ctx context.Context, errChan chan<- error) {
	defer c.wg.Done()

	for {
		select {
		case <-ctx.Done():
			errChan <- ctx.Err()

			return

		default:
			var raw json.RawMessage
			if err := c.stream.Read(&raw); err != nil {
				errChan <- fmt.Errorf("stream read: %w", err)

				return
			}

			if isBatch(raw) {
				c.wg.Add(1)

				go c.handleBatch(ctx, raw)

				continue
			}

			if err := c.dispatchMessage(ctx, raw); err != nil {
				errChan <- err

				return
			}
		}
	}
}

// parseMessage classifies raw as either a request or response message.
// Exactly one of req/resp is non-nil on success; a non-nil error indicates
// malformed input.
func parseMessage(raw json.RawMessage) (*request, *response, error) {
	var v partialMessage
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, nil, fmt.Errorf("message unmarshal: %w", err)
	}

	if v.JSONRPC != "2.0" {
		return nil, nil, fmt.Errorf(`unsupported jsonrpc ("2.0" != %s)`, v.JSONRPC)
	}

	if v.Method == "" {
		resp := new(response)
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, nil, fmt.Errorf("response unmarshal: %w", err)
		}

		return nil, resp, nil
	}

	req := new(request)
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, nil, fmt.Errorf("request unmarshal: %w", err)
	}

	return req, nil, nil
}

// dispatchMessage classifies a single raw message and schedules a goroutine
// to handle it. Returns a non-nil error only for malformed input.
func (c *conn) dispatchMessage(ctx context.Context, raw json.RawMessage) error {
	req, resp, err := parseMessage(raw)
	if err != nil {
		return err
	}

	c.wg.Add(1)

	if resp != nil {
		go c.handleResponse(ctx, resp)
	} else {
		go c.handleRequest(ctx, req)
	}

	return nil
}

// handleBatch processes an incoming batch message. Each request in the batch
// is dispatched to the handler concurrently; the collected replies are sent
// as a single outbound batch. Responses inside the batch route through the
// inflight map, exactly as single responses do. An empty batch or a parse
// failure on the batch envelope produces a single InvalidRequest reply (the
// connection stays open).
func (c *conn) handleBatch(ctx context.Context, raw json.RawMessage) {
	defer c.wg.Done()

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		c.sendMessage(ctx, newErrorResponse(nil, NewError(InvalidRequest, err.Error(), nil)))

		return
	}

	if len(items) == 0 {
		c.sendMessage(ctx, newErrorResponse(nil, NewError(InvalidRequest, "empty batch", nil)))

		return
	}

	responses := c.collectBatchResponses(ctx, items)
	if len(responses) == 0 {
		return
	}

	c.sendMessage(ctx, responses)
}

// collectBatchResponses dispatches each item concurrently and returns the
// gathered responses. Items that fail to parse contribute an InvalidRequest
// entry; incoming responses are routed via the inflight map and contribute
// nothing to the returned slice.
func (c *conn) collectBatchResponses(ctx context.Context, items []json.RawMessage) []*response {
	// Buffered to len(items) so sends never block; drained after inner.Wait.
	sink := make(chan *response, len(items))

	var inner sync.WaitGroup

	for _, item := range items {
		req, resp, err := parseMessage(item)
		if err != nil {
			sink <- newErrorResponse(nil, NewError(InvalidRequest, err.Error(), nil))

			continue
		}

		if resp != nil {
			c.wg.Add(1)

			go c.handleResponse(ctx, resp)

			continue
		}

		reply := c.makeReplier(req.ID(), func(_ context.Context, r *response) error {
			sink <- r

			return nil
		})

		inner.Go(func() {
			if err := c.handler.ServeRPC(ctx, req, reply, c); err != nil {
				c.shutdown(fmt.Errorf("handler error: %w", err))
			}
		})
	}

	inner.Wait()
	close(sink)

	responses := make([]*response, 0, len(items))
	for r := range sink {
		responses = append(responses, r)
	}

	return responses
}

// sendMessage writes msg to outgoing, returning early on cancellation or shutdown.
func (c *conn) sendMessage(ctx context.Context, msg any) {
	select {
	case <-ctx.Done():
	case <-c.done:
	case c.outgoing <- msg:
	}
}

// write sends outgoing messages until ctx is cancelled or an error occurs.
func (c *conn) write(ctx context.Context, errChan chan<- error) {
	defer c.wg.Done()

	for {
		select {
		case <-ctx.Done():
			errChan <- ctx.Err()

			return
		case msg := <-c.outgoing:
			if err := c.stream.Write(msg); err != nil {
				errChan <- fmt.Errorf("stream write: %w", err)

				return
			}
		}
	}
}

// handleRequest invokes the handler for the incoming request.
// If the handler returns an error, the connection is closed.
func (c *conn) handleRequest(ctx context.Context, req *request) {
	defer c.wg.Done()

	if err := c.handler.ServeRPC(ctx, req, c.replier(req.ID()), c); err != nil {
		c.shutdown(fmt.Errorf("handler error: %w", err))
	}
}

// handleResponse routes an incoming response to the waiting [Conn.Call] goroutine.
// Unknown IDs and non-string IDs are silently dropped.
func (c *conn) handleResponse(ctx context.Context, resp *response) {
	defer c.wg.Done()

	id, ok := resp.ID().(string)
	if !ok {
		return
	}

	// Delete under the write lock so only one goroutine can claim the channel.
	// A duplicate response arriving concurrently will find the entry already
	// gone and return without sending. Call's deferred delete becomes a no-op.
	c.mu.Lock()
	ch, ok := c.inflight[id]
	delete(c.inflight, id)
	c.mu.Unlock()

	if ok {
		select {
		case <-ctx.Done():
			return
		case ch <- resp:
		}
	}
}

// makeReplier builds a [Replier] that constructs the response and forwards it
// to sink. Notifications (id == nil) produce a no-op replier. The returned
// Replier returns [ErrReplied] on any call after the first.
func (c *conn) makeReplier(id any, sink func(ctx context.Context, r *response) error) Replier {
	if id == nil {
		return func(context.Context, any) error { return nil }
	}

	var replied atomic.Bool

	return func(ctx context.Context, result any) error {
		if replied.Swap(true) {
			return ErrReplied
		}

		var resp *response

		if jerr, ok := result.(Error); ok {
			resp = newErrorResponse(id, jerr)
		} else if data, err := json.Marshal(&result); err != nil {
			return fmt.Errorf("marshalling result: %w", err)
		} else {
			resp = newResponse(id, data)
		}

		return sink(ctx, resp)
	}
}

// replier returns a [Replier] that writes the response to the outgoing queue.
// Notifications (id == nil) return a no-op.
// The returned Replier returns [ErrReplied] on any call after the first.
func (c *conn) replier(id any) Replier {
	return c.makeReplier(id, c.sendToOutgoing)
}

// sendToOutgoing delivers r to the write loop or aborts on ctx/shutdown.
func (c *conn) sendToOutgoing(ctx context.Context, r *response) error {
	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck
	case <-c.done:
		return ErrClosed
	case c.outgoing <- r:
		return nil
	}
}
