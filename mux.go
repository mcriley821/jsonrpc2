package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Nullable represents a value that may or may not be present.
type Nullable[T any] struct {
	// Value holds the unmarshalled params when Valid is true;
	// it is the zero value of T when Valid is false.
	Value T

	// Valid reports whether the JSON-RPC params field was present in the
	// request. When false, params were omitted entirely.
	Valid bool
}

// Mux dispatches incoming JSON-RPC requests to per-method handlers.
// It implements Handler and can be passed directly to NewConn as the
// top-level handler. Register methods with Handle or HandleFunc before
// the Mux is put into service.
type Mux struct {
	// handlers maps method names to their registered Handler.
	handlers map[string]Handler

	// fallback is the catch-all Handler used when no method-specific handler
	// is registered. nil means reply with MethodNotFound (the default).
	fallback Handler

	// mu guards handlers and fallback.
	mu sync.RWMutex
}

var _ Handler = (*Mux)(nil)

// NewMux creates and returns a new, empty Mux.
func NewMux() *Mux {
	return &Mux{
		handlers: make(map[string]Handler),
		fallback: nil,
		mu:       sync.RWMutex{},
	}
}

// Handle registers h to serve requests for the given method name.
// It panics if method is already registered.
func (m *Mux) Handle(method string, h Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.handlers[method]; exists {
		panic(fmt.Sprintf("jsonrpc2: handler already registered for method %q", method))
	}

	m.handlers[method] = h
}

// HandleFunc registers a bare handler function for the given method name.
// It panics if method is already registered.
func (m *Mux) HandleFunc(method string, f func(ctx context.Context, req Request, reply Replier, conn Conn) error) {
	m.Handle(method, HandlerFunc(f))
}

// Fallback sets a catch-all Handler invoked when an incoming request has no
// registered method handler. Calling Fallback more than once replaces
// the previous fallback. If no fallback is set, ServeRPC replies with a
// MethodNotFound error for unregistered methods.
func (m *Mux) Fallback(h Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fallback = h
}

// ServeRPC implements Handler. It looks up the handler registered for
// req.Method() and delegates to it. If no handler is found and a fallback is
// set, the fallback is called. Otherwise a MethodNotFound error response is
// sent via reply and ServeRPC returns nil.
func (m *Mux) ServeRPC(ctx context.Context, req Request, reply Replier, conn Conn) error {
	m.mu.RLock()
	h, ok := m.handlers[req.Method()]
	fb := m.fallback
	m.mu.RUnlock()

	if !ok {
		if fb != nil {
			if err := fb.ServeRPC(ctx, req, reply, conn); err != nil {
				return fmt.Errorf("fallback handler: %w", err)
			}

			return nil
		}

		return reply(ctx, NewError(MethodNotFound, req.Method(), nil))
	}

	if err := h.ServeRPC(ctx, req, reply, conn); err != nil {
		return fmt.Errorf("handler %q: %w", req.Method(), err)
	}

	return nil
}

// Handle registers a fully-typed request handler for method on m.
//
// When a request arrives, params are unmarshalled into P if present, and
// the handler receives a [Nullable][P] reporting whether params were provided.
// If params are absent, the handler receives Nullable[P]{Valid: false}.
//
// The return value R is marshaled and sent as the success result.
//
// Error handling:
//   - If f returns an Error (via errors.As), an error response is sent.
//   - Params unmarshal errors send an InvalidParams response.
//   - Any other non-nil error closes the connection.
//   - If the request is a notification (req.ID() == nil), no response is
//     sent regardless of R or the error kind.
func Handle[P, R any](m *Mux, method string, f func(ctx context.Context, params Nullable[P], conn Conn) (R, error)) {
	m.HandleFunc(method, func(ctx context.Context, req Request, reply Replier, conn Conn) error {
		isNotif := req.ID() == nil

		var np Nullable[P]
		if raw := req.Params(); raw != nil {
			if err := json.Unmarshal(raw, &np.Value); err != nil {
				if isNotif {
					return nil
				}

				return reply(ctx, NewError(InvalidParams, fmt.Sprintf("unmarshalling params: %s", err), nil))
			}

			np.Valid = true
		}

		result, fErr := f(ctx, np, conn)
		if fErr != nil {
			var rpcErr Error

			if errors.As(fErr, &rpcErr) {
				if !isNotif {
					return reply(ctx, rpcErr)
				}

				return nil
			}

			return fErr
		}

		if isNotif {
			return nil
		}

		return reply(ctx, result)
	})
}

// HandleNotification registers a typed notification handler for method on m.
//
// Notification handlers receive no Replier; calling one is not possible by
// construction.
//
// If params are absent, the handler receives [Nullable][P]{Valid: false}.
// Params unmarshal errors and Error returns from f are silently discarded
// per JSON-RPC 2.0 §6 (notifications cannot be confirmed). Any other non-nil
// error from f closes the connection.
func HandleNotification[P any](
	m *Mux, method string, f func(ctx context.Context, params Nullable[P], conn Conn) error,
) {
	m.HandleFunc(method, func(ctx context.Context, req Request, _ Replier, conn Conn) error {
		var np Nullable[P]

		raw := req.Params()
		if raw != nil && json.Unmarshal(raw, &np.Value) == nil {
			np.Valid = true
		}

		if raw != nil && !np.Valid {
			return nil // malformed params silently discarded per §6
		}

		if fErr := f(ctx, np, conn); fErr != nil {
			var rpcErr Error

			if !errors.As(fErr, &rpcErr) {
				return fErr
			}
		}

		return nil
	})
}
