// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package tsnode runs additional Tailscale nodes embedded in the Trellis
// process (via tsnet) so proxy listeners can serve TLS on their own tailnet
// hostnames. It owns every tsnet server: one supervised node per
// tailscale_nodes entry that a proxy listener references.
package tsnode

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

var (
	// ErrShuttingDown is returned by Listener once shutdown has begun.
	ErrShuttingDown = errors.New("tailscale nodes are shutting down")
	// ErrNodeFailed is returned by Listener when the node failed terminally
	// (state directory lock held, or Start error).
	ErrNodeFailed = errors.New("tailscale node failed")
)

// Timings holds the durations the monitor uses. Tests shrink them.
type Timings struct {
	RefreshInterval   time.Duration // periodic StatusWithoutPeers refresh
	BackoffMin        time.Duration // watcher reconnect / serve-config retry
	BackoffMax        time.Duration
	WatchHealthyReset time.Duration // watcher uptime that resets the backoff
	LoginURLWait      time.Duration // wait for a login URL before StartLoginInteractive
	CertLogInterval   time.Duration // rate limit for certificate error logs
	KeyExpiryWarn     time.Duration // KeyExpiresSoon threshold
	KeyExpiryLogEvery time.Duration // how often to repeat the expiry warning
}

// DefaultTimings are the production timings.
var DefaultTimings = Timings{
	RefreshInterval:   60 * time.Second,
	BackoffMin:        time.Second,
	BackoffMax:        30 * time.Second,
	WatchHealthyReset: 60 * time.Second,
	LoginURLWait:      10 * time.Second,
	CertLogInterval:   time.Minute,
	KeyExpiryWarn:     14 * 24 * time.Hour,
	KeyExpiryLogEvery: 24 * time.Hour,
}

// Option configures a Registry.
type Option func(*Registry)

// WithTimings overrides DefaultTimings.
func WithTimings(t Timings) Option { return func(r *Registry) { r.timings = t } }

// WithLogf overrides log.Printf for the registry's own log lines.
func WithLogf(logf func(format string, args ...any)) Option {
	return func(r *Registry) { r.logf = logf }
}

// Registry owns one supervised node per referenced tailscale_nodes entry.
type Registry struct {
	nodes   map[string]*node
	order   []string
	timings Timings
	logf    func(format string, args ...any)

	ctx    context.Context // root context; cancelled by BeginShutdown
	cancel context.CancelFunc

	mu           sync.Mutex
	started      bool
	shuttingDown bool
	listeners    []*trackedListener

	releaseNodes chan struct{} // closed in Shutdown; lets supervisors clean up
	releaseOnce  sync.Once
	wg           sync.WaitGroup
}

// NewRegistry validates the node declarations and references (the same
// structural rules the config validator reports) and creates a backend for
// each referenced node. It starts nothing. Unreferenced nodes get no backend
// and never join the tailnet.
func NewRegistry(cfgs []config.TailscaleNodeConfig, referenced []string, be BackendFactory, opts ...Option) (*Registry, error) {
	canonical, fieldErrs := config.CheckTailscaleNodes(cfgs)
	declared := make(map[string]config.TailscaleNodeConfig)
	for _, c := range cfgs {
		declared[c.Hostname] = c
	}
	for _, name := range referenced {
		if _, ok := declared[name]; !ok {
			fieldErrs = append(fieldErrs, config.FieldError{Field: "tailscale_node", Message: fmt.Sprintf("references undeclared tailscale node '%s'", name)})
		}
	}
	if err := config.FieldErrorsToError(fieldErrs); err != nil {
		return nil, fmt.Errorf("tailscale_nodes: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	r := &Registry{
		nodes:        make(map[string]*node),
		timings:      DefaultTimings,
		logf:         log.Printf,
		ctx:          ctx,
		cancel:       cancel,
		releaseNodes: make(chan struct{}),
	}
	for _, o := range opts {
		o(r)
	}

	for _, name := range referenced {
		if _, dup := r.nodes[name]; dup {
			continue
		}
		cfg := declared[name]
		cfg.StateDir = canonical[name]
		r.nodes[name] = newNode(r, cfg, be(cfg))
		r.order = append(r.order, name)
	}
	return r, nil
}

// Has reports whether hostname is a node managed by this registry.
func (r *Registry) Has(hostname string) bool {
	if r == nil {
		return false
	}
	_, ok := r.nodes[hostname]
	return ok
}

// Start launches one supervisor goroutine per node and returns immediately.
// It is a no-op if called twice or after shutdown has begun.
func (r *Registry) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.shuttingDown {
		return
	}
	r.started = true
	for _, name := range r.order {
		n := r.nodes[name]
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			defer close(n.exited)
			n.supervise(r.ctx)
		}()
	}
}

// Listener blocks until the node is ready, the registry begins shutdown, ctx
// is done, or the node fails terminally. It then returns a TLS listener on
// the node's netstack, or ErrShuttingDown / ErrNodeFailed / ctx.Err().
func (r *Registry) Listener(ctx context.Context, hostname, addr string) (net.Listener, error) {
	n, ok := r.nodes[hostname]
	if !ok {
		return nil, fmt.Errorf("unknown tailscale node %q", hostname)
	}
	select {
	case <-n.ready:
	case <-n.failed:
		return nil, fmt.Errorf("%w: %s: %s", ErrNodeFailed, hostname, n.lastError())
	case <-r.ctx.Done():
		return nil, ErrShuttingDown
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Re-check under the mutex BeginShutdown takes: a node that finishes
	// login just as shutdown begins must not create a listener.
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shuttingDown {
		return nil, ErrShuttingDown
	}
	ln, err := n.backend.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("tailscale node %s: listen %s: %w", hostname, addr, err)
	}
	tl := &trackedListener{Listener: ln}
	r.listeners = append(r.listeners, tl)
	return tls.NewListener(tl, &tls.Config{GetCertificate: n.getCertificate}), nil
}

// Status returns the status of every node, in declaration-reference order.
func (r *Registry) Status() []NodeStatus {
	if r == nil {
		return []NodeStatus{}
	}
	out := make([]NodeStatus, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.nodes[name].status())
	}
	return out
}

// BeginShutdown cancels all supervisors and waiters and forbids new
// listeners. Non-blocking and idempotent.
func (r *Registry) BeginShutdown() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shuttingDown {
		return
	}
	r.shuttingDown = true
	r.cancel()
}

// Shutdown closes node listeners, releases supervisors to clean up their own
// nodes, and joins them, bounded by ctx. Supervisors still running when ctx
// expires (typically stuck in Start) are named in the returned error; their
// state directory locks stay held until they finish on their own. Shutdown
// calls BeginShutdown if it has not been called.
func (r *Registry) Shutdown(ctx context.Context) error {
	r.BeginShutdown()

	r.mu.Lock()
	lns := r.listeners
	r.listeners = nil
	r.mu.Unlock()
	for _, ln := range lns {
		ln.Close()
	}

	r.releaseOnce.Do(func() { close(r.releaseNodes) })

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
	}

	var stuck []string
	for _, name := range r.order {
		select {
		case <-r.nodes[name].exited:
		default:
			stuck = append(stuck, name)
		}
	}
	if len(stuck) == 0 {
		return nil
	}
	sort.Strings(stuck)
	return fmt.Errorf("tailscale nodes did not stop before the deadline (state directory locks remain held): %s", strings.Join(stuck, ", "))
}

// trackedListener closes the underlying netstack listener at most once, so
// both the proxy goroutine and Registry.Shutdown may close it.
type trackedListener struct {
	net.Listener
	once sync.Once
	err  error
}

func (l *trackedListener) Close() error {
	l.once.Do(func() { l.err = l.Listener.Close() })
	return l.err
}
