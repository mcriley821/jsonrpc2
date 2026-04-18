package jsonrpc2

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// Conn is a full-duplex JSON-RPC 2.0 connection. It dispatches incoming
// requests to the Handler provided to NewConn, and exposes Call and Notify
// for sending outbound requests.
type Conn interface {
	// Call sends a JSON-RPC request for method with the given params and
	// waits for the response. params is marshaled to JSON; pass nil to
	// omit the params field. Returns the terminal errorif the connection
	// shuts down before the response arrives, or a context error if ctx
	// expires first.
	Call(ctx context.Context, method string, params any) (Response, error)

	// Notify sends a JSON-RPC notification (a request with no ID) for
	// method with the given params. The server will not send a response.
	// Returns the terminal error if the connection has already shut down.
	Notify(ctx context.Context, method string, params any) error

	// Close shuts down the connection. It is safe to call more than once.
	// Returns any error from closing the underlying stream, or ctx.Err() if
	// ctx expires before all background goroutines finish.
	Close(ctx context.Context) error

	// Done returns a channel that is closed when the connection has fully shut
	// down and all background goroutines have exited. It is safe for multiple
	// goroutines to wait on the channel. Use Err to retrieve the terminal error.
	Done() <-chan struct{}

	// Err returns the terminal error once the connection has shut down. It
	// returns nil until Done is closed. On a clean Close the value is
	// ErrClosed; on an I/O or handler failure it is a wrapped error describing
	// the cause.
	Err() error
}

// conn is an implementation of Conn.
type conn struct {
	// cancel terminates the background read/write goroutines.
	cancel context.CancelFunc

	// stream is the underlying transport.
	stream Stream

	// handler is responsible for dispatching incoming RPC requests.
	handler Handler

	// outgoing is an unbuffered channel used to hand off outgoing messages to
	// the write goroutine.
	outgoing chan any

	// done is closed after all background goroutines have exited.
	done chan struct{}

	// shutdownOnce ensures teardown runs exactly once.
	shutdownOnce sync.Once

	// termErr is the first terminal error, set inside shutdownOnce.Do.
	termErr error

	// streamCloseErr is the error returned by stream.Close(), set inside
	// shutdownOnce.Do.
	streamCloseErr error

	// wg tracks all background goroutines: read, write, and per-request handlers.
	// done is closed only after wg reaches zero.
	wg sync.WaitGroup

	// inflight maps outgoing call IDs (always UUID strings) to their response
	// channels.
	inflight map[string]chan *response

	// closed is set to true by shutdown under mu. registerRequest checks it
	// under the same lock before inserting into inflight, eliminating the
	// TOCTOU window between the closed check and the map write.
	closed bool

	// mu protects access to inflight and closed.
	mu sync.Mutex
}

var _ Conn = (*conn)(nil)

// NewConn creates a new Conn over stream, using handler to dispatch incoming
// requests. It starts background goroutines for reading, writing, and lifecycle
// management; use Done and Err to observe when they exit.
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
		mu:             sync.Mutex{},
	}

	go c.run(ctx)

	return c
}

func (c *conn) Call(ctx context.Context, method string, params any) (Response, error) {
	id := uuid.NewString()

	req, err := newRequest(id, method, params)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	respCh := make(chan *response, 1)

	if err := c.registerRequest(id, respCh); err != nil {
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

func (c *conn) Close(ctx context.Context) error {
	c.shutdown(ErrClosed)

	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck
	case <-c.done:
		return c.streamCloseErr
	}
}

func (c *conn) Done() <-chan struct{} {
	return c.done
}

func (c *conn) Err() error {
	select {
	case <-c.done:
		return c.termErr
	default:
		return nil
	}
}

// registerRequest registers ch under id in the inflight map. It holds mu for
// the duration so that the closed check and the map insertion are a single
// critical section, eliminating the TOCTOU window against shutdown.
func (c *conn) registerRequest(id string, ch chan *response) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return c.termErr
	}

	c.inflight[id] = ch

	return nil
}

// shutdown records err as the terminal error (first call wins), cancels the
// context, closes the underlying stream, and clears the inflight map. It is
// safe to call concurrently and from multiple goroutines.
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

// run starts the read and write goroutines and waits for the first of:
// context cancellation, a read error, or a write error. It calls shutdown with
// the triggering error, waits for all goroutines to exit via wg, then closes
// done to broadcast completion.
func (c *conn) run(ctx context.Context) {
	defer func() {
		c.wg.Wait()
		close(c.done)
	}()

	readDone := make(chan error, 1)
	writeDone := make(chan error, 1)

	c.wg.Add(2) //nolint:mnd // two goroutines: read and write

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

// partialMessage is used to classify an incoming JSON-RPC message before full
// deserialization. A non-empty Method indicates a request or notification; an
// empty Method indicates a response.
type partialMessage struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
}

// read loops reading JSON messages from the stream. Each message is partially
// decoded to distinguish requests (have a method field) from responses, then
// dispatched to handleRequest or handleResponse in a new goroutine. Any error
// is sent to errChan and the loop exits. Context cancellation also exits the
// loop; the stream must be closed externally to unblock any in-progress read.
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

			var v partialMessage
			if err := json.Unmarshal(raw, &v); err != nil {
				errChan <- fmt.Errorf("message unmarshal: %w", err)

				return
			}

			if v.JSONRPC != "2.0" {
				errChan <- fmt.Errorf(`unsupported jsonrpc ("2.0" != %s)`, v.JSONRPC)

				return
			}

			if v.Method == "" {
				resp := new(response)

				if err := json.Unmarshal(raw, &resp); err != nil {
					errChan <- fmt.Errorf("response unmarshal: %w", err)

					return
				}

				c.wg.Add(1)

				go c.handleResponse(ctx, resp)
			} else {
				req := new(request)

				if err := json.Unmarshal(raw, &req); err != nil {
					errChan <- fmt.Errorf("request unmarshal: %w", err)

					return
				}

				c.wg.Add(1)

				go c.handleRequest(ctx, req)
			}
		}
	}
}

// write drains the outgoing channel, encoding each message to the stream.
// Any write error is sent to errChan and the loop exits.
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

// handleRequest dispatches req to the handler. If the handler returns a
// non-nil error, shutdown is called with the wrapped error, closing the
// connection.
func (c *conn) handleRequest(ctx context.Context, req *request) {
	defer c.wg.Done()

	if err := c.handler.ServeRPC(ctx, req, c.replier(req.ID()), c); err != nil {
		c.shutdown(fmt.Errorf("handler error: %w", err))
	}
}

// handleResponse delivers resp to the Call goroutine waiting on the matching
// request ID. Responses with non-string IDs are silently dropped, as all
// outgoing call IDs are UUID strings. If no goroutine is waiting (request was
// cancelled or the ID is unknown), the response is silently dropped.
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

// replier returns a Replier closure bound to id. The closure builds an error
// response when result is an Error, or marshals result as a success response,
// then queues the response to the outgoing channel. If the message is a
// notification (id == nil), a no-op closure is returned.
func (c *conn) replier(id any) Replier {
	if id == nil {
		return func(_ context.Context, _ any) error { return nil }
	}

	return func(ctx context.Context, result any) error {
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
		case c.outgoing <- resp:
			return nil
		}
	}
}
