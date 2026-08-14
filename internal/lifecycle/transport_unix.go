//go:build !windows

package lifecycle

import (
	"context"
	"net"
)

func dial(ctx context.Context, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", address)
}
