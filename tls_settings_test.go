package grpcurl_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"

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

func TestTLS12(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("", "internal/testing/tls/server.crt", "internal/testing/tls/server.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	tlsConf, err := ClientTLSConfig(false, "internal/testing/tls/ca.crt", "", "")
	if err != nil {
		t.Fatalf("failed to create client TLS config: %v", err)
	}
	tlsConf.MaxVersion = tls.VersionTLS12

	e, err := createTestServerAndClient(serverCreds, credentials.NewTLS(tlsConf))
	if err != nil {
		t.Fatalf("failed to setup server and client: %v", err)
	}
	defer e.Close()

	tlsVersion := negotiatedTLSVersion(t, e.cc)
	if tlsVersion != tls.VersionTLS12 {
		t.Errorf("expected TLS 1.2, got 0x%04x", tlsVersion)
	}
}

func TestTLS13(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("", "internal/testing/tls/server.crt", "internal/testing/tls/server.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "internal/testing/tls/ca.crt", "", "")
	if err != nil {
		t.Fatalf("failed to create client creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err != nil {
		t.Fatalf("failed to setup server and client: %v", err)
	}
	defer e.Close()

	tlsVersion := negotiatedTLSVersion(t, e.cc)
	if tlsVersion != tls.VersionTLS13 {
		t.Errorf("expected TLS 1.3, got 0x%04x", tlsVersion)
	}
}

func negotiatedTLSVersion(t *testing.T, cc *grpc.ClientConn) uint16 {
	t.Helper()
	cl := grpcurl_testing.NewTestServiceClient(cc)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var p peer.Peer
	_, err := cl.UnaryCall(ctx, &grpcurl_testing.SimpleRequest{}, grpc.WaitForReady(true), grpc.Peer(&p))
	if err != nil {
		t.Fatalf("RPC failed: %v", err)
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		t.Fatalf("expected TLS auth info, got %T", p.AuthInfo)
	}
	return tlsInfo.State.Version
}

func TestBrokenTLS_ClientPlainText(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("", "internal/testing/tls/server.crt", "internal/testing/tls/server.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	// Plaintext client to TLS server: the server expects a TLS handshake,
	// gets an HTTP/2 preface instead, and closes the connection.
	e, err := createTestServerAndClient(serverCreds, nil)
	if err == nil {
		e.Close()
		t.Fatal("expecting failure when connecting plaintext to TLS server")
	}
	if !strings.Contains(err.Error(), "EOF") &&
		!strings.Contains(err.Error(), "use of closed network connection") &&
		!strings.Contains(err.Error(), "connection reset by peer") {
		t.Fatalf("expecting connection closed error, got: %v", err)
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
	if !strings.Contains(err.Error(), "certificate") {
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
	// The exact TLS alert varies by Go version and TLS version negotiated:
	// - TLS 1.2: "bad certificate" (Go <=1.24) or "handshake failure" (Go 1.25+)
	// - TLS 1.3: "certificate required" (server rejects after handshake)
	errMsg := err.Error()
	if !strings.Contains(errMsg, "bad certificate") &&
		!strings.Contains(errMsg, "handshake failure") &&
		!strings.Contains(errMsg, "certificate required") {
		t.Fatalf("expecting a TLS certificate error, got: %v", err)
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
	// The exact TLS alert varies by Go version and TLS version negotiated:
	// - TLS 1.2: "bad certificate" (Go <=1.24) or "handshake failure" (Go 1.25+)
	// - TLS 1.3: "certificate required" (server rejects after handshake)
	errMsg := err.Error()
	if !strings.Contains(errMsg, "bad certificate") &&
		!strings.Contains(errMsg, "handshake failure") &&
		!strings.Contains(errMsg, "certificate required") {
		t.Fatalf("expecting a TLS certificate error, got: %v", err)
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
