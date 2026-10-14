package grpcurl_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/fullstorydev/grpcurl"
	"google.golang.org/grpc"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
)

// proxyChildEnv marks the re-executed child process that runs the body of
// TestProxySupport.
const proxyChildEnv = "GRPCURL_TEST_PROXY_CHILD"

// proxyTarget is the address the client is asked to dial. It must be a name
// rather than an IP, and it must not resolve:
//
//   - A loopback address would never be proxied at all. net/http excludes
//     "localhost" and any loopback IP from proxying, so the proxy would be
//     skipped and the test would pass without proving anything.
//   - Because the name never resolves, the RPC can only succeed by way of the
//     proxy. A regression that bypasses the proxy cannot accidentally pass by
//     connecting directly.
//
// The .invalid TLD is reserved by RFC 2606 and is guaranteed not to resolve.
const proxyTarget = "grpcurl-proxy-test.invalid:443"

// TestProxySupport verifies that a dial honors the HTTPS_PROXY environment
// variable, by routing it through a local HTTP CONNECT proxy.
//
// This guards the fix in https://github.com/fullstorydev/grpcurl/pull/480,
// which removed a custom dialer precisely so that grpc-go's own proxy support
// would be used. grpc-go treats grpc.WithContextDialer as an opt-out of proxy
// support: when a custom dialer is set it skips the delegating resolver that
// implements proxying. Reintroducing a custom dialer therefore silently
// disables proxies, which nothing else in this suite would catch.
//
// The test runs in a re-executed child process. net/http resolves the proxy
// environment once per process and caches it in a sync.Once, so any earlier
// dial in this test binary would fix the cached value to "no proxy" and make
// setting HTTPS_PROXY here have no effect.
func TestProxySupport(t *testing.T) {
	if os.Getenv(proxyChildEnv) != "1" {
		reExecForProxyTest(t)
		return
	}

	backend := startTCPServer(t)
	proxyAddr, connectTargets := startConnectProxy(t, backend)

	// Safe to set here: this is a fresh process and nothing has dialed yet.
	t.Setenv("HTTPS_PROXY", "http://"+proxyAddr)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	// WithNoProxy must dial the resolved address directly. A missing port
	// fails locally without DNS or network access; the proxy would accept it.
	directResolver := manual.NewBuilderWithScheme("dns")
	directResolver.InitialState(resolver.State{Addresses: []resolver.Address{{Addr: "missing-port"}}})
	localResolver := manual.NewBuilderWithScheme("dns")
	const resolvedTarget = "grpcurl-resolved-proxy-test.invalid:443"
	localResolver.InitialState(resolver.State{Addresses: []resolver.Address{{Addr: resolvedTarget}}})
	customDialer := func(ctx context.Context, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", backend)
	}

	for _, tc := range []struct {
		name          string
		target        string
		opts          []grpc.DialOption
		wantProxy     bool
		wantError     error
		connectTarget string
	}{
		{name: "bare address", target: proxyTarget, wantProxy: true},
		{name: "dns address", target: "dns:///" + proxyTarget, wantProxy: true},
		{
			// TODO: https://github.com/fullstorydev/grpcurl/issues/581
			// The malformed resolved address should fail promptly with "missing
			// port in address". The timeout is current behavior; bypassing the
			// proxy is the desired behavior that this case also verifies.
			name:      "proxy disabled",
			target:    "dns:///" + proxyTarget,
			opts:      []grpc.DialOption{grpc.WithResolvers(directResolver), grpc.WithNoProxy()},
			wantError: context.DeadlineExceeded,
		},
		{
			name:          "local DNS resolution",
			target:        "dns:///" + proxyTarget,
			opts:          []grpc.DialOption{grpc.WithResolvers(localResolver), grpc.WithLocalDNSResolution()},
			wantProxy:     true,
			connectTarget: resolvedTarget,
		},
		{
			name:   "custom dialer",
			target: proxyTarget,
			opts:   []grpc.DialOption{grpc.WithContextDialer(customDialer)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(connectTargets())
			timeout := dialTimeout
			if tc.wantError != nil {
				timeout = time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			cc, err := BlockingDial(ctx, "", tc.target, nil, tc.opts...)
			if cc != nil {
				defer cc.Close()
			}
			if tc.wantError != nil {
				if !errors.Is(err, tc.wantError) {
					t.Fatalf("BlockingDial(%q) error = %v, want %v", tc.target, err, tc.wantError)
				}
			} else if err != nil {
				t.Fatalf("BlockingDial(%q) failed: %v\nCONNECT requests seen by proxy: %v",
					tc.target, err, connectTargets()[before:])
			} else {
				simpleTest(t, cc)
			}

			seen := connectTargets()[before:]
			if !tc.wantProxy {
				if len(seen) != 0 {
					t.Fatalf("proxy should be bypassed, got CONNECT requests: %v", seen)
				}
				return
			}
			if len(seen) == 0 {
				t.Fatal("proxy received no CONNECT requests")
			}
			wantTarget := tc.connectTarget
			if wantTarget == "" {
				wantTarget = proxyTarget
			}
			for _, target := range seen {
				if target != wantTarget {
					t.Errorf("proxy received CONNECT for %q, want %q", target, wantTarget)
				}
			}
		})
	}
}

// reExecForProxyTest runs this test again in a child process, where the proxy
// environment has not yet been resolved and cached.
func reExecForProxyTest(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProxySupport$", "-test.v", "-test.timeout=55s")
	cmd.Env = append(os.Environ(), proxyChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("proxy test child process failed: %v\n%s", err, out)
	}
}

// startConnectProxy starts a minimal HTTP CONNECT proxy on loopback. It
// records every target it is asked to connect to, then tunnels to backendAddr
// regardless of what was requested. That indirection is what lets the test use
// an unresolvable target: the client only ever sends the name as text in the
// CONNECT request, and never resolves it itself.
//
// The returned function reports the targets seen so far.
func startConnectProxy(t *testing.T, backendAddr string) (proxyAddr string, connectTargets func() []string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for proxy: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	var mu sync.Mutex
	var targets []string

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return // listener closed at test cleanup
			}
			go proxyOneConn(conn, backendAddr, func(target string) {
				mu.Lock()
				defer mu.Unlock()
				targets = append(targets, target)
			})
		}
	}()

	return l.Addr().String(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), targets...)
	}
}

// proxyOneConn handles a single CONNECT request and then bridges the tunnel.
func proxyOneConn(conn net.Conn, backendAddr string, record func(string)) {
	defer conn.Close()

	br := bufio.NewReader(conn)
	requestLine, err := br.ReadString('\n')
	if err != nil {
		return
	}
	// e.g. "CONNECT grpcurl-proxy-test.invalid:443 HTTP/1.1"
	fields := strings.Fields(requestLine)
	if len(fields) < 2 || fields[0] != "CONNECT" {
		return
	}
	record(fields[1])

	// Discard the remaining request headers.
	for {
		line, err := br.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "" {
			break
		}
	}

	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	upstream, err := net.Dial("tcp", backendAddr)
	if err != nil {
		return
	}
	defer upstream.Close()

	// Bridge until either side closes. br may hold buffered bytes already read
	// from conn, so copy from it rather than from conn directly.
	go func() { _, _ = io.Copy(upstream, br) }()
	_, _ = io.Copy(conn, upstream)
}
