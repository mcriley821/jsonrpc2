package jsonrpc2

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	// ErrClosed is returned by method calls of a closed [Conn].
	ErrClosed = errors.New("connection closed")

	// ErrReplied is returned by a [Replier] that has already been called once.
	ErrReplied = errors.New("reply already sent")
)

// ErrorCode is an error code. Standard codes are in the range [-32768, -32000];
// application-defined codes should be outside this range.
type ErrorCode = int

const (
	// ParseError is returned when the JSON is malformed.
	ParseError ErrorCode = -32700

	// InvalidRequest is returned when the request object is invalid.
	InvalidRequest ErrorCode = -32600

	// MethodNotFound is returned when the method does not exist.
	MethodNotFound ErrorCode = -32601

	// InvalidParams is returned when the method arguments are invalid.
	InvalidParams ErrorCode = -32602

	// InternalError is returned for server-side processing errors.
	InternalError ErrorCode = -32603
)

// Error represents a request error with code, message, and optional data.
type Error interface {
	error

	// Code returns the error code.
	Code() ErrorCode

	// Message returns the error description.
	Message() string

	// Data returns supplementary data, or nil.
	Data() any
}

type errObj struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Data    any       `json:"data,omitzero"`
}

type rpcError struct {
	obj errObj
}

var _ Error = (*rpcError)(nil)

// NewError creates a new [Error].
// Pass nil for data to omit the field.
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
