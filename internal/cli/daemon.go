package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/app"
	"github.com/pondpilot/quackridge/internal/certstore"
	"github.com/pondpilot/quackridge/internal/config"
	"github.com/pondpilot/quackridge/internal/control"
	"github.com/pondpilot/quackridge/internal/pairing"
)

type daemon struct {
	service      *quackridge.Service
	runtime      *app.Runtime
	config       config.Store
	manager      config.TransactionalService
	mu           sync.Mutex
	pairing      map[string]*pairing.Server
	pairingState map[string]string
	rotating     bool
	reveals      map[string]time.Time
	certificates certstore.Store
}

func (d *daemon) Status() quackridge.Status        { return d.service.Status() }
func (d *daemon) Reload(ctx context.Context) error { return d.service.Reload(ctx) }
func (d *daemon) RotateToken(ctx context.Context) error {
	d.mu.Lock()
	if d.rotating {
		d.mu.Unlock()
		return errors.New("token rotation is already active")
	}
	d.rotating = true
	servers := d.pairing
	d.pairing = make(map[string]*pairing.Server)
	d.mu.Unlock()
	for _, server := range servers {
		_ = server.Close()
	}
	err := d.runtime.RotateToken(ctx)
	d.mu.Lock()
	d.rotating = false
	d.mu.Unlock()
	return err
}
func (d *daemon) Configuration() (config.Document, error) { return d.config.Load() }
func (d *daemon) Diagnostics(ctx context.Context) (map[string]any, error) {
	return d.runtime.Diagnostics(ctx)
}

func (d *daemon) MutateSource(ctx context.Context, mutation config.Mutation) (config.Document, string, error) {
	return d.manager.Apply(ctx, mutation)
}
func (d *daemon) RefreshSourceHealth(ctx context.Context, id string) error {
	return d.runtime.RefreshSourceHealth(ctx, id)
}

func (d *daemon) Pair(_ context.Context, origins []string, ttl time.Duration) (control.PairingChallenge, error) {
	status := d.service.Status()
	if status.State != quackridge.StateReady && status.State != quackridge.StateDegraded {
		return control.PairingChallenge{}, errors.New("service is not ready")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.rotating {
		return control.PairingChallenge{}, errors.New("token rotation is active")
	}
	server, challenge, err := pairing.Start(pairing.Options{
		Origins: origins, TTL: ttl, Endpoint: status.Endpoint, Token: d.runtime.Token(),
	})
	if err != nil {
		return control.PairingChallenge{}, err
	}
	id, err := randomDaemonID()
	if err != nil {
		_ = server.Close()
		return control.PairingChallenge{}, err
	}
	if d.pairing == nil {
		d.pairing = make(map[string]*pairing.Server)
	}
	d.pairing[id] = server
	go func() {
		<-server.Done()
		d.mu.Lock()
		if d.pairing[id] == server {
			delete(d.pairing, id)
			if d.pairingState == nil {
				d.pairingState = make(map[string]string)
			}
			for len(d.pairingState) >= 64 {
				for terminalID := range d.pairingState {
					delete(d.pairingState, terminalID)
					break
				}
			}
			d.pairingState[id] = server.Status()
		}
		d.mu.Unlock()
	}()
	return control.PairingChallenge{ID: id, URL: challenge.URL, Nonce: challenge.Nonce, ExpiresAt: challenge.ExpiresAt}, nil
}

func (d *daemon) PairingStatus(id string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	server, ok := d.pairing[id]
	if ok {
		return server.Status(), true
	}
	state, ok := d.pairingState[id]
	return state, ok
}

func (d *daemon) CancelPairing(id string) bool {
	d.mu.Lock()
	server, ok := d.pairing[id]
	d.mu.Unlock()
	if ok {
		_ = server.Close()
	}
	return ok
}

func (d *daemon) PrepareManualReveal(context.Context) (control.ManualRevealPreparation, error) {
	nonce, err := randomDaemonID()
	if err != nil {
		return control.ManualRevealPreparation{}, err
	}
	expires := time.Now().UTC().Add(30 * time.Second)
	d.mu.Lock()
	if d.reveals == nil {
		d.reveals = make(map[string]time.Time)
	}
	d.reveals[nonce] = expires
	d.mu.Unlock()
	return control.ManualRevealPreparation{Nonce: nonce, ExpiresAt: expires}, nil
}

func (d *daemon) ConsumeManualReveal(_ context.Context, nonce, confirmation string) (string, error) {
	if confirmation != "REVEAL QUACK TOKEN" {
		return "", errors.New("confirmation mismatch")
	}
	d.mu.Lock()
	expires, ok := d.reveals[nonce]
	delete(d.reveals, nonce)
	d.mu.Unlock()
	if !ok || time.Now().After(expires) {
		return "", errors.New("nonce expired")
	}
	return d.runtime.Token(), nil
}

func randomDaemonID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (d *daemon) ImportCertificate(data []byte) (certstore.Certificate, error) {
	return d.certificates.Import(data)
}
func (d *daemon) ListCertificates() ([]certstore.Certificate, error) { return d.certificates.List() }
func (d *daemon) RemoveCertificate(reference string) error {
	return d.manager.WithDocumentLock(context.Background(), func(document config.Document) error {
		if certificateReferenced(document, reference) {
			return errors.New("certificate is in use")
		}
		return d.certificates.Remove(reference)
	})
}

func (d *daemon) closePairings() {
	d.mu.Lock()
	servers := d.pairing
	d.pairing = make(map[string]*pairing.Server)
	d.mu.Unlock()
	for _, server := range servers {
		_ = server.Close()
	}
}
