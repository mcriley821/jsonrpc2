package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Nullable represents an optional value.
type Nullable[T any] struct {
	// Value holds the unmarshalled value when [Nullable.Valid] is true.
	Value T

	// Valid reports whether params were provided.
	Valid bool
}

// Mux dispatches requests to per-method handlers. It implements [Handler].
type Mux struct {
	handlers map[string]Handler
	fallback Handler
	mu       sync.RWMutex
}

var _ Handler = (*Mux)(nil)

// NewMux creates and returns a new, empty [Mux].
func NewMux() *Mux {
	return &Mux{
		handlers: make(map[string]Handler),
		fallback: nil,
		mu:       sync.RWMutex{},
	}
}

// Handle registers h for the method. Panics if already registered.
func (m *Mux) Handle(method string, h Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.handlers[method]; exists {
		panic(fmt.Sprintf("jsonrpc2: handler already registered for method %q", method))
	}

	m.handlers[method] = h
}

// HandleFunc registers f for the method. Panics if already registered.
func (m *Mux) HandleFunc(method string, f func(ctx context.Context, req Request, reply Replier, conn Conn) error) {
	m.Handle(method, HandlerFunc(f))
}

// Fallback sets a catch-all handler for unregistered methods.
// Without it, MethodNotFound is returned.
func (m *Mux) Fallback(h Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fallback = h
}

// ServeRPC handles the incoming request by forwarding to the registered method
// handler, falling back to [Mux.Fallback] when there is no registered handler.
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

// Handle registers a typed handler for method on m. The handler receives params
// as [Nullable][P] and returns R to be marshaled as the result.
// An [Error] return sends an error response; unmarshal errors send [InvalidParams].
// Notifications suppress the response regardless of result or error.
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
// Unmarshal errors and [Error] returns are silently discarded.
//
// If the peer sends a regular request (non-nil ID) to this method, no reply is
// ever sent. The remote peer will never receive a response.
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
