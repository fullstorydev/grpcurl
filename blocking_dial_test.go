package grpcurl_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/fullstorydev/grpcurl"
	grpcurl_testing "github.com/fullstorydev/grpcurl/internal/testing"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/test/bufconn"
)

// dialTimeout gives successful connections ample time to become ready.
const dialTimeout = 10 * time.Second

// Failed connections currently retry until their context expires. Keep these
// deadlines short so testing that behavior does not slow down the suite.
const dialFailureTimeout = 250 * time.Millisecond

// startTCPServer starts a gRPC server on a loopback TCP port and returns its
// host:port address. The server is stopped when the test finishes.
func startTCPServer(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	serveOn(t, l)
	return l.Addr().String()
}

// serveOn registers the test service on a gRPC server serving the given
// listener, and arranges for it to be stopped when the test finishes.
func serveOn(t *testing.T, l net.Listener) *grpc.Server {
	t.Helper()
	svr := grpc.NewServer()
	grpcurl_testing.RegisterTestServiceServer(svr, grpcurl_testing.TestServer{})
	go svr.Serve(l)
	t.Cleanup(svr.Stop)
	return svr
}

// unusedTCPPort returns a loopback address that nothing is listening on, by
// binding a port and immediately releasing it.
func unusedTCPPort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("failed to close listener: %v", err)
	}
	return addr
}

// assertDialSucceeds dials the address and verifies the connection works by
// issuing an RPC over it.
func assertDialSucceeds(t *testing.T, network, address string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	cc, err := BlockingDial(ctx, network, address, nil)
	if err != nil {
		t.Fatalf("BlockingDial(%q, %q) failed: %v", network, address, err)
	}
	defer cc.Close()

	simpleTest(t, cc)
}

// assertDialTimesOut records current behavior, not the desired behavior, for
// failures before the transport handshake.
// TODO: https://github.com/fullstorydev/grpcurl/issues/581
// Replace the timeout assertion with a check for prompt failure that preserves
// the underlying dial error once error reporting is fixed.
func assertDialTimesOut(t *testing.T, network, address string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), dialFailureTimeout)
	defer cancel()

	cc, err := BlockingDial(ctx, network, address, nil)
	if cc != nil {
		cc.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("BlockingDial(%q, %q) error = %q, want it to wrap %v",
			network, address, err, context.DeadlineExceeded)
	}
}

func TestBlockingDialTCP(t *testing.T) {
	addr := startTCPServer(t)

	// The empty network is what the command line uses, so it is the most
	// important case to cover; "tcp" is supported for callers of the library.
	for _, network := range []string{"", "tcp"} {
		t.Run(fmt.Sprintf("network=%q", network), func(t *testing.T) {
			assertDialSucceeds(t, network, addr)
		})
	}
}

func TestBlockingDialTCPConnectionRefused(t *testing.T) {
	// TODO: https://github.com/fullstorydev/grpcurl/issues/581
	// A refused connection should fail promptly with the underlying
	// "connection refused" error. For now, record the generic timeout.
	addr := unusedTCPPort(t)

	for _, network := range []string{"", "tcp"} {
		t.Run(fmt.Sprintf("network=%q", network), func(t *testing.T) {
			assertDialTimesOut(t, network, addr)
		})
	}
}

type dialError struct{ temporary bool }

func (e dialError) Error() string   { return "injected dial failure" }
func (e dialError) Temporary() bool { return e.temporary }

func fastDialBackoff() grpc.DialOption {
	return grpc.WithConnectParams(grpc.ConnectParams{
		Backoff: backoff.Config{
			BaseDelay:  10 * time.Millisecond,
			Multiplier: 1,
			MaxDelay:   10 * time.Millisecond,
		},
		MinConnectTimeout: time.Second,
	})
}

func TestBlockingDialRetries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		temporary bool
		override  bool
	}{
		{name: "temporary", temporary: true},
		// TODO: https://github.com/fullstorydev/grpcurl/issues/581
		// Without a caller override, a permanent error should be returned on
		// the first attempt. This case currently records retrying to success.
		{name: "permanent"},
		{name: "permanent with caller override", override: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener := bufconn.Listen(1024 * 1024)
			serveOn(t, listener)
			var attempts atomic.Int32
			wantErr := dialError{temporary: tc.temporary}
			opts := []grpc.DialOption{
				fastDialBackoff(),
				grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
					if attempts.Add(1) == 1 {
						return nil, wantErr
					}
					return listener.DialContext(ctx)
				}),
			}
			if tc.override {
				opts = append(opts, grpc.FailOnNonTempDialError(false))
			}
			ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
			defer cancel()
			cc, err := BlockingDial(ctx, "", "passthrough:///retry-test", nil, opts...)
			// NewClient currently retries both temporary and permanent errors;
			// FailOnNonTempDialError(false) is also accepted from library callers.
			if err != nil {
				t.Fatal(err)
			}
			defer cc.Close()
			if attempts.Load() != 2 {
				t.Errorf("dial attempts = %d, want 2", attempts.Load())
			}
			cancel()
			// A successful connection must outlive its dial context.
			simpleTest(t, cc)
		})
	}
}

func TestBlockingDialReconnect(t *testing.T) {
	first := bufconn.Listen(1024 * 1024)
	second := bufconn.Listen(1024 * 1024)
	firstServer := serveOn(t, first)
	serveOn(t, second)
	var active atomic.Pointer[bufconn.Listener]
	active.Store(first)
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	cc, err := BlockingDial(ctx, "", "passthrough:///reconnect-test", nil,
		fastDialBackoff(),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return active.Load().DialContext(ctx)
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	cancel()
	simpleTest(t, cc)
	active.Store(second)
	firstServer.Stop()
	simpleTest(t, cc)
}

func TestBlockingDialTCPNetworkRejectsUnixAddress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	cc, err := BlockingDial(ctx, "tcp", "unix:///tmp/does-not-exist.sock", nil)
	if cc != nil {
		cc.Close()
	}
	if err == nil {
		t.Fatal("BlockingDial with tcp network and unix address succeeded, expected it to fail")
	}
	if !strings.Contains(err.Error(), "cannot use unix address") {
		t.Errorf("error = %q, want it to contain %q", err, "cannot use unix address")
	}
}
