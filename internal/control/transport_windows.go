//go:build windows

package control

import (
	"context"
	"net"
	"os/user"
	"time"

	"github.com/Microsoft/go-winio"
)

func DefaultAddress() (string, error) { return `\\.\pipe\QuackRidge-control`, nil }

func listen(address string) (net.Listener, error) {
	current, err := user.Current()
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(address, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + current.Uid + ")",
		MessageMode:        false, InputBufferSize: 64 << 10, OutputBufferSize: 1 << 20,
	})
}

func dial(ctx context.Context, address string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, address)
}

func EndpointPresent(address string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	connection, err := dial(ctx, address)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}
