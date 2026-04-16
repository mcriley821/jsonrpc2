package jsonrpc2

// Option configures a [Conn] created by [NewConn].
type Option func(*connOptions)

type connOptions struct{}

func defaultConnOptions() connOptions {
	return connOptions{}
}
