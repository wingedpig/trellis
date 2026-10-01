// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package tsnode

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"os"
	"sync"

	"github.com/wingedpig/trellis/internal/config"
	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

// BackendFactory creates the Backend for one node. cfg.StateDir is the
// canonical state directory.
type BackendFactory func(cfg config.TailscaleNodeConfig) Backend

// Backend is the subset of *tsnet.Server (plus its LocalClient) that tsnode
// uses. It exists so tests can substitute a fake.
type Backend interface {
	Start() error
	Listen(network, addr string) (net.Listener, error)
	GetCertificate(hi *tls.ClientHelloInfo) (*tls.Certificate, error)
	WatchIPNBus(ctx context.Context, mask ipn.NotifyWatchOpt) (IPNWatcher, error)
	StatusWithoutPeers(ctx context.Context) (*ipnstate.Status, error)
	StartLoginInteractive(ctx context.Context) error
	ClearServeConfig(ctx context.Context) error
	Close() error
}

// IPNWatcher is a stream of IPN bus notifications.
type IPNWatcher interface {
	Next() (ipn.Notify, error)
	Close() error
}

// TSNetBackend returns the production BackendFactory, which wraps
// *tsnet.Server. Backend logs go to debugf (verbose; may be nil to discard)
// and user-facing logs to log.Printf.
func TSNetBackend(debugf func(format string, args ...any)) BackendFactory {
	return func(cfg config.TailscaleNodeConfig) Backend {
		s := &tsnet.Server{
			Hostname:  cfg.Hostname,
			Dir:       cfg.StateDir,
			Ephemeral: cfg.Ephemeral,
			UserLogf: func(format string, args ...any) {
				log.Printf("tailscale node "+cfg.Hostname+": "+format, args...)
			},
		}
		if cfg.AuthKeyEnv != "" {
			s.AuthKey = os.Getenv(cfg.AuthKeyEnv)
		}
		if debugf != nil {
			s.Logf = func(format string, args ...any) {
				debugf("tsnet["+cfg.Hostname+"]: "+format, args...)
			}
		}
		return &tsnetBackend{s: s}
	}
}

type tsnetBackend struct {
	s *tsnet.Server

	mu sync.Mutex
	lc *local.Client
}

var errNotStarted = errors.New("tailscale node not started")

func (b *tsnetBackend) Start() error {
	if err := b.s.Start(); err != nil {
		return err
	}
	lc, err := b.s.LocalClient()
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.lc = lc
	b.mu.Unlock()
	return nil
}

func (b *tsnetBackend) client() (*local.Client, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lc == nil {
		return nil, errNotStarted
	}
	return b.lc, nil
}

func (b *tsnetBackend) Listen(network, addr string) (net.Listener, error) {
	return b.s.Listen(network, addr)
}

func (b *tsnetBackend) GetCertificate(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
	lc, err := b.client()
	if err != nil {
		return nil, err
	}
	return lc.GetCertificate(hi)
}

func (b *tsnetBackend) WatchIPNBus(ctx context.Context, mask ipn.NotifyWatchOpt) (IPNWatcher, error) {
	lc, err := b.client()
	if err != nil {
		return nil, err
	}
	return lc.WatchIPNBus(ctx, mask)
}

func (b *tsnetBackend) StatusWithoutPeers(ctx context.Context) (*ipnstate.Status, error) {
	lc, err := b.client()
	if err != nil {
		return nil, err
	}
	return lc.StatusWithoutPeers(ctx)
}

func (b *tsnetBackend) StartLoginInteractive(ctx context.Context) error {
	lc, err := b.client()
	if err != nil {
		return err
	}
	return lc.StartLoginInteractive(ctx)
}

func (b *tsnetBackend) ClearServeConfig(ctx context.Context) error {
	lc, err := b.client()
	if err != nil {
		return err
	}
	return lc.SetServeConfig(ctx, new(ipn.ServeConfig))
}

func (b *tsnetBackend) Close() error {
	return b.s.Close()
}
