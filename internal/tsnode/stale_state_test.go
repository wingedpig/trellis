// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package tsnode_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/tsnode"
	"github.com/wingedpig/trellis/internal/tsnode/tsnodetest"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

// gate blocks a wrapped call the first time it runs until released.
type gate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGate() *gate { return &gate{entered: make(chan struct{}), release: make(chan struct{})} }

func (g *gate) hold(ctx context.Context) {
	g.once.Do(func() {
		close(g.entered)
		select {
		case <-g.release:
		case <-ctx.Done():
		}
	})
}

func (g *gate) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("gated call not entered")
	}
}

type gatedClearServe struct {
	*tsnodetest.Fake
	g *gate
}

func (b *gatedClearServe) ClearServeConfig(ctx context.Context) error {
	b.g.hold(ctx)
	return b.Fake.ClearServeConfig(ctx)
}

type gatedStatus struct {
	*tsnodetest.Fake
	g *gate
}

// StatusWithoutPeers takes its snapshot, then (the first time) blocks, so
// the snapshot is stale by the time it returns.
func (b *gatedStatus) StatusWithoutPeers(ctx context.Context) (*ipnstate.Status, error) {
	st, err := b.Fake.StatusWithoutPeers(ctx)
	b.g.hold(ctx)
	return st, err
}

func startSingle(t *testing.T, cfg config.TailscaleNodeConfig, be tsnode.Backend, tm tsnode.Timings) *tsnode.Registry {
	t.Helper()
	reg, err := tsnode.NewRegistry([]config.TailscaleNodeConfig{cfg}, []string{cfg.Hostname},
		func(config.TailscaleNodeConfig) tsnode.Backend { return be },
		tsnode.WithTimings(tm), tsnode.WithLogf(func(string, ...any) {}))
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, reg.Shutdown(ctx))
	})
	reg.Start()
	return reg
}

// NeedsLogin arriving while ClearServeConfig runs must not be overwritten by
// running, must not release waiters, and must still trigger interactive login.
func TestStateChangeDuringClearServeConfig(t *testing.T) {
	cfg := nodeCfg(t, "ent")
	b := &gatedClearServe{Fake: tsnodetest.NewFake(cfg), g: newGate()}
	reg := startSingle(t, cfg, b, fast)
	require.NoError(t, b.WaitWatcher(3*time.Second))
	ch := make(chan lnResult, 1)
	go func() {
		ln, err := reg.Listener(context.Background(), "ent", ":443")
		ch <- lnResult{ln, err}
	}()

	b.SendState(ipn.Running)
	b.g.waitEntered(t)
	b.UpdateStatus(func(s *tsnodetest.FakeStatus) { s.BackendState = "NeedsLogin" })
	b.SendState(ipn.NeedsLogin)
	waitFor(t, func() bool { return reg.Status()[0].State == tsnode.StateNeedsLogin }, "needs-login")
	close(b.g.release)

	waitFor(t, func() bool { return b.LoginCalls() == 1 }, "StartLoginInteractive")
	assert.Equal(t, tsnode.StateNeedsLogin, reg.Status()[0].State)
	pending(t, ch)

	// Login completes: serve config was already cleared, so no second call.
	b.UpdateStatus(func(s *tsnodetest.FakeStatus) { s.BackendState = "Running" })
	b.SendState(ipn.Running)
	require.NoError(t, recv(t, ch).err)
	waitFor(t, func() bool { return reg.Status()[0].State == tsnode.StateRunning }, "running")
	assert.Equal(t, 1, b.ClearServeCalls())
}

// A status snapshot taken before Running cleared the login URL must not put
// the URL back; otherwise a later expiry would skip interactive login.
func TestStaleStatusDoesNotRestoreLoginURL(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tm      func() tsnode.Timings
		trigger func(*gatedStatus)
	}{
		{"readiness check", func() tsnode.Timings { return fast }, func(b *gatedStatus) { b.Send(tsnodetest.NetMapNotify()) }},
		{"periodic refresh", func() tsnode.Timings { tm := fast; tm.RefreshInterval = 20 * time.Millisecond; return tm }, func(*gatedStatus) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := nodeCfg(t, "ent")
			b := &gatedStatus{Fake: tsnodetest.NewFake(cfg), g: newGate()}
			oldURL := "https://login.tailscale.com/a/old"
			b.UpdateStatus(func(s *tsnodetest.FakeStatus) { s.BackendState = "NeedsLogin"; s.AuthURL = oldURL })
			reg := startSingle(t, cfg, b, tc.tm())
			require.NoError(t, b.WaitWatcher(3*time.Second))
			b.SendState(ipn.NeedsLogin)
			b.Send(ipn.Notify{BrowseToURL: &oldURL})
			waitFor(t, func() bool { return reg.Status()[0].LoginURL == oldURL }, "initial login URL")

			tc.trigger(b)
			b.g.waitEntered(t) // snapshot (NeedsLogin + old URL) taken and held
			b.UpdateStatus(func(s *tsnodetest.FakeStatus) { s.BackendState = "Running"; s.AuthURL = "" })
			b.SendState(ipn.Running)
			waitFor(t, func() bool { return reg.Status()[0].LoginURL == "" }, "Running clears login URL")
			close(b.g.release)
			waitFor(t, func() bool { return reg.Status()[0].State == tsnode.StateRunning }, "running")
			assert.Empty(t, reg.Status()[0].LoginURL, "stale snapshot restored the login URL")

			b.UpdateStatus(func(s *tsnodetest.FakeStatus) { s.BackendState = "NeedsLogin" })
			b.SendState(ipn.NeedsLogin)
			waitFor(t, func() bool { return b.LoginCalls() == 1 }, "StartLoginInteractive after expiry")
		})
	}
}
