package grpcurl_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	. "github.com/fullstorydev/grpcurl"
	grpcurl_testing "github.com/fullstorydev/grpcurl/internal/testing"
)

func TestPlainText(t *testing.T) {
	e, err := createTestServerAndClient(nil, nil)
	if err != nil {
		t.Fatalf("failed to setup server and client: %v", err)
	}
	defer e.Close()

	simpleTest(t, e.cc)
}

func TestBasicTLS(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("", "internal/testing/tls/server.crt", "internal/testing/tls/server.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "internal/testing/tls/ca.crt", "", "")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err != nil {
		t.Fatalf("failed to setup server and client: %v", err)
	}
	defer e.Close()

	simpleTest(t, e.cc)
}

func TestInsecureClientTLS(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("", "internal/testing/tls/server.crt", "internal/testing/tls/server.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(true, "", "", "")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err != nil {
		t.Fatalf("failed to setup server and client: %v", err)
	}
	defer e.Close()

	simpleTest(t, e.cc)
}

func TestClientCertTLS(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("internal/testing/tls/ca.crt", "internal/testing/tls/server.crt", "internal/testing/tls/server.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "internal/testing/tls/ca.crt", "internal/testing/tls/client.crt", "internal/testing/tls/client.key")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err != nil {
		t.Fatalf("failed to setup server and client: %v", err)
	}
	defer e.Close()

	simpleTest(t, e.cc)
}

func TestRequireClientCertTLS(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("internal/testing/tls/ca.crt", "internal/testing/tls/server.crt", "internal/testing/tls/server.key", true)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "internal/testing/tls/ca.crt", "internal/testing/tls/client.crt", "internal/testing/tls/client.key")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err != nil {
		t.Fatalf("failed to setup server and client: %v", err)
	}
	defer e.Close()

	simpleTest(t, e.cc)
}

func TestBrokenTLS_ClientPlainText(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("", "internal/testing/tls/server.crt", "internal/testing/tls/server.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	// client connection (usually) succeeds since client is not waiting for TLS handshake
	// (we try several times, but if we never get a connection and the error message is
	// a known/expected possibility, we'll just bail)
	var e testEnv
	failCount := 0
	for {
		e, err = createTestServerAndClient(serverCreds, nil)
		if err == nil {
			// success!
			defer e.Close()
			break
		}

		if strings.Contains(err.Error(), "deadline exceeded") ||
			strings.Contains(err.Error(), "use of closed network connection") {
			// It is possible that the connection never becomes healthy:
			//   1) grpc connects successfully
			//   2) grpc client tries to send HTTP/2 preface and settings frame
			//   3) server, expecting handshake, closes the connection
			//   4) in the client, the write fails, so the connection never
			//      becomes ready
			// The client will attempt to reconnect on transient errors, so
			// may eventually bump into the connect time limit. This used to
			// result in a "deadline exceeded" error, but more recent versions
			// of the grpc library report any underlying I/O error instead, so
			// we also check for "use of closed network connection".
			failCount++
			if failCount > 5 {
				return // bail...
			}
			// we'll try again

		} else {
			// some other error occurred, so we'll consider that a test failure
			t.Fatalf("failed to setup server and client: %v", err)
		}
	}

	// but request fails because server closes connection upon seeing request
	// bytes that are not a TLS handshake
	cl := grpcurl_testing.NewTestServiceClient(e.cc)
	_, err = cl.UnaryCall(context.Background(), &grpcurl_testing.SimpleRequest{})
	if err == nil {
		t.Fatal("expecting failure")
	}
	// various errors possible when server closes connection
	if !strings.Contains(err.Error(), "transport is closing") &&
		!strings.Contains(err.Error(), "connection is unavailable") &&
		!strings.Contains(err.Error(), "use of closed network connection") &&
		!strings.Contains(err.Error(), "all SubConns are in TransientFailure") {

		t.Fatalf("expecting transport failure, got: %v", err)
	}
}

func TestBrokenTLS_ServerPlainText(t *testing.T) {
	clientCreds, err := ClientTransportCredentials(false, "internal/testing/tls/ca.crt", "", "")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(nil, clientCreds)
	if err == nil {
		e.Close()
		t.Fatal("expecting TLS failure setting up server and client")
	}
	if !strings.Contains(err.Error(), "first record does not look like a TLS handshake") {
		t.Fatalf("expecting TLS handshake failure, got: %v", err)
	}
}

func TestBrokenTLS_ServerUsesWrongCert(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("", "internal/testing/tls/other.crt", "internal/testing/tls/other.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "internal/testing/tls/ca.crt", "", "")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err == nil {
		e.Close()
		t.Fatal("expecting TLS failure setting up server and client")
	}
	if !strings.Contains(err.Error(), "certificate is valid for") {
		t.Fatalf("expecting TLS certificate error, got: %v", err)
	}
}

func TestBrokenTLS_ClientHasExpiredCert(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("internal/testing/tls/ca.crt", "internal/testing/tls/server.crt", "internal/testing/tls/server.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "internal/testing/tls/ca.crt", "internal/testing/tls/expired.crt", "internal/testing/tls/expired.key")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err == nil {
		e.Close()
		t.Fatal("expecting TLS failure setting up server and client")
	}
	// Go 1.21 uses "expired certificate" in the error message.
	// Older Go versions use a simpler "bad certificate".
	// `runtime.Version()` exists, but we don't want to parse a version String for comparison.
	if !strings.Contains(err.Error(), "expired certificate") && !strings.Contains(err.Error(), "bad certificate") {
		t.Fatalf("expecting TLS certificate error, got: %v", err)
	}
}

func TestBrokenTLS_ServerHasExpiredCert(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("", "internal/testing/tls/expired.crt", "internal/testing/tls/expired.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "internal/testing/tls/ca.crt", "", "")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err == nil {
		e.Close()
		t.Fatal("expecting TLS failure setting up server and client")
	}
	if !strings.Contains(err.Error(), "certificate has expired or is not yet valid") {
		t.Fatalf("expecting TLS certificate expired, got: %v", err)
	}
}

func TestBrokenTLS_ClientNotTrusted(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("internal/testing/tls/ca.crt", "internal/testing/tls/server.crt", "internal/testing/tls/server.key", true)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "internal/testing/tls/ca.crt", "internal/testing/tls/wrong-client.crt", "internal/testing/tls/wrong-client.key")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err == nil {
		e.Close()
		t.Fatal("expecting TLS failure setting up server and client")
	}
	if !strings.Contains(err.Error(), "bad certificate") {
		t.Fatalf("expecting TLS certificate error, got: %v", err)
	}
}

func TestBrokenTLS_ServerNotTrusted(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("", "internal/testing/tls/server.crt", "internal/testing/tls/server.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "", "internal/testing/tls/client.crt", "internal/testing/tls/client.key")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err == nil {
		e.Close()
		t.Fatal("expecting TLS failure setting up server and client")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("expecting TLS certificate error, got: %v", err)
	}
}

func TestBrokenTLS_RequireClientCertButNonePresented(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("internal/testing/tls/ca.crt", "internal/testing/tls/server.crt", "internal/testing/tls/server.key", true)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "internal/testing/tls/ca.crt", "", "")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err == nil {
		e.Close()
		t.Fatal("expecting TLS failure setting up server and client")
	}
	if !strings.Contains(err.Error(), "bad certificate") {
		t.Fatalf("expecting TLS certificate error, got: %v", err)
	}
}

func simpleTest(t *testing.T, cc *grpc.ClientConn) {
	cl := grpcurl_testing.NewTestServiceClient(cc)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := cl.UnaryCall(ctx, &grpcurl_testing.SimpleRequest{}, grpc.WaitForReady(true))
	if err != nil {
		t.Errorf("simple RPC failed: %v", err)
	}
}

func createTestServerAndClient(serverCreds, clientCreds credentials.TransportCredentials) (testEnv, error) {
	var e testEnv
	completed := false
	defer func() {
		if !completed {
			e.Close()
		}
	}()

	var svrOpts []grpc.ServerOption
	if serverCreds != nil {
		svrOpts = []grpc.ServerOption{grpc.Creds(serverCreds)}
	}
	svr := grpc.NewServer(svrOpts...)
	grpcurl_testing.RegisterTestServiceServer(svr, grpcurl_testing.TestServer{})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return e, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	go svr.Serve(l)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cc, err := BlockingDial(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port), clientCreds)
	if err != nil {
		return e, err
	}

	e.svr = svr
	e.cc = cc
	completed = true
	return e, nil
}

type testEnv struct {
	svr *grpc.Server
	cc  *grpc.ClientConn
}

func (e *testEnv) Close() {
	if e.cc != nil {
		e.cc.Close()
		e.cc = nil
	}
	if e.svr != nil {
		e.svr.GracefulStop()
		e.svr = nil
	}
}
