package jsonrpc2

import (
	"encoding/json"
	"fmt"
)

type requestObj struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitzero"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitzero"`
}

// Request represents an incoming request or notification.
// Notifications have a nil ID.
type Request interface {
	// ID returns the request identifier, or nil for a notification.
	ID() any

	// Method returns the method name.
	Method() string

	// Params returns the raw JSON params, or nil if absent.
	Params() json.RawMessage
}

type request struct {
	obj requestObj
}

var _ Request = (*request)(nil)

func (r *request) ID() any {
	return r.obj.ID
}

func (r *request) Method() string {
	return r.obj.Method
}

func (r *request) Params() json.RawMessage {
	return r.obj.Params
}

func (r *request) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(r.obj)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	return data, nil
}

func (r *request) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &r.obj); err != nil {
		return fmt.Errorf("unmarshalling request: %w", err)
	}

	return nil
}

func newRequest(id any, method string, params any) (*request, error) {
	var rawParams json.RawMessage

	if params != nil {
		var err error

		rawParams, err = json.Marshal(&params)
		if err != nil {
			return nil, fmt.Errorf("marshalling params: %w", err)
		}
	}

	return &request{requestObj{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  rawParams,
	}}, nil
}
