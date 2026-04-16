package jsonrpc2_test

import (
	"testing"

	"github.com/mcriley821/jsonrpc2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewError(t *testing.T) {
	t.Parallel()

	const (
		code    = 1
		message = "test message"
		data    = false
	)

	err := jsonrpc2.NewError(code, message, data)
	require.NotNil(t, err)

	assert.Equal(t, code, err.Code())
	assert.Equal(t, message, err.Message())
	assert.Equal(t, data, err.Data())
}
