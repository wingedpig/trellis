// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package tsnode

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wingedpig/trellis/internal/config"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

// State is a node's lifecycle state.
type State string

const (
	StateStarting      State = "starting"
	StateNeedsLogin    State = "needs-login"
	StateNeedsApproval State = "needs-approval"
	StateRunning       State = "running"
	StateStopped       State = "stopped"
	StateError         State = "error"  // recoverable
	StateFailed        State = "failed" // terminal
)

// NodeStatus is a snapshot of one node, as served by the API.
type NodeStatus struct {
	Hostname         string     `json:"hostname"`
	StateDir         string     `json:"state_dir"`
	State            State      `json:"state"`
	FQDN             string     `json:"fqdn,omitempty"`
	HostnameMismatch bool       `json:"hostname_mismatch"`
	TailscaleIPs     []string   `json:"tailscale_ips,omitempty"`
	LoginURL         string     `json:"login_url,omitempty"`
	KeyExpiry        *time.Time `json:"key_expiry,omitempty"`
	KeyExpiresSoon   bool       `json:"key_expires_soon"`
	CertError        string     `json:"cert_error,omitempty"`
	CertErrorAt      *time.Time `json:"cert_error_at,omitempty"`
	CertNotAfter     *time.Time `json:"cert_not_after,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
}

// errKind classifies LastError so a successful readiness check clears only
// the errors it has resolved.
type errKind int

const (
	errOther errKind = iota
	errPrereq
	errApproval
	errServeConfig
)

const (
	hintMagicDNS = "tsnet: you must enable MagicDNS in the DNS page of the admin panel to proceed. See https://tailscale.com/s/https"
	hintHTTPS    = "tsnet: you must enable HTTPS in the admin panel to proceed. See https://tailscale.com/s/https"
	hintApproval = "approve this machine in the Tailscale admin console (Machines page)"
)

type node struct {
	reg     *Registry
	cfg     config.TailscaleNodeConfig // StateDir is canonical
	backend Backend

	ready  chan struct{} // closed once, when first initialized and ready
	failed chan struct{} // closed on terminal failure
	exited chan struct{} // closed when the supervisor goroutine returns

	checkReq     chan struct{} // coalesced readiness-check requests (cap 1)
	loginEntered chan uint64   // generation of each entry into needs-login (cap 1)

	mu               sync.Mutex
	st               NodeStatus
	lastErrKind      errKind
	started          bool
	cleanedUp        bool
	initialized      bool
	readyClosed      bool
	lock             *os.File
	lockReleases     atomic.Int32 // for tests
	ipnGen           uint64
	loginGen         uint64
	lastCertLog      time.Time
	lastExpiryLog    time.Time
	loggedMismatchOf string
}

func newNode(r *Registry, cfg config.TailscaleNodeConfig, be Backend) *node {
	return &node{
		reg:          r,
		cfg:          cfg,
		backend:      be,
		ready:        make(chan struct{}),
		failed:       make(chan struct{}),
		exited:       make(chan struct{}),
		checkReq:     make(chan struct{}, 1),
		loginEntered: make(chan uint64, 1),
		st: NodeStatus{
			Hostname: cfg.Hostname,
			StateDir: cfg.StateDir,
			State:    StateStarting,
		},
	}
}

func (n *node) logf(format string, args ...any) {
	n.reg.logf("tailscale node "+n.cfg.Hostname+": "+format, args...)
}

func (n *node) status() NodeStatus {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := n.st
	s.TailscaleIPs = append([]string(nil), n.st.TailscaleIPs...)
	return s
}

func (n *node) lastError() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.st.LastError
}

// setStateLocked records a state transition and logs it.
func (n *node) setStateLocked(s State) {
	prev := n.st.State
	if prev == s {
		return
	}
	n.st.State = s
	switch s {
	case StateNeedsLogin:
		if n.st.LoginURL != "" {
			n.logf("NEEDS LOGIN — visit %s to add this node to your tailnet", n.st.LoginURL)
		} else {
			n.logf("state %s -> %s (waiting for login URL)", prev, s)
		}
	case StateNeedsApproval:
		n.logf("state %s -> %s: %s", prev, s, hintApproval)
	case StateError, StateFailed:
		n.logf("state %s -> %s: %s", prev, s, n.st.LastError)
	default:
		n.logf("state %s -> %s", prev, s)
	}
}

func (n *node) setLastErrorLocked(kind errKind, msg string) {
	n.st.LastError = msg
	n.lastErrKind = kind
}

func (n *node) setLastError(kind errKind, msg string) {
	n.mu.Lock()
	n.setLastErrorLocked(kind, msg)
	n.mu.Unlock()
}

// fail marks the node as terminally failed and releases waiters.
func (n *node) fail(err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.setLastErrorLocked(errOther, err.Error())
	n.setStateLocked(StateFailed)
	close(n.failed)
}

// supervise runs the node's whole life: lock, Start, monitor, wait for
// release, cleanup. It is the sole owner of the node's teardown.
func (n *node) supervise(ctx context.Context) {
	lock, err := lockStateDir(n.cfg.StateDir)
	if err != nil {
		n.fail(err)
		return
	}
	n.mu.Lock()
	n.lock = lock
	n.mu.Unlock()

	if err := n.backend.Start(); err != nil {
		n.fail(fmt.Errorf("start: %w", err))
		n.cleanup()
		return
	}
	n.mu.Lock()
	n.started = true
	n.mu.Unlock()

	if ctx.Err() == nil {
		n.monitor(ctx)
	}

	// Node listeners may still be draining HTTP requests after cancellation;
	// closing the tsnet server now would cut them off.
	<-n.reg.releaseNodes
	n.cleanup()
}

// cleanup closes the backend (if started) and then releases the state
// directory lock. It runs at most once.
func (n *node) cleanup() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.cleanedUp {
		return
	}
	if n.started {
		if err := n.backend.Close(); err != nil {
			n.setLastErrorLocked(errOther, "close: "+err.Error())
			n.logf("close: %v", err)
		}
	}
	if n.lock != nil {
		if err := unlockStateDir(n.lock); err != nil {
			n.logf("release state directory lock: %v", err)
		}
		n.lock = nil
		n.lockReleases.Add(1)
	}
	n.cleanedUp = true
}

// monitor is the single consumer of the IPN bus and the single source of
// node state, readiness and login URLs. It returns when ctx is cancelled.
func (n *node) monitor(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); n.readinessLoop(ctx) }()
	go func() { defer wg.Done(); n.refreshLoop(ctx) }()
	go func() { defer wg.Done(); n.loginLoop(ctx) }()
	n.watchLoop(ctx)
	wg.Wait()
}

// sleepCtx sleeps for d or until ctx is done; it reports whether ctx is live.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (n *node) nextBackoff(b time.Duration) time.Duration {
	b *= 2
	if b > n.reg.timings.BackoffMax {
		b = n.reg.timings.BackoffMax
	}
	return b
}

// watchLoop consumes the IPN bus, re-establishing the watcher with backoff
// on errors. Watcher errors are non-terminal.
func (n *node) watchLoop(ctx context.Context) {
	t := n.reg.timings
	backoff := t.BackoffMin
	for ctx.Err() == nil {
		w, err := n.backend.WatchIPNBus(ctx, ipn.NotifyInitialState|ipn.NotifyNoPrivateKeys)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			n.setLastError(errOther, "watching IPN bus: "+err.Error())
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = n.nextBackoff(backoff)
			continue
		}

		connectedAt := time.Now()
		stop := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				w.Close()
			case <-stop:
			}
		}()
		for {
			nt, err := w.Next()
			if err != nil {
				if ctx.Err() == nil {
					n.setLastError(errOther, "IPN bus: "+err.Error())
				}
				break
			}
			n.handleNotify(nt)
		}
		close(stop)
		w.Close()
		if ctx.Err() != nil {
			return
		}
		if time.Since(connectedAt) >= t.WatchHealthyReset {
			backoff = t.BackoffMin
		}
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = n.nextBackoff(backoff)
	}
}

func (n *node) handleNotify(nt ipn.Notify) {
	n.mu.Lock()
	scheduleCheck := false
	if nt.ErrMessage != nil {
		n.setLastErrorLocked(errOther, "backend: "+*nt.ErrMessage)
		n.logf("backend error: %s", *nt.ErrMessage)
	}
	if nt.BrowseToURL != nil && *nt.BrowseToURL != "" && *nt.BrowseToURL != n.st.LoginURL {
		n.st.LoginURL = *nt.BrowseToURL
		if n.st.State == StateNeedsLogin {
			n.logf("NEEDS LOGIN — visit %s to add this node to your tailnet", n.st.LoginURL)
		}
	}
	if nt.State != nil {
		n.ipnGen++
		switch *nt.State {
		case ipn.NoState, ipn.Starting:
			n.setStateLocked(StateStarting)
		case ipn.NeedsLogin:
			if n.st.State != StateNeedsLogin {
				n.loginGen++
				gen := n.loginGen
				select {
				case <-n.loginEntered:
				default:
				}
				n.loginEntered <- gen
			}
			n.setStateLocked(StateNeedsLogin)
		case ipn.NeedsMachineAuth:
			n.setLastErrorLocked(errApproval, hintApproval)
			n.setStateLocked(StateNeedsApproval)
		case ipn.Running:
			n.st.LoginURL = ""
			scheduleCheck = true
		case ipn.Stopped:
			n.setStateLocked(StateStopped)
		}
	}
	if nt.NetMap != nil {
		scheduleCheck = true
	}
	n.mu.Unlock()
	if scheduleCheck {
		n.scheduleCheck()
	}
}

// loginLoop calls StartLoginInteractive once per entry into needs-login if
// no login URL has appeared after LoginURLWait, so re-authentication after
// key expiry yields a URL.
func (n *node) loginLoop(ctx context.Context) {
	var timer *time.Timer
	var timerC <-chan time.Time
	var gen uint64
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case gen = <-n.loginEntered:
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(n.reg.timings.LoginURLWait)
			timerC = timer.C
		case <-timerC:
			timerC = nil
			n.mu.Lock()
			kick := n.loginGen == gen && n.st.State == StateNeedsLogin && n.st.LoginURL == ""
			n.mu.Unlock()
			if !kick {
				continue
			}
			n.logf("no login URL yet; requesting interactive login")
			if err := n.backend.StartLoginInteractive(ctx); err != nil && ctx.Err() == nil {
				n.setLastError(errOther, "start interactive login: "+err.Error())
			}
		}
	}
}

// refreshLoop periodically refreshes status fields and schedules a readiness
// check, catching tailnet setting changes that arrive without notifications.
func (n *node) refreshLoop(ctx context.Context) {
	tk := time.NewTicker(n.reg.timings.RefreshInterval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
		n.mu.Lock()
		gen := n.ipnGen
		n.mu.Unlock()
		st, err := n.backend.StatusWithoutPeers(ctx)
		if err != nil {
			if ctx.Err() == nil {
				n.setLastError(errOther, "status: "+err.Error())
			}
			continue
		}
		n.mu.Lock()
		// Discard a snapshot the IPN state moved past while it was in flight;
		// it could restore a login URL a newer Running already cleared.
		if n.ipnGen == gen {
			n.applyStatusLocked(st)
		}
		n.mu.Unlock()
		n.scheduleCheck()
	}
}

// applyStatusLocked updates the status fields derived from StatusWithoutPeers.
func (n *node) applyStatusLocked(st *ipnstate.Status) {
	ips := make([]string, 0, len(st.TailscaleIPs))
	for _, ip := range st.TailscaleIPs {
		ips = append(ips, ip.String())
	}
	n.st.TailscaleIPs = ips
	if st.Self != nil {
		if fqdn := strings.TrimSuffix(st.Self.DNSName, "."); fqdn != "" {
			n.st.FQDN = fqdn
		}
		n.st.KeyExpiry = nil
		n.st.KeyExpiresSoon = false
		if st.Self.KeyExpiry != nil && !st.Self.KeyExpiry.IsZero() {
			exp := *st.Self.KeyExpiry
			n.st.KeyExpiry = &exp
			if left := time.Until(exp); left < n.reg.timings.KeyExpiryWarn {
				n.st.KeyExpiresSoon = true
				if time.Since(n.lastExpiryLog) >= n.reg.timings.KeyExpiryLogEvery {
					n.lastExpiryLog = time.Now()
					n.logf("node key expires %s (in %s); disable key expiry for this machine in the Tailscale admin console to avoid re-login", exp.Format(time.RFC3339), left.Round(time.Minute))
				}
			}
		}
	}
	if st.BackendState == ipn.NeedsLogin.String() && n.st.LoginURL == "" && st.AuthURL != "" {
		n.st.LoginURL = st.AuthURL
		if n.st.State == StateNeedsLogin {
			n.logf("NEEDS LOGIN — visit %s to add this node to your tailnet", n.st.LoginURL)
		}
	}
}

// scheduleCheck requests a readiness check. Requests coalesce: at most one
// check runs at a time and requests during a run trigger exactly one more.
func (n *node) scheduleCheck() {
	select {
	case n.checkReq <- struct{}{}:
	default:
	}
}

// readinessLoop runs readiness checks one at a time, plus backoff retries
// while clearing serve config keeps failing during initialization.
func (n *node) readinessLoop(ctx context.Context) {
	backoff := n.reg.timings.BackoffMin
	var retry *time.Timer
	var retryC <-chan time.Time
	defer func() {
		if retry != nil {
			retry.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-n.checkReq:
		case <-retryC:
			retryC = nil
		}
		if serveFailed := n.readinessCheck(ctx); serveFailed && retryC == nil && ctx.Err() == nil {
			retry = time.NewTimer(backoff)
			retryC = retry.C
			backoff = n.nextBackoff(backoff)
		}
	}
}

// readinessCheck evaluates whether the node can serve TLS. It reports true
// if clearing serve config failed during initialization (to be retried).
func (n *node) readinessCheck(ctx context.Context) (serveFailed bool) {
	n.mu.Lock()
	gen := n.ipnGen
	n.mu.Unlock()

	st, err := n.backend.StatusWithoutPeers(ctx)
	if err != nil {
		if ctx.Err() == nil {
			n.setLastError(errOther, "status: "+err.Error())
		}
		return false
	}

	n.mu.Lock()
	if n.ipnGen != gen {
		// The IPN state changed while we were checking; this snapshot may be
		// stale. Discard it and check again rather than overwrite newer state.
		n.mu.Unlock()
		n.scheduleCheck()
		return false
	}
	n.applyStatusLocked(st)
	if st.BackendState != ipn.Running.String() || len(st.TailscaleIPs) == 0 {
		n.mu.Unlock()
		return false
	}
	if st.CurrentTailnet == nil || !st.CurrentTailnet.MagicDNSEnabled {
		n.setLastErrorLocked(errPrereq, hintMagicDNS)
		n.setStateLocked(StateError)
		n.mu.Unlock()
		return false
	}
	if len(st.CertDomains) == 0 {
		n.setLastErrorLocked(errPrereq, hintHTTPS)
		n.setStateLocked(StateError)
		n.mu.Unlock()
		return false
	}
	if n.lastErrKind == errPrereq || n.lastErrKind == errApproval {
		n.setLastErrorLocked(errOther, "")
	}
	n.verifyHostnameLocked(st)
	initialized := n.initialized
	n.mu.Unlock()

	if !initialized {
		if err := n.backend.ClearServeConfig(ctx); err != nil {
			if ctx.Err() != nil {
				return false
			}
			n.mu.Lock()
			n.setLastErrorLocked(errServeConfig, "clearing serve config: "+err.Error())
			if n.ipnGen == gen {
				n.setStateLocked(StateError)
			}
			n.mu.Unlock()
			return true
		}
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.initialized {
		n.initialized = true
		if n.lastErrKind == errServeConfig {
			n.setLastErrorLocked(errOther, "")
		}
	}
	if n.ipnGen != gen {
		// The IPN state changed during ClearServeConfig (e.g. NeedsLogin).
		// Its success stands, but readiness must be re-evaluated before
		// releasing waiters or reporting running.
		n.scheduleCheck()
		return false
	}
	if !n.readyClosed {
		n.readyClosed = true
		close(n.ready)
	}
	n.setStateLocked(StateRunning)
	return false
}

// verifyHostnameLocked records the FQDN and flags a node whose first label
// differs from the configured hostname (e.g. "markbook-ent-1" because a
// stale machine holds the name). The node still serves.
func (n *node) verifyHostnameLocked(st *ipnstate.Status) {
	if st.Self == nil {
		return
	}
	fqdn := strings.TrimSuffix(st.Self.DNSName, ".")
	if fqdn == "" {
		return
	}
	n.st.FQDN = fqdn
	label, _, _ := strings.Cut(fqdn, ".")
	n.st.HostnameMismatch = label != n.cfg.Hostname
	if n.st.HostnameMismatch && n.loggedMismatchOf != fqdn {
		n.loggedMismatchOf = fqdn
		n.logf("ERROR: tailnet assigned %s but the configured hostname is %s — probably a stale machine still holds the name. Remove it in the Tailscale admin console (Machines), then rename this machine to %s", fqdn, n.cfg.Hostname, n.cfg.Hostname)
	}
}

// getCertificate wraps the backend's certificate fetch and records the
// outcome. Errors are logged at most once per CertLogInterval, since every
// handshake retries.
func (n *node) getCertificate(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert, err := n.backend.GetCertificate(hi)
	now := time.Now()
	n.mu.Lock()
	defer n.mu.Unlock()
	if err != nil {
		n.st.CertError = err.Error()
		n.st.CertErrorAt = &now
		if now.Sub(n.lastCertLog) >= n.reg.timings.CertLogInterval {
			n.lastCertLog = now
			n.logf("certificate error: %v", err)
		}
		return nil, err
	}
	if n.st.CertError != "" {
		n.st.CertError = ""
		n.st.CertErrorAt = nil
		n.lastCertLog = time.Time{}
		n.logf("certificate recovered")
	}
	leaf := cert.Leaf
	if leaf == nil && len(cert.Certificate) > 0 {
		leaf, _ = x509.ParseCertificate(cert.Certificate[0])
	}
	if leaf != nil {
		na := leaf.NotAfter
		n.st.CertNotAfter = &na
	}
	return cert, nil
}
