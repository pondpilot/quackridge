package cli

import (
	"context"
	"errors"
	"sync"
	"time"

	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/app"
	"github.com/pondpilot/quackridge/internal/config"
	"github.com/pondpilot/quackridge/internal/control"
	"github.com/pondpilot/quackridge/internal/pairing"
)

type daemon struct {
	service *quackridge.Service
	runtime *app.Runtime
	config  config.Store
	mu      sync.Mutex
	pairing []*pairing.Server
}

func (d *daemon) Status() quackridge.Status               { return d.service.Status() }
func (d *daemon) Reload(ctx context.Context) error        { return d.service.Reload(ctx) }
func (d *daemon) RotateToken(ctx context.Context) error   { return d.runtime.RotateToken(ctx) }
func (d *daemon) Configuration() (config.Document, error) { return d.config.Load() }
func (d *daemon) Diagnostics(ctx context.Context) (map[string]any, error) {
	return d.runtime.Diagnostics(ctx)
}

func (d *daemon) Pair(_ context.Context, origins []string, ttl time.Duration) (control.PairingChallenge, error) {
	status := d.service.Status()
	if status.State != quackridge.StateReady && status.State != quackridge.StateDegraded {
		return control.PairingChallenge{}, errors.New("service is not ready")
	}
	server, challenge, err := pairing.Start(pairing.Options{
		Origins: origins, TTL: ttl, Endpoint: status.Endpoint, Token: d.runtime.Token(),
	})
	if err != nil {
		return control.PairingChallenge{}, err
	}
	d.mu.Lock()
	d.pairing = append(d.pairing, server)
	d.mu.Unlock()
	return control.PairingChallenge{URL: challenge.URL, Nonce: challenge.Nonce, ExpiresAt: challenge.ExpiresAt}, nil
}

func (d *daemon) closePairings() {
	d.mu.Lock()
	servers := d.pairing
	d.pairing = nil
	d.mu.Unlock()
	for _, server := range servers {
		_ = server.Close()
	}
}
