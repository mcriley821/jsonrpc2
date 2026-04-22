package jsonrpc2

import (
	"bytes"
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

// NewConn creates and starts a new [Conn] over stream. The connection runs until
// the peer closes, a registered handler returns an error, or ctx is cancelled.
// Use [WithHandler] to dispatch incoming requests; without it, requests receive
// a [MethodNotFound] error response and notifications are silently ignored.
// Use [Conn.Done] to wait for shutdown.
func NewConn(ctx context.Context, stream Stream, opts ...Option) Conn {
	//nolint:gosec,nolintlint // G118: cancel stored in conn struct, called during shutdown
	ctx, cancel := context.WithCancel(ctx)

	o := defaultConnOptions()
	for _, opt := range opts {
		opt(&o)
	}

	c := &conn{
		cancel:         cancel,
		stream:         stream,
		handler:        o.handler,
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

	defer func() {
		c.mu.Lock()
		delete(c.inflight, id)
		c.mu.Unlock()
	}()

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

// registerRequests registers requests in the inflight map. It holds mu for
// the duration so that the closed check and the map insertions are a single
// critical section, eliminating the TOCTOU window against shutdown.
func (c *conn) registerRequests(reqs map[string]chan *response) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return c.termErr
	}

	maps.Copy(c.inflight, reqs)

	return nil
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

// classifyRaw detects whether raw is a batch (JSON array) and partially
// parses each element to extract jsonrpc and method fields.
func classifyRaw(raw json.RawMessage) ([]partialMessage, bool, error) {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	isBatch := len(trimmed) > 0 && trimmed[0] == '['

	var messages []partialMessage

	if isBatch {
		if err := json.Unmarshal(raw, &messages); err != nil {
			return nil, false, fmt.Errorf("message unmarshal: %w", err)
		}
	} else {
		var m partialMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, false, fmt.Errorf("message unmarshal: %w", err)
		}

		messages = []partialMessage{m}
	}

	return messages, isBatch, nil
}

// validateMessages checks that every message has jsonrpc "2.0" and that a
// batch does not mix requests and responses.
func validateMessages(messages []partialMessage, isBatch bool) error {
	isResponse := messages[0].Method == ""

	for _, m := range messages {
		if m.JSONRPC != "2.0" {
			return fmt.Errorf(`unsupported jsonrpc ("2.0" != %s)`, m.JSONRPC)
		}

		if !isBatch {
			continue
		}

		if isResponse && m.Method != "" {
			return errors.New("invalid batch: request in response batch")
		}

		if !isResponse && m.Method == "" {
			return errors.New("invalid batch: response in request batch")
		}
	}

	return nil
}

// dispatchResponses unmarshals raw into responses and launches handleResponses.
func (c *conn) dispatchResponses(ctx context.Context, raw json.RawMessage, isBatch bool) error {
	var responses []*response

	if isBatch {
		if err := json.Unmarshal(raw, &responses); err != nil {
			return fmt.Errorf("response unmarshal: %w", err)
		}
	} else {
		r := new(response)
		if err := json.Unmarshal(raw, r); err != nil {
			return fmt.Errorf("response unmarshal: %w", err)
		}

		responses = []*response{r}
	}

	c.wg.Add(1)

	go c.handleResponses(ctx, responses)

	return nil
}

// dispatchRequests unmarshals raw into requests and launches handleRequests.
func (c *conn) dispatchRequests(ctx context.Context, raw json.RawMessage, isBatch bool) error {
	var requests []*request

	if isBatch {
		if err := json.Unmarshal(raw, &requests); err != nil {
			return fmt.Errorf("request unmarshal: %w", err)
		}
	} else {
		r := new(request)
		if err := json.Unmarshal(raw, r); err != nil {
			return fmt.Errorf("request unmarshal: %w", err)
		}

		requests = []*request{r}
	}

	c.wg.Add(1)

	go c.handleRequests(ctx, requests, isBatch)

	return nil
}

// dispatchRaw classifies, validates, and dispatches a single raw message.
func (c *conn) dispatchRaw(ctx context.Context, raw json.RawMessage) error {
	messages, isBatch, err := classifyRaw(raw)
	if err != nil {
		return err
	}

	if len(messages) == 0 {
		return nil
	}

	if err := validateMessages(messages, isBatch); err != nil {
		return err
	}

	if messages[0].Method == "" {
		return c.dispatchResponses(ctx, raw, isBatch)
	}

	return c.dispatchRequests(ctx, raw, isBatch)
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

			if err := c.dispatchRaw(ctx, raw); err != nil {
				errChan <- err

				return
			}
		}
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

// handleRequests invokes the handler for the incoming requests.
// If the handler returns an error, the connection is closed.
func (c *conn) handleRequests(ctx context.Context, requests []*request, isBatch bool) {
	defer c.wg.Done()

	sink := make(chan any, len(requests))

	var wg sync.WaitGroup

	for _, req := range requests {
		wg.Go(func() {
			if err := c.handler.ServeRPC(ctx, req, c.replier(req.ID(), sink), c); err != nil {
				c.shutdown(fmt.Errorf("handler error: %w", err))
			}
		})
	}

	wg.Wait()
	close(sink)

	out := make([]any, 0, len(requests))
	for resp := range sink {
		out = append(out, resp)
	}

	if len(out) == 0 {
		return
	}

	var msg any
	if isBatch {
		msg = out
	} else {
		msg = out[0]
	}

	select {
	case <-c.Done():
	case c.outgoing <- msg:
	}
}

// handleResponses routes an incoming responses to the waiting [Conn.Call]
// or [Conn.Batch] goroutine.
// Unknown IDs and non-string IDs are silently dropped.
func (c *conn) handleResponses(ctx context.Context, responses []*response) {
	defer c.wg.Done()

	for _, resp := range responses {
		id, ok := resp.ID().(string)
		if !ok {
			continue
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
}

// replier returns a [Replier] bound to the request ID.
// Notifications (id == nil) return a no-op.
// The returned Replier returns [ErrReplied] on any call after the first.
func (c *conn) replier(id any, sink chan<- any) Replier {
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

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			return ErrClosed
		case sink <- resp:
			return nil
		}
	}
}
