// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/tsnode"
	"github.com/wingedpig/trellis/internal/tsnode/tsnodetest"
	"tailscale.com/ipn"
)

var fastTimings = tsnode.Timings{
	RefreshInterval:   time.Hour,
	BackoffMin:        5 * time.Millisecond,
	BackoffMax:        40 * time.Millisecond,
	WatchHealthyReset: time.Hour,
	LoginURLWait:      time.Hour,
	CertLogInterval:   time.Hour,
	KeyExpiryWarn:     time.Hour,
	KeyExpiryLogEvery: time.Hour,
}

func newTestRegistry(t *testing.T, hostnames ...string) (*tsnode.Registry, *tsnodetest.Factory) {
	t.Helper()
	var nodes []config.TailscaleNodeConfig
	for _, h := range hostnames {
		nodes = append(nodes, config.TailscaleNodeConfig{Hostname: h, StateDir: filepath.Join(t.TempDir(), h)})
	}
	fac := tsnodetest.NewFactory()
	reg, err := tsnode.NewRegistry(nodes, hostnames, fac.Backend, tsnode.WithTimings(fastTimings), tsnode.WithLogf(func(string, ...any) {}))
	require.NoError(t, err)
	return reg, fac
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	return ln.Addr().String()
}

func waitUntil(t *testing.T, cond func() bool, what string) {
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

// nodeListener waits for the fake to hand out its netstack listener.
func nodeListener(t *testing.T, f *tsnodetest.Fake) *tsnodetest.FakeListener {
	t.Helper()
	waitUntil(t, func() bool { return len(f.Listeners()) > 0 }, "node listener")
	return f.Listeners()[0]
}

func nodeClient(fl *tsnodetest.FakeListener) *http.Client {
	addr := fl.Addr().String()
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		},
	}
}

func TestNewManagerTailscaleNodeErrors(t *testing.T) {
	reg, _ := newTestRegistry(t, "ent")
	defer reg.Shutdown(context.Background())
	route := []config.ProxyRouteConfig{{Upstream: "localhost:3000"}}

	cases := []struct {
		name string
		cfgs []config.ProxyListenerConfig
		reg  *tsnode.Registry
		want string
	}{
		{"undeclared node", []config.ProxyListenerConfig{{Listen: ":443", TailscaleNode: "nope", Routes: route}}, reg, "undeclared"},
		{"no registry", []config.ProxyListenerConfig{{Listen: ":443", TailscaleNode: "ent", Routes: route}}, nil, "undeclared"},
		{"tls_tailscale", []config.ProxyListenerConfig{{Listen: ":443", TailscaleNode: "ent", TLSTailscale: true, Routes: route}}, reg, "must be unset"},
		{"tls_cert", []config.ProxyListenerConfig{{Listen: ":443", TailscaleNode: "ent", TLSCert: "c", TLSKey: "k", Routes: route}}, reg, "must be unset"},
		{"host part", []config.ProxyListenerConfig{{Listen: "0.0.0.0:443", TailscaleNode: "ent", Routes: route}}, reg, "must not include a host"},
		{"no port", []config.ProxyListenerConfig{{Listen: "443", TailscaleNode: "ent", Routes: route}}, reg, "bare port"},
		{"same node and port", []config.ProxyListenerConfig{
			{Listen: ":443", TailscaleNode: "ent", Routes: route},
			{Listen: ":443", TailscaleNode: "ent", Routes: route},
		}, reg, "conflicts with proxy[0].listen"},
		{"same node, equivalent port", []config.ProxyListenerConfig{
			{Listen: ":443", TailscaleNode: "ent", Routes: route},
			{Listen: ":0443", TailscaleNode: "ent", Routes: route},
		}, reg, "conflicts with proxy[0].listen"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewManager(tc.cfgs, tc.reg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	// Host :443 and node :443 are separate bind namespaces.
	_, err := NewManager([]config.ProxyListenerConfig{
		{Listen: ":443", Routes: route},
		{Listen: ":443", TailscaleNode: "ent", Routes: route},
		{Listen: ":1003", TailscaleNode: "ent", Routes: route},
	}, reg)
	require.NoError(t, err)
}

func TestNodeListenerRoutesAndHeaders(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "api host=%s proto=%s fwdhost=%s", r.Host, r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-Host"))
	}))
	defer api.Close()
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "web host=%s", r.Host)
	}))
	defer web.Close()
	ws := startEchoUpgradeServer(t)

	reg, fac := newTestRegistry(t, "ent")
	m, err := NewManager([]config.ProxyListenerConfig{{
		Listen:        ":443",
		TailscaleNode: "ent",
		Routes: []config.ProxyRouteConfig{
			{PathRegexp: "^/ws", Upstream: ws},
			{PathRegexp: "^/api/", Upstream: strings.TrimPrefix(api.URL, "http://")},
			{Upstream: strings.TrimPrefix(web.URL, "http://")},
		},
	}}, reg)
	require.NoError(t, err)
	require.NoError(t, m.Start(context.Background()))
	defer m.Shutdown(context.Background())

	f := fac.Get("ent")
	require.NoError(t, f.WaitWatcher(3*time.Second))
	f.SendState(ipn.Running)
	fl := nodeListener(t, f)
	assert.Equal(t, ":443", fl.Requested)
	assert.Equal(t, 1, f.ClearServeCalls())

	client := nodeClient(fl)
	get := func(path string) string {
		resp, err := client.Get("https://ent.tailnet.ts.net" + path)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	assert.Equal(t, "api host=ent.tailnet.ts.net proto=https fwdhost=ent.tailnet.ts.net", get("/api/v1/x"))
	assert.Equal(t, "web host=ent.tailnet.ts.net", get("/"))

	// WebSocket tunneling.
	conn, err := tls.Dial("tcp", fl.Addr().String(), &tls.Config{InsecureSkipVerify: true})
	require.NoError(t, err)
	defer conn.Close()
	fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: ent.tailnet.ts.net\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	require.NoError(t, err)
	assert.Contains(t, status, "101")
	for {
		line, err := br.ReadString('\n')
		require.NoError(t, err)
		if line == "\r\n" {
			break
		}
	}
	fmt.Fprint(conn, "ping\n")
	echo, err := br.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "ping\n", echo)
}

// startEchoUpgradeServer accepts an HTTP upgrade request, answers 101 and
// then echoes bytes back.
func startEchoUpgradeServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil || !strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
					return
				}
				fmt.Fprint(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
				io.Copy(c, br)
			}()
		}
	}()
	return ln.Addr().String()
}

// Case 15: login completes while a host listener's HTTP shutdown is held
// open by a slow request; no node listener is ever created.
func TestLoginCompletesDuringHTTPDrain(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		fmt.Fprint(w, "done")
	}))
	defer slow.Close()
	upstream := strings.TrimPrefix(slow.URL, "http://")

	reg, fac := newTestRegistry(t, "ent")
	hostAddr := freePort(t)
	m, err := NewManager([]config.ProxyListenerConfig{
		{Listen: hostAddr, Routes: []config.ProxyRouteConfig{{Upstream: upstream}}},
		{Listen: ":443", TailscaleNode: "ent", Routes: []config.ProxyRouteConfig{{Upstream: upstream}}},
	}, reg)
	require.NoError(t, err)
	require.NoError(t, m.Start(context.Background()))
	f := fac.Get("ent")
	require.NoError(t, f.WaitWatcher(3*time.Second))
	f.SendState(ipn.NeedsLogin)

	waitUntil(t, func() bool {
		c, err := net.Dial("tcp", hostAddr)
		if err == nil {
			c.Close()
		}
		return err == nil
	}, "host listener")
	reqDone := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + hostAddr + "/")
		if err == nil {
			resp.Body.Close()
		}
		reqDone <- err
	}()
	<-started

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownDone <- m.Shutdown(ctx)
	}()
	time.Sleep(20 * time.Millisecond) // BeginShutdown has run; HTTP drain is blocked

	// The node finishes login now, with every prerequisite passing.
	f.SendState(ipn.Running)
	time.Sleep(20 * time.Millisecond)
	close(release)

	require.NoError(t, <-reqDone)
	require.NoError(t, <-shutdownDone)
	assert.Empty(t, f.Listeners(), "backend.Listen must never be called")
	assert.Equal(t, 1, f.CloseCount())
}

// Case 16: an in-flight request on a node listener survives shutdown until
// the HTTP drain completes; Close waits for it.
func TestNodeListenerDrainsBeforeClose(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		fmt.Fprint(w, "done")
	}))
	defer slow.Close()

	reg, fac := newTestRegistry(t, "ent")
	m, err := NewManager([]config.ProxyListenerConfig{
		{Listen: ":443", TailscaleNode: "ent", Routes: []config.ProxyRouteConfig{{Upstream: strings.TrimPrefix(slow.URL, "http://")}}},
	}, reg)
	require.NoError(t, err)
	require.NoError(t, m.Start(context.Background()))
	f := fac.Get("ent")
	require.NoError(t, f.WaitWatcher(3*time.Second))
	f.SendState(ipn.Running)
	fl := nodeListener(t, f)

	type result struct {
		body string
		err  error
	}
	reqDone := make(chan result, 1)
	go func() {
		resp, err := nodeClient(fl).Get("https://ent.tailnet.ts.net/")
		if err != nil {
			reqDone <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		reqDone <- result{body: string(b)}
	}()
	<-started

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownDone <- m.Shutdown(ctx)
	}()
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, 0, f.CloseCount(), "Close must wait for the HTTP drain")

	close(release)
	r := <-reqDone
	require.NoError(t, r.err)
	assert.Equal(t, "done", r.body)
	require.NoError(t, <-shutdownDone)
	assert.Equal(t, 1, f.CloseCount())
	assert.Empty(t, f.Violations())
}

// Case 17: a listener obtained just before shutdown, served only after HTTP
// shutdown, gets ErrServerClosed and is still closed.
func TestListenerObtainedJustBeforeShutdown(t *testing.T) {
	reg, fac := newTestRegistry(t, "ent")
	m, err := NewManager([]config.ProxyListenerConfig{
		{Listen: ":443", TailscaleNode: "ent", Routes: []config.ProxyRouteConfig{{Upstream: "127.0.0.1:1"}}},
	}, reg)
	require.NoError(t, err)
	reg.Start() // start nodes without the proxy's serving goroutines
	f := fac.Get("ent")
	require.NoError(t, f.WaitWatcher(3*time.Second))
	f.SendState(ipn.Running)

	ln, err := reg.Listener(context.Background(), "ent", ":443")
	require.NoError(t, err)

	l := m.listeners[0]
	reg.BeginShutdown()
	require.NoError(t, l.server.Shutdown(context.Background()))
	assert.ErrorIs(t, l.server.Serve(ln), http.ErrServerClosed)
	l.serveTailscale(ln) // the proxy goroutine's path closes ln itself
	require.NoError(t, reg.Shutdown(context.Background()))

	require.NotEmpty(t, f.Listeners())
	for _, fl := range f.Listeners() {
		assert.True(t, fl.Closed())
	}
	assert.Equal(t, 1, f.CloseCount())
	assert.Empty(t, f.Violations())
}

// Host listeners never wait on nodes, even when a node is stuck in login.
func TestHostListenerIndependentOfNodes(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer up.Close()
	reg, _ := newTestRegistry(t, "ent")
	hostAddr := freePort(t)
	m, err := NewManager([]config.ProxyListenerConfig{
		{Listen: hostAddr, Routes: []config.ProxyRouteConfig{{Upstream: strings.TrimPrefix(up.URL, "http://")}}},
		{Listen: ":443", TailscaleNode: "ent", Routes: []config.ProxyRouteConfig{{Upstream: "127.0.0.1:1"}}},
	}, reg)
	require.NoError(t, err)
	require.NoError(t, m.Start(context.Background()))
	defer m.Shutdown(context.Background())

	waitUntil(t, func() bool {
		resp, err := http.Get("http://" + hostAddr + "/")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == 200
	}, "host listener serving")
}
