// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package tsnodetest provides a scriptable fake tsnode.Backend so lifecycle
// tests never touch a real tailnet.
package tsnodetest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/tsnode"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/netmap"
)

// Fake is a scriptable tsnode.Backend. Zero values give a node that starts
// immediately and whose status reports Running with every prerequisite met.
type Fake struct {
	Cfg config.TailscaleNodeConfig

	mu sync.Mutex

	// Start control.
	startGate     chan struct{} // if non-nil, Start blocks until closed
	startErr      error
	startCalled   bool
	startReturned bool

	// Close bookkeeping.
	closeCount int
	closeHook  func()
	violations []string
	calls      []string

	// Status script.
	St FakeStatus

	// Watcher script.
	ipnState     ipn.State
	watchErrs    []error // returned by successive WatchIPNBus calls
	watchCalls   int
	watcher      *fakeWatcher
	watcherReady chan struct{} // closed/replaced when a watcher attaches

	// Readiness-related counters.
	statusCalls       int
	statusInFlight    int
	statusMaxInFlight int
	statusDelay       time.Duration
	clearServeErrs    []error
	clearServeCalls   int
	loginCalls        int

	// Certificates.
	certErrs []error
	cert     *tls.Certificate

	listeners []*FakeListener
}

// FakeStatus is what StatusWithoutPeers reports.
type FakeStatus struct {
	BackendState    string
	IPs             []string
	MagicDNSEnabled bool
	CertDomains     []string
	DNSName         string
	AuthURL         string
	KeyExpiry       *time.Time
}

// NewFake returns a fake whose status passes every readiness condition.
func NewFake(cfg config.TailscaleNodeConfig) *Fake {
	return &Fake{
		Cfg:      cfg,
		ipnState: ipn.NoState,
		St: FakeStatus{
			BackendState:    "Running",
			IPs:             []string{"100.64.0.9"},
			MagicDNSEnabled: true,
			CertDomains:     []string{cfg.Hostname + ".tailnet.ts.net"},
			DNSName:         cfg.Hostname + ".tailnet.ts.net.",
		},
		watcherReady: make(chan struct{}),
	}
}

// Factory returns a BackendFactory that records the fakes it creates.
type Factory struct {
	mu    sync.Mutex
	Fakes map[string]*Fake
	Setup func(*Fake) // optional, runs on each new fake
}

func NewFactory() *Factory { return &Factory{Fakes: make(map[string]*Fake)} }

func (f *Factory) Backend(cfg config.TailscaleNodeConfig) tsnode.Backend {
	fk := NewFake(cfg)
	if f.Setup != nil {
		f.Setup(fk)
	}
	f.mu.Lock()
	f.Fakes[cfg.Hostname] = fk
	f.mu.Unlock()
	return fk
}

func (f *Factory) Get(hostname string) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Fakes[hostname]
}

// --- scripting ---

// HoldStart makes Start block until ReleaseStart is called.
func (f *Fake) HoldStart() {
	f.mu.Lock()
	f.startGate = make(chan struct{})
	f.mu.Unlock()
}

func (f *Fake) ReleaseStart() {
	f.mu.Lock()
	g := f.startGate
	f.mu.Unlock()
	if g != nil {
		close(g)
	}
}

func (f *Fake) SetStartErr(err error) { f.mu.Lock(); f.startErr = err; f.mu.Unlock() }

// SetCloseHook runs fn inside Close (e.g. to assert the lock is still held).
func (f *Fake) SetCloseHook(fn func()) { f.mu.Lock(); f.closeHook = fn; f.mu.Unlock() }

// FailWatch makes the next len(errs) WatchIPNBus calls fail.
func (f *Fake) FailWatch(errs ...error) {
	f.mu.Lock()
	f.watchErrs = append(f.watchErrs, errs...)
	f.mu.Unlock()
}

func (f *Fake) FailClearServe(errs ...error) {
	f.mu.Lock()
	f.clearServeErrs = append(f.clearServeErrs, errs...)
	f.mu.Unlock()
}

func (f *Fake) FailCert(errs ...error) {
	f.mu.Lock()
	f.certErrs = append(f.certErrs, errs...)
	f.mu.Unlock()
}

func (f *Fake) SetStatusDelay(d time.Duration) { f.mu.Lock(); f.statusDelay = d; f.mu.Unlock() }

// UpdateStatus mutates the scripted status.
func (f *Fake) UpdateStatus(fn func(*FakeStatus)) {
	f.mu.Lock()
	fn(&f.St)
	f.mu.Unlock()
}

// WaitWatcher blocks until a watcher is attached.
func (f *Fake) WaitWatcher(timeout time.Duration) error {
	deadline := time.After(timeout)
	for {
		f.mu.Lock()
		w, ready := f.watcher, f.watcherReady
		f.mu.Unlock()
		if w != nil {
			return nil
		}
		select {
		case <-ready:
		case <-deadline:
			return errors.New("no watcher attached")
		}
	}
}

// Send delivers a notification to the current watcher. A State in n also
// becomes the fake's IPN state (reported to later watchers' initial state).
func (f *Fake) Send(n ipn.Notify) {
	if err := f.WaitWatcher(2 * time.Second); err != nil {
		panic(err)
	}
	f.mu.Lock()
	if n.State != nil {
		f.ipnState = *n.State
	}
	w := f.watcher
	f.mu.Unlock()
	w.ch <- n
}

// SendState is Send with only State set.
func (f *Fake) SendState(s ipn.State) { f.Send(ipn.Notify{State: &s}) }

// BreakWatcher makes the current watcher's Next return err.
func (f *Fake) BreakWatcher(err error) {
	if e := f.WaitWatcher(2 * time.Second); e != nil {
		panic(e)
	}
	f.mu.Lock()
	w := f.watcher
	f.watcher = nil
	f.mu.Unlock()
	w.errCh <- err
}

// --- observations ---

func (f *Fake) CloseCount() int        { f.mu.Lock(); defer f.mu.Unlock(); return f.closeCount }
func (f *Fake) StartCalled() bool      { f.mu.Lock(); defer f.mu.Unlock(); return f.startCalled }
func (f *Fake) WatchCalls() int        { f.mu.Lock(); defer f.mu.Unlock(); return f.watchCalls }
func (f *Fake) LoginCalls() int        { f.mu.Lock(); defer f.mu.Unlock(); return f.loginCalls }
func (f *Fake) ClearServeCalls() int   { f.mu.Lock(); defer f.mu.Unlock(); return f.clearServeCalls }
func (f *Fake) StatusCalls() int       { f.mu.Lock(); defer f.mu.Unlock(); return f.statusCalls }
func (f *Fake) MaxStatusInFlight() int { f.mu.Lock(); defer f.mu.Unlock(); return f.statusMaxInFlight }
func (f *Fake) Violations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.violations...)
}
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}
func (f *Fake) Listeners() []*FakeListener {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*FakeListener(nil), f.listeners...)
}

// --- Backend ---

func (f *Fake) Start() error {
	f.mu.Lock()
	if f.closeCount > 0 {
		f.violations = append(f.violations, "Start after Close")
	}
	f.startCalled = true
	f.calls = append(f.calls, "Start")
	gate := f.startGate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startReturned = true
	f.calls = append(f.calls, "Start-returned")
	return f.startErr
}

func (f *Fake) Close() error {
	f.mu.Lock()
	if !f.startReturned {
		f.violations = append(f.violations, "Close before or during Start")
	}
	f.closeCount++
	if f.closeCount > 1 {
		f.violations = append(f.violations, "Close called twice")
	}
	f.calls = append(f.calls, "Close")
	hook := f.closeHook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

func (f *Fake) Listen(network, addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	fl := &FakeListener{Listener: ln, Requested: addr}
	f.mu.Lock()
	f.listeners = append(f.listeners, fl)
	f.calls = append(f.calls, "Listen")
	f.mu.Unlock()
	return fl, nil
}

func (f *Fake) GetCertificate(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.certErrs) > 0 {
		err := f.certErrs[0]
		f.certErrs = f.certErrs[1:]
		return nil, err
	}
	if f.cert == nil {
		c, err := selfSigned(f.Cfg.Hostname + ".tailnet.ts.net")
		if err != nil {
			return nil, err
		}
		f.cert = c
	}
	return f.cert, nil
}

func (f *Fake) WatchIPNBus(ctx context.Context, mask ipn.NotifyWatchOpt) (tsnode.IPNWatcher, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.watchCalls++
	if len(f.watchErrs) > 0 {
		err := f.watchErrs[0]
		f.watchErrs = f.watchErrs[1:]
		return nil, err
	}
	w := &fakeWatcher{ctx: ctx, ch: make(chan ipn.Notify, 256), errCh: make(chan error, 1), closed: make(chan struct{})}
	if mask&ipn.NotifyInitialState != 0 {
		s := f.ipnState
		w.ch <- ipn.Notify{State: &s}
	}
	f.watcher = w
	close(f.watcherReady)
	f.watcherReady = make(chan struct{})
	return w, nil
}

func (f *Fake) StatusWithoutPeers(ctx context.Context) (*ipnstate.Status, error) {
	f.mu.Lock()
	f.statusCalls++
	f.statusInFlight++
	if f.statusInFlight > f.statusMaxInFlight {
		f.statusMaxInFlight = f.statusInFlight
	}
	delay := f.statusDelay
	s := f.St
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.statusInFlight--
		f.mu.Unlock()
	}()
	if delay > 0 {
		time.Sleep(delay)
	}
	st := &ipnstate.Status{
		BackendState:   s.BackendState,
		AuthURL:        s.AuthURL,
		CertDomains:    s.CertDomains,
		CurrentTailnet: &ipnstate.TailnetStatus{MagicDNSEnabled: s.MagicDNSEnabled},
		Self:           &ipnstate.PeerStatus{DNSName: s.DNSName, KeyExpiry: s.KeyExpiry},
	}
	for _, ip := range s.IPs {
		st.TailscaleIPs = append(st.TailscaleIPs, netip.MustParseAddr(ip))
	}
	return st, nil
}

func (f *Fake) StartLoginInteractive(ctx context.Context) error {
	f.mu.Lock()
	f.loginCalls++
	f.calls = append(f.calls, "StartLoginInteractive")
	f.mu.Unlock()
	return nil
}

func (f *Fake) ClearServeConfig(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearServeCalls++
	f.calls = append(f.calls, "ClearServeConfig")
	if len(f.clearServeErrs) > 0 {
		err := f.clearServeErrs[0]
		f.clearServeErrs = f.clearServeErrs[1:]
		return err
	}
	return nil
}

// --- watcher ---

type fakeWatcher struct {
	ctx       context.Context
	ch        chan ipn.Notify
	errCh     chan error
	closed    chan struct{}
	closeOnce sync.Once
}

func (w *fakeWatcher) Next() (ipn.Notify, error) {
	select {
	case n := <-w.ch:
		return n, nil
	case err := <-w.errCh:
		return ipn.Notify{}, err
	case <-w.closed:
		return ipn.Notify{}, errors.New("watcher closed")
	case <-w.ctx.Done():
		return ipn.Notify{}, w.ctx.Err()
	}
}

func (w *fakeWatcher) Close() error {
	w.closeOnce.Do(func() { close(w.closed) })
	return nil
}

// --- listener ---

// FakeListener is a loopback listener standing in for a netstack listener.
type FakeListener struct {
	net.Listener
	Requested string

	mu     sync.Mutex
	closed int
}

func (l *FakeListener) Close() error {
	l.mu.Lock()
	l.closed++
	l.mu.Unlock()
	return l.Listener.Close()
}

// Closed reports whether Close was called.
func (l *FakeListener) Closed() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.closed > 0 }

// NetMapNotify returns a notification carrying only a (non-nil) NetMap.
func NetMapNotify() ipn.Notify {
	return ipn.Notify{NetMap: &netmap.NetworkMap{}}
}

// selfSigned returns a self-signed certificate for host.
func selfSigned(host string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create cert: %w", err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
