// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package tsnode_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/tsnode"
	"github.com/wingedpig/trellis/internal/tsnode/tsnodetest"
	"tailscale.com/ipn"
)

var fast = tsnode.Timings{
	RefreshInterval:   time.Hour,
	BackoffMin:        5 * time.Millisecond,
	BackoffMax:        40 * time.Millisecond,
	WatchHealthyReset: time.Hour,
	LoginURLWait:      50 * time.Millisecond,
	CertLogInterval:   time.Hour,
	KeyExpiryWarn:     14 * 24 * time.Hour,
	KeyExpiryLogEvery: 24 * time.Hour,
}

type logBuf struct {
	mu    sync.Mutex
	lines []string
}

func (l *logBuf) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logBuf) count(substr string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

type harness struct {
	t   *testing.T
	reg *tsnode.Registry
	fac *tsnodetest.Factory
	log *logBuf
}

func nodeCfg(t *testing.T, hostname string) config.TailscaleNodeConfig {
	return config.TailscaleNodeConfig{Hostname: hostname, StateDir: filepath.Join(t.TempDir(), hostname)}
}

func newHarness(t *testing.T, tm tsnode.Timings, nodes ...config.TailscaleNodeConfig) *harness {
	t.Helper()
	h := &harness{t: t, fac: tsnodetest.NewFactory(), log: &logBuf{}}
	var refs []string
	for _, n := range nodes {
		refs = append(refs, n.Hostname)
	}
	reg, err := tsnode.NewRegistry(nodes, refs, h.fac.Backend, tsnode.WithTimings(tm), tsnode.WithLogf(h.log.logf))
	require.NoError(t, err)
	h.reg = reg
	for _, n := range nodes {
		h.watchTeardown(n.Hostname)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = reg.Shutdown(ctx)
		for _, n := range nodes {
			f := h.fac.Get(n.Hostname)
			assert.Empty(t, f.Violations(), "backend contract violations for %s", n.Hostname)
			assert.LessOrEqual(t, f.CloseCount(), 1)
			assert.LessOrEqual(t, tsnode.LockReleases(reg, n.Hostname), 1)
		}
	})
	return h
}

// watchTeardown asserts, inside Close, that the state lock is still held
// (i.e. the lock is released only after Close).
func (h *harness) watchTeardown(hostname string) {
	f := h.fac.Get(hostname)
	reg := h.reg
	t := h.t
	f.SetCloseHook(func() {
		assert.Equal(t, 0, tsnode.LockReleases(reg, hostname), "lock released before Close")
		assert.True(t, lockHeld(f.Cfg.StateDir), "lock not held during Close")
	})
}

func (h *harness) fake(hostname string) *tsnodetest.Fake { return h.fac.Get(hostname) }

func (h *harness) status(hostname string) tsnode.NodeStatus {
	for _, s := range h.reg.Status() {
		if s.Hostname == hostname {
			return s
		}
	}
	h.t.Fatalf("no status for %s", hostname)
	return tsnode.NodeStatus{}
}

func (h *harness) waitState(hostname string, want tsnode.State) {
	h.t.Helper()
	waitFor(h.t, func() bool { return h.status(hostname).State == want }, "state "+string(want))
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type lnResult struct {
	ln  net.Listener
	err error
}

func (h *harness) listenAsync(ctx context.Context, hostname string) <-chan lnResult {
	ch := make(chan lnResult, 1)
	go func() {
		ln, err := h.reg.Listener(ctx, hostname, ":443")
		ch <- lnResult{ln, err}
	}()
	return ch
}

func pending(t *testing.T, ch <-chan lnResult) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("listener returned early: %v %v", r.ln, r.err)
	case <-time.After(30 * time.Millisecond):
	}
}

func recv(t *testing.T, ch <-chan lnResult) lnResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("listener did not return")
		return lnResult{}
	}
}

// lockHeld reports whether dir's lock is held by someone else.
func lockHeld(dir string) bool {
	f, err := os.OpenFile(filepath.Join(dir, ".trellis.lock"), os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// handshake dials ln and completes a TLS handshake, returning its error.
func handshake(t *testing.T, ln net.Listener) error {
	t.Helper()
	fl := ln.Addr().String()
	accepted := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.(*tls.Conn).Handshake()
			c.Close()
		}
		close(accepted)
	}()
	conn, err := tls.Dial("tcp", fl, &tls.Config{InsecureSkipVerify: true})
	if err == nil {
		conn.Close()
	}
	<-accepted
	return err
}

// --- construction ---

func TestNewRegistryStructuralErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	realParent := filepath.Join(home, "real")
	require.NoError(t, os.MkdirAll(realParent, 0o700))
	link := filepath.Join(home, "link")
	require.NoError(t, os.Symlink(realParent, link))

	cases := []struct {
		name  string
		nodes []config.TailscaleNodeConfig
		refs  []string
		want  string
	}{
		{"missing hostname", []config.TailscaleNodeConfig{{}}, nil, "is required"},
		{"invalid hostname", []config.TailscaleNodeConfig{{Hostname: "Bad_Name"}}, nil, "invalid hostname"},
		{"leading hyphen", []config.TailscaleNodeConfig{{Hostname: "-x"}}, nil, "invalid hostname"},
		{"duplicate hostname", []config.TailscaleNodeConfig{{Hostname: "a", StateDir: home + "/a"}, {Hostname: "a", StateDir: home + "/b"}}, nil, "duplicate"},
		{"tilde vs absolute", []config.TailscaleNodeConfig{{Hostname: "a", StateDir: "~/s"}, {Hostname: "b", StateDir: home + "/s"}}, nil, "also used by"},
		{"trailing slash", []config.TailscaleNodeConfig{{Hostname: "a", StateDir: home + "/s/"}, {Hostname: "b", StateDir: home + "/s"}}, nil, "also used by"},
		{"symlinked parent", []config.TailscaleNodeConfig{{Hostname: "a", StateDir: link + "/n"}, {Hostname: "b", StateDir: realParent + "/n"}}, nil, "also used by"},
		{"nested", []config.TailscaleNodeConfig{{Hostname: "a", StateDir: home + "/s"}, {Hostname: "b", StateDir: home + "/s/inner"}}, nil, "nested"},
		{"default dirs collide with explicit", []config.TailscaleNodeConfig{{Hostname: "a"}, {Hostname: "b", StateDir: "~/.trellis/tsnet/a"}}, nil, "also used by"},
		{"undeclared reference", []config.TailscaleNodeConfig{{Hostname: "a", StateDir: home + "/a"}}, []string{"a", "zzz"}, "undeclared"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fac := tsnodetest.NewFactory()
			_, err := tsnode.NewRegistry(tc.nodes, tc.refs, fac.Backend)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Empty(t, fac.Fakes, "factory must not be called on invalid config")
		})
	}
}

func TestUnreferencedNodeNotCreated(t *testing.T) {
	fac := tsnodetest.NewFactory()
	reg, err := tsnode.NewRegistry([]config.TailscaleNodeConfig{nodeCfg(t, "used"), nodeCfg(t, "unused")}, []string{"used"}, fac.Backend)
	require.NoError(t, err)
	assert.NotNil(t, fac.Get("used"))
	assert.Nil(t, fac.Get("unused"))
	assert.True(t, reg.Has("used"))
	assert.False(t, reg.Has("unused"))
	require.NoError(t, reg.Shutdown(context.Background()))
}

// --- lifecycle and readiness ---

func TestHappyPath(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	h.reg.Start()
	f := h.fake("ent")
	ch := h.listenAsync(context.Background(), "ent")
	f.SendState(ipn.Running)

	r := recv(t, ch)
	require.NoError(t, r.err)
	h.waitState("ent", tsnode.StateRunning)
	assert.Equal(t, 1, f.ClearServeCalls())
	st := h.status("ent")
	assert.Equal(t, "ent.tailnet.ts.net", st.FQDN)
	assert.Equal(t, []string{"100.64.0.9"}, st.TailscaleIPs)
	assert.False(t, st.HostnameMismatch)

	require.NoError(t, handshake(t, r.ln))
	assert.NotNil(t, h.status("ent").CertNotAfter)
}

func TestLateLogin(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	h.reg.Start()
	f := h.fake("ent")
	ch := h.listenAsync(context.Background(), "ent")

	url := "https://login.tailscale.com/a/abc"
	s := ipn.NeedsLogin
	f.Send(ipn.Notify{State: &s, BrowseToURL: &url})
	h.waitState("ent", tsnode.StateNeedsLogin)
	assert.Equal(t, url, h.status("ent").LoginURL)
	assert.Positive(t, h.log.count("NEEDS LOGIN — visit "+url))
	pending(t, ch)

	f.SendState(ipn.Running)
	r := recv(t, ch)
	require.NoError(t, r.err)
	h.waitState("ent", tsnode.StateRunning)
	assert.Empty(t, h.status("ent").LoginURL)
}

func TestPrerequisitesEnabledByNetMap(t *testing.T) {
	for _, tc := range []struct {
		name string
		off  func(*tsnodetest.FakeStatus)
		on   func(*tsnodetest.FakeStatus)
		hint string
	}{
		{"magicdns", func(s *tsnodetest.FakeStatus) { s.MagicDNSEnabled = false }, func(s *tsnodetest.FakeStatus) { s.MagicDNSEnabled = true }, "MagicDNS"},
		{"https", func(s *tsnodetest.FakeStatus) { s.CertDomains = nil }, func(s *tsnodetest.FakeStatus) { s.CertDomains = []string{"ent.tailnet.ts.net"} }, "enable HTTPS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, fast, nodeCfg(t, "ent"))
			f := h.fake("ent")
			f.UpdateStatus(tc.off)
			h.reg.Start()
			ch := h.listenAsync(context.Background(), "ent")
			f.SendState(ipn.Running)
			h.waitState("ent", tsnode.StateError)
			assert.Contains(t, h.status("ent").LastError, tc.hint)
			pending(t, ch)

			f.UpdateStatus(tc.on)
			f.Send(tsnodetest.NetMapNotify()) // no State field
			require.NoError(t, recv(t, ch).err)
			h.waitState("ent", tsnode.StateRunning)
			assert.Empty(t, h.status("ent").LastError)
		})
	}
}

func TestPrerequisitesEnabledByPeriodicRefresh(t *testing.T) {
	tm := fast
	tm.RefreshInterval = 20 * time.Millisecond
	h := newHarness(t, tm, nodeCfg(t, "ent"))
	f := h.fake("ent")
	f.UpdateStatus(func(s *tsnodetest.FakeStatus) { s.MagicDNSEnabled = false })
	h.reg.Start()
	ch := h.listenAsync(context.Background(), "ent")
	f.SendState(ipn.Running)
	h.waitState("ent", tsnode.StateError)
	pending(t, ch)

	f.UpdateStatus(func(s *tsnodetest.FakeStatus) { s.MagicDNSEnabled = true })
	require.NoError(t, recv(t, ch).err)
	h.waitState("ent", tsnode.StateRunning)
}

func TestReadinessNotRevoked(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	h.reg.Start()
	f := h.fake("ent")
	ch := h.listenAsync(context.Background(), "ent")
	f.SendState(ipn.Running)
	r := recv(t, ch)
	require.NoError(t, r.err)
	h.waitState("ent", tsnode.StateRunning)

	f.UpdateStatus(func(s *tsnodetest.FakeStatus) { s.MagicDNSEnabled = false })
	f.Send(tsnodetest.NetMapNotify())
	h.waitState("ent", tsnode.StateError)
	assert.False(t, f.Listeners()[0].Closed())

	// A second listener is still granted: readiness is not revoked.
	r2 := recv(t, h.listenAsync(context.Background(), "ent"))
	require.NoError(t, r2.err)
	r2.ln.Close()

	f.UpdateStatus(func(s *tsnodetest.FakeStatus) { s.MagicDNSEnabled = true })
	f.Send(tsnodetest.NetMapNotify())
	h.waitState("ent", tsnode.StateRunning)
	assert.Equal(t, 1, f.ClearServeCalls())
}

func TestReadinessChecksCoalesce(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	f := h.fake("ent")
	f.SetStatusDelay(20 * time.Millisecond)
	h.reg.Start()
	f.SendState(ipn.Running)
	time.Sleep(5 * time.Millisecond) // first check in flight
	for i := 0; i < 50; i++ {
		f.Send(tsnodetest.NetMapNotify())
	}
	h.waitState("ent", tsnode.StateRunning)
	time.Sleep(80 * time.Millisecond)
	assert.Equal(t, 1, f.MaxStatusInFlight(), "readiness checks overlapped")
	assert.LessOrEqual(t, f.StatusCalls(), 3, "burst should coalesce into at most one more check")
}

func TestBackendErrorThenRecovery(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	h.reg.Start()
	f := h.fake("ent")
	ch := h.listenAsync(context.Background(), "ent")
	msg := "control unreachable"
	f.Send(ipn.Notify{ErrMessage: &msg})
	waitFor(t, func() bool { return strings.Contains(h.status("ent").LastError, msg) }, "LastError")
	pending(t, ch)

	f.SendState(ipn.Running)
	require.NoError(t, recv(t, ch).err)
	h.waitState("ent", tsnode.StateRunning)
}

func TestWatcherFailureThenRecovery(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	f := h.fake("ent")
	f.FailWatch(errors.New("bus down 1"), errors.New("bus down 2"))
	h.reg.Start()
	ch := h.listenAsync(context.Background(), "ent")

	require.NoError(t, f.WaitWatcher(3*time.Second))
	assert.Equal(t, 3, f.WatchCalls())
	assert.Contains(t, h.status("ent").LastError, "bus down")
	pending(t, ch)

	// Next errors mid-stream; the watcher is re-established.
	f.BreakWatcher(errors.New("stream reset"))
	waitFor(t, func() bool { return f.WatchCalls() == 4 }, "watcher re-established")
	require.NoError(t, f.WaitWatcher(3*time.Second))
	pending(t, ch)

	f.SendState(ipn.Running)
	r := recv(t, ch)
	require.NoError(t, r.err, "no waiter receives an error")
	h.waitState("ent", tsnode.StateRunning)

	// Reconnecting re-reads the initial state (now Running).
	f.BreakWatcher(errors.New("stream reset again"))
	waitFor(t, func() bool { return f.WatchCalls() == 5 }, "second reconnect")
	h.waitState("ent", tsnode.StateRunning)
}

func TestStartErrorFailsTerminally(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	f := h.fake("ent")
	f.SetStartErr(errors.New("boom"))
	h.reg.Start()
	r := recv(t, h.listenAsync(context.Background(), "ent"))
	require.ErrorIs(t, r.err, tsnode.ErrNodeFailed)
	assert.Contains(t, r.err.Error(), "boom")
	h.waitState("ent", tsnode.StateFailed)
	waitFor(t, func() bool { return tsnode.LockReleases(h.reg, "ent") == 1 }, "lock released")
	assert.Equal(t, 0, f.CloseCount())
	assert.False(t, lockHeld(f.Cfg.StateDir))
}

func TestLockContention(t *testing.T) {
	cfg := nodeCfg(t, "ent")
	h1 := newHarness(t, fast, cfg)
	h1.reg.Start()
	h1.fake("ent").SendState(ipn.Running)
	h1.waitState("ent", tsnode.StateRunning)

	h2 := newHarness(t, fast, cfg)
	h2.reg.Start()
	r := recv(t, h2.listenAsync(context.Background(), "ent"))
	require.ErrorIs(t, r.err, tsnode.ErrNodeFailed)
	h2.waitState("ent", tsnode.StateFailed)
	assert.Contains(t, h2.status("ent").LastError, "in use by another process")
	assert.False(t, h2.fake("ent").StartCalled())

	assert.Equal(t, tsnode.StateRunning, h1.status("ent").State)
	require.NoError(t, recv(t, h1.listenAsync(context.Background(), "ent")).err)
}

func TestExpiryAndRecovery(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	h.reg.Start()
	f := h.fake("ent")
	f.SendState(ipn.Running)
	r := recv(t, h.listenAsync(context.Background(), "ent"))
	require.NoError(t, r.err)
	h.waitState("ent", tsnode.StateRunning)

	f.SendState(ipn.NeedsLogin)
	h.waitState("ent", tsnode.StateNeedsLogin)
	waitFor(t, func() bool { return f.LoginCalls() == 1 }, "StartLoginInteractive")

	url := "https://login.tailscale.com/a/reauth"
	f.Send(ipn.Notify{BrowseToURL: &url})
	waitFor(t, func() bool { return h.status("ent").LoginURL == url }, "login URL")
	assert.False(t, f.Listeners()[0].Closed())

	f.SendState(ipn.Running)
	h.waitState("ent", tsnode.StateRunning)
	assert.Empty(t, h.status("ent").LoginURL)
	time.Sleep(3 * fast.LoginURLWait)
	assert.Equal(t, 1, f.LoginCalls())
	assert.False(t, f.Listeners()[0].Closed())
}

func TestCertificateFailureAndRecovery(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	h.reg.Start()
	f := h.fake("ent")
	f.FailCert(errors.New("acme: rate limited"))
	f.SendState(ipn.Running)
	r := recv(t, h.listenAsync(context.Background(), "ent"))
	require.NoError(t, r.err)

	require.Error(t, handshake(t, r.ln))
	st := h.status("ent")
	assert.Contains(t, st.CertError, "rate limited")
	assert.NotNil(t, st.CertErrorAt)

	require.NoError(t, handshake(t, r.ln))
	require.NoError(t, handshake(t, r.ln))
	st = h.status("ent")
	assert.Empty(t, st.CertError)
	assert.NotNil(t, st.CertNotAfter)
	assert.Equal(t, 1, h.log.count("certificate recovered"))
}

func TestHostnameMismatch(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	f := h.fake("ent")
	f.UpdateStatus(func(s *tsnodetest.FakeStatus) { s.DNSName = "ent-1.tailnet.ts.net." })
	h.reg.Start()
	f.SendState(ipn.Running)
	require.NoError(t, recv(t, h.listenAsync(context.Background(), "ent")).err)
	h.waitState("ent", tsnode.StateRunning)
	st := h.status("ent")
	assert.True(t, st.HostnameMismatch)
	assert.Equal(t, "ent-1.tailnet.ts.net", st.FQDN)

	f.Send(tsnodetest.NetMapNotify())
	f.Send(tsnodetest.NetMapNotify())
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, 1, h.log.count("ent-1.tailnet.ts.net but the configured hostname is ent"))
}

func TestClearServeConfigRetry(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	f := h.fake("ent")
	f.FailClearServe(errors.New("localapi 500"), errors.New("localapi 500"))
	h.reg.Start()
	ch := h.listenAsync(context.Background(), "ent")
	f.SendState(ipn.Running)

	h.waitState("ent", tsnode.StateError)
	assert.Contains(t, h.status("ent").LastError, "clearing serve config")

	// Retries happen on the backoff schedule with no further notifications.
	r := recv(t, ch)
	require.NoError(t, r.err)
	h.waitState("ent", tsnode.StateRunning)
	assert.Equal(t, 3, f.ClearServeCalls())
	assert.Empty(t, h.status("ent").LastError)

	f.Send(tsnodetest.NetMapNotify())
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, 3, f.ClearServeCalls())
}

// --- shutdown ---

func TestShutdownWhileLoginPending(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	h.reg.Start()
	f := h.fake("ent")
	f.SendState(ipn.NeedsLogin)
	h.waitState("ent", tsnode.StateNeedsLogin)
	ch := h.listenAsync(context.Background(), "ent")
	pending(t, ch)

	h.reg.BeginShutdown()
	assert.ErrorIs(t, recv(t, ch).err, tsnode.ErrShuttingDown)
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, 0, f.CloseCount(), "no cleanup before releaseNodes")
	assert.Equal(t, 0, tsnode.LockReleases(h.reg, "ent"))

	require.NoError(t, h.reg.Shutdown(context.Background()))
	assert.Equal(t, 1, f.CloseCount())
	assert.Equal(t, 1, tsnode.LockReleases(h.reg, "ent"))
	assert.False(t, lockHeld(f.Cfg.StateDir))
}

func TestLoginCompletesAfterBeginShutdown(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	h.reg.Start()
	f := h.fake("ent")
	f.SendState(ipn.NeedsLogin)
	h.waitState("ent", tsnode.StateNeedsLogin)
	h.reg.BeginShutdown()

	// Monitor has stopped; even if readiness fired now, no listener.
	_, err := h.reg.Listener(context.Background(), "ent", ":443")
	assert.ErrorIs(t, err, tsnode.ErrShuttingDown)
	assert.Empty(t, f.Listeners())
	require.NoError(t, h.reg.Shutdown(context.Background()))
}

func TestShutdownDuringStart(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	f := h.fake("ent")
	f.HoldStart()
	h.reg.Start()
	waitFor(t, f.StartCalled, "Start called")

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		done <- h.reg.Shutdown(ctx)
	}()
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, 0, f.CloseCount())
	f.ReleaseStart()

	require.NoError(t, <-done)
	assert.Equal(t, []string{"Start", "Start-returned", "Close"}, f.Calls())
	assert.Equal(t, 1, tsnode.LockReleases(h.reg, "ent"))
	assert.Equal(t, 0, f.WatchCalls(), "monitor never started")
}

func TestShutdownDeadlineWhileStartStuck(t *testing.T) {
	stuckCfg, okCfg := nodeCfg(t, "stuck"), nodeCfg(t, "ok")
	h := newHarness(t, fast, stuckCfg, okCfg)
	stuck, ok := h.fake("stuck"), h.fake("ok")
	stuck.HoldStart()
	h.reg.Start()
	ok.SendState(ipn.Running)
	h.waitState("ok", tsnode.StateRunning)
	waitFor(t, stuck.StartCalled, "stuck Start called")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := h.reg.Shutdown(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stuck")
	assert.NotContains(t, err.Error(), "ok")
	assert.Equal(t, 0, stuck.CloseCount())
	assert.Equal(t, 1, ok.CloseCount())
	assert.Equal(t, 1, tsnode.LockReleases(h.reg, "ok"))

	// The stuck node's lock is still held.
	h2 := newHarness(t, fast, stuckCfg)
	h2.reg.Start()
	h2.waitState("stuck", tsnode.StateFailed)

	stuck.ReleaseStart()
	waitFor(t, func() bool { return tsnode.LockReleases(h.reg, "stuck") == 1 }, "stuck node cleaned up")
	assert.Equal(t, 1, stuck.CloseCount())
	assert.Equal(t, []string{"Start", "Start-returned", "Close"}, stuck.Calls())
}

func TestShutdownIdempotence(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	h.reg.Start()
	f := h.fake("ent")
	f.SendState(ipn.Running)
	h.waitState("ent", tsnode.StateRunning)
	h.reg.BeginShutdown()
	h.reg.BeginShutdown()
	require.NoError(t, h.reg.Shutdown(context.Background()))
	require.NoError(t, h.reg.Shutdown(context.Background()))
	tsnode.CleanupNode(h.reg, "ent")
	assert.Equal(t, 1, f.CloseCount())
	assert.Equal(t, 1, tsnode.LockReleases(h.reg, "ent"))

	// Shutdown without a prior BeginShutdown.
	h2 := newHarness(t, fast, nodeCfg(t, "other"))
	h2.reg.Start()
	h2.fake("other").SendState(ipn.Running)
	h2.waitState("other", tsnode.StateRunning)
	require.NoError(t, h2.reg.Shutdown(context.Background()))
	assert.Equal(t, 1, h2.fake("other").CloseCount())
	_, err := h2.reg.Listener(context.Background(), "other", ":443")
	assert.ErrorIs(t, err, tsnode.ErrShuttingDown)
}

func TestShutdownClosesUnservedListeners(t *testing.T) {
	h := newHarness(t, fast, nodeCfg(t, "ent"))
	h.reg.Start()
	f := h.fake("ent")
	f.SendState(ipn.Running)
	require.NoError(t, recv(t, h.listenAsync(context.Background(), "ent")).err)
	require.NoError(t, h.reg.Shutdown(context.Background()))
	for _, l := range f.Listeners() {
		assert.True(t, l.Closed())
	}
}
