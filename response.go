package jsonrpc2

import (
	"encoding/json"
	"fmt"
)

type responseObj struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Result  json.RawMessage `json:"result,omitzero"`
	Error   *rpcError       `json:"error,omitzero"`
}

// Response represents a response received from a [Conn.Call].
type Response interface {
	// ID returns the request identifier this response corresponds to.
	ID() any

	// Result unmarshals the success result into value. Only call when
	// [Response.Failed] returns false.
	Result(value any) error

	// Error returns the error, or nil if this is a success response.
	Error() Error

	// Failed reports whether this is an error response.
	Failed() bool
}

type response struct {
	obj responseObj
}

var _ Response = (*response)(nil)

func (r *response) ID() any {
	return r.obj.ID
}

func (r *response) Result(value any) error {
	if err := json.Unmarshal(r.obj.Result, value); err != nil {
		return fmt.Errorf("unmarshalling response: %w", err)
	}

	return nil
}

func (r *response) Error() Error {
	return r.obj.Error
}

func (r *response) Failed() bool {
	return r.obj.Error != nil
}

func (r *response) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(&r.obj)
	if err != nil {
		return nil, fmt.Errorf("marshalling response: %w", err)
	}

	return data, nil
}

func (r *response) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &r.obj); err != nil {
		return fmt.Errorf("unmarshalling response: %w", err)
	}

	return nil
}

func newErrorResponse(id any, err Error) *response {
	jerr, ok := err.(*rpcError)

	if !ok {
		jerr = &rpcError{errObj{
			Code:    err.Code(),
			Message: err.Message(),
			Data:    err.Data(),
		}}
	}

	return &response{responseObj{
		JSONRPC: "2.0",
		ID:      id,
		Result:  nil,
		Error:   jerr,
	}}
}

func newResponse(id any, result json.RawMessage) *response {
	return &response{responseObj{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
		Error:   nil,
	}}
}
