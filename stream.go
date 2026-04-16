package jsonrpc2

import (
	"encoding/json"
	"fmt"
	"io"
)

// Stream wraps an io.ReadWriteCloser with JSON encoding and decoding.
// Each Read/Write call exchanges a single complete JSON value.
type Stream interface {
	// Read a message from the stream into v. A non-nil error is treated as
	// fatal by Conn, which will close the connection in response.
	Read(v any) error

	// Write a message to the stream. A non-nil error is treated as fatal
	// by Conn, which will close the connection in response.
	Write(obj any) error

	// Close the stream.
	Close() error
}

// encoderStream is the unexported implementation of Stream.
type encoderStream struct {
	stream  io.ReadWriteCloser // underlying I/O transport
	decoder *json.Decoder      // decodes incoming JSON
	encoder *json.Encoder      // encodes outgoing JSON
}

var _ Stream = (*encoderStream)(nil)

// NewStream creates a Stream that encodes outgoing values and decodes incoming
// values as newline-delimited JSON over stream.
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
