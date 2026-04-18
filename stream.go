package jsonrpc2

import (
	"encoding/json"
	"fmt"
	"io"
)

// Stream encodes and decodes JSON messages.
// Each Read/Write call exchanges one complete JSON value.
// Errors are fatal and cause [Conn] to close.
type Stream interface {
	// Read decodes one JSON value into v.
	Read(v any) error

	// Write encodes and sends one JSON value.
	Write(obj any) error

	// Close closes the underlying transport.
	Close() error
}

type encoderStream struct {
	stream  io.ReadWriteCloser
	decoder *json.Decoder
	encoder *json.Encoder
}

var _ Stream = (*encoderStream)(nil)

// NewStream creates a [Stream] over stream for newline-delimited JSON.
func NewStream(stream io.ReadWriteCloser) Stream {
	return &encoderStream{stream, json.NewDecoder(stream), json.NewEncoder(stream)}
}

func (s *encoderStream) Read(v any) error {
	if err := s.decoder.Decode(v); err != nil {
		return fmt.Errorf("decoding object: %w", err)
	}

	return nil
}

func (s *encoderStream) Write(v any) error {
	if err := s.encoder.Encode(v); err != nil {
		return fmt.Errorf("encoding object: %w", err)
	}

	return nil
}

func (s *encoderStream) Close() error {
	if err := s.stream.Close(); err != nil {
		return fmt.Errorf("closing: %w", err)
	}

	return nil
}
