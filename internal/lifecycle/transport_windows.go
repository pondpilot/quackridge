//go:build windows

package lifecycle

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

func dial(ctx context.Context, address string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, address)
}
