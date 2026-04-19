package jsonrpc2_test

import (
	"io"
	"net"
	"testing"

	"github.com/mcriley821/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testStream struct {
	net.Conn

	closed bool
}

var _ io.ReadWriteCloser = (*testStream)(nil)

func newTestStream(t *testing.T) (*testStream, net.Conn) {
	t.Helper()

	c1, c2 := net.Pipe()

	t.Cleanup(func() { _ = c1.Close() })
	t.Cleanup(func() { _ = c2.Close() })

	return &testStream{c1, false}, c2
}

func (s *testStream) Close() error {
	if s.closed {
		return nil
	}

	s.closed = true

	return s.Conn.Close() //nolint:wrapcheck
}

func TestNewStream(t *testing.T) {
	t.Parallel()

	buf, _ := newTestStream(t)

	stream := jsonrpc2.NewStream(buf)
	assert.NotNil(t, stream)
}

func TestStream_Read(t *testing.T) { //nolint:tparallel
	t.Parallel()

	s, p := newTestStream(t)

	stream := jsonrpc2.NewStream(s)
	require.NotNil(t, stream)

	t.Run("empty obj", func(t *testing.T) {
		go func() {
			_, err := p.Write([]byte("{}"))
			assert.NoError(t, err)
		}()

		var v any

		require.NoError(t, stream.Read(&v))
	})

	t.Run("valid", func(t *testing.T) {
		go func() {
			_, err := p.Write([]byte(`{"key": "value"}`))
			assert.NoError(t, err)
		}()

		v := map[string]string{}
		require.NoError(t, stream.Read(&v))

		require.Contains(t, v, "key")
		assert.Equal(t, "value", v["key"])
	})

	t.Run("invalid", func(t *testing.T) {
		go func() {
			_, err := p.Write([]byte("not json"))
			assert.NoError(t, err)
		}()

		var v any

		assert.Error(t, stream.Read(&v))
	})
}

func TestStream_Write(t *testing.T) { //nolint:tparallel
	t.Parallel()

	s, p := newTestStream(t)

	stream := jsonrpc2.NewStream(s)
	require.NotNil(t, stream)

	t.Run("valid", func(t *testing.T) {
		go func() { assert.NoError(t, stream.Write("test")) }()

		data := make([]byte, 7) // json.Encoder appends a newline
		_, err := p.Read(data)

		require.NoError(t, err)
		assert.Equal(t, `"test"`+"\n", string(data))
	})

	t.Run("invalid", func(t *testing.T) {
		assert.Error(t, stream.Write(func() {}))
	})
}

func TestStream_Close(t *testing.T) {
	t.Parallel()

	s, _ := newTestStream(t)

	stream := jsonrpc2.NewStream(s)
	require.NotNil(t, stream)

	require.NoError(t, stream.Close())
	assert.True(t, s.closed)
}
