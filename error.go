package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	// ErrClosed is returned by method calls of a closed Conn.
	ErrClosed = errors.New("connection closed")
)

// ErrorCode is a JSON-RPC 2.0 error code. Application-defined codes should
// be outside the reserved range [-32768, -32000].
type ErrorCode = int

const (
	// ParseError indicates an error occurred on the server while parsing the JSON message.
	ParseError ErrorCode = -32700

	// InvalidRequest indicates the JSON received is an invalid Request object.
	InvalidRequest ErrorCode = -32600

	// MethodNotFound indicates the method does not exist or is not available.
	MethodNotFound ErrorCode = -32601

	// InvalidParams indicates invalid parameters for the requested method.
	InvalidParams ErrorCode = -32602

	// InternalError indicates an internal error occurred while processing.
	InternalError ErrorCode = -32603
)

// Error represents a JSON-RPC 2.0 error object.
type Error interface {
	error

	// Code returns the error code of this error.
	Code() ErrorCode

	// Message returns the human-readable description of the error.
	Message() string

	// Data returns the optional supplementary data attached to the error,
	// or nil if none was provided.
	Data() any
}

// errObj is the JSON-serializable form of a JSON-RPC 2.0 error object.
type errObj struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Data    any       `json:"data,omitzero"`
}

// rpcError is an implementation of Error.
type rpcError struct {
	obj errObj
}

var _ Error = (*rpcError)(nil)

// NewError creates a new JSON-RPC 2.0 error with the given code, message, and
// optional data.
func NewError(code ErrorCode, message string, data any) Error {
	return &rpcError{errObj{code, message, data}}
}

func (e *rpcError) Code() ErrorCode {
	return e.obj.Code
}

func (e *rpcError) Message() string {
	return e.obj.Message
}

func (e *rpcError) Data() any {
	return e.obj.Data
}

func (e *rpcError) Error() string {
	return e.obj.Message
}

func (e *rpcError) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(&e.obj)
	if err != nil {
		return nil, fmt.Errorf("marshalling error: %w", err)
	}

	return data, nil
}

func (e *rpcError) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &e.obj); err != nil {
		return fmt.Errorf("unmarshalling error: %w", err)
	}

	return nil
}
