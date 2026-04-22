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
	//nolint:gosec // G118: cancel stored in conn struct, called during shutdown
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

			var (
				messages []partialMessage
				err      error
				isBatch  = false
			)

			if len(raw) > 0 && raw[0] == '[' {
				isBatch = true
				err = json.Unmarshal(raw, &messages)
			} else {
				messages = append(messages, partialMessage{})
				err = json.Unmarshal(raw, &messages[0])
			}

			if err != nil {
				errChan <- fmt.Errorf("message unmarshal: %w", err)

				return
			}

			if len(messages) == 0 {
				continue
			}

			isResponse := messages[0].Method == ""

			for _, message := range messages {
				if message.JSONRPC != "2.0" {
					errChan <- fmt.Errorf(`unsupported jsonrpc ("2.0" != %s)`, message.JSONRPC)

					return
				}

				if isBatch {
					var err error
					if isResponse && message.Method != "" {
						err = errors.New("request in response batch")
					} else if !isResponse && message.Method == "" {
						err = errors.New("response in request batch")
					}

					if err != nil {
						errChan <- fmt.Errorf("invalid batch: %w", err)

						return
					}
				}
			}

			if isResponse {
				var (
					responses []*response
					err       error
				)

				if isBatch {
					err = json.Unmarshal(raw, &responses)
				} else {
					responses = append(responses, new(response))
					err = json.Unmarshal(raw, &responses[0])
				}

				if err != nil {
					errChan <- fmt.Errorf("response unmarshal: %w", err)

					return
				}

				c.wg.Add(1)

				go c.handleResponses(ctx, responses)
			} else {
				var (
					requests []*request
					err      error
				)

				if isBatch {
					err = json.Unmarshal(raw, &requests)
				} else {
					requests = append(requests, new(request))
					err = json.Unmarshal(raw, &requests[0])
				}

				if err != nil {
					errChan <- fmt.Errorf("request unmarshal: %w", err)

					return
				}

				c.wg.Add(1)

				go c.handleRequests(ctx, requests, isBatch)
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

	sink := make(chan any)
	done := make(chan struct{})

	go func() {
		out := make([]any, 0, len(requests))

		defer func() { done <- struct{}{} }()

		for resp := range sink {
			out = append(out, resp)
		}

		if len(out) == 0 {
			return
		}

		if isBatch {
			select {
			case <-c.Done():
			case c.outgoing <- out:
			}
		} else {
			select {
			case <-c.Done():
			case c.outgoing <- out[0]:
			}
		}
	}()

	wg := sync.WaitGroup{}

	for _, req := range requests {
		wg.Add(1)

		go func(r *request) {
			defer wg.Done()

			replier := c.replier(r.ID(), sink)
			if err := c.handler.ServeRPC(ctx, r, replier, c); err != nil {
				c.shutdown(fmt.Errorf("handler error: %w", err))
			}
		}(req)
	}

	wg.Wait()
	close(sink)
	<-done
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
