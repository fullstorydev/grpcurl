package grpcurl_test

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/interop/grpc_testing"

	. "github.com/fullstorydev/grpcurl"
	grpcurl_testing "github.com/fullstorydev/grpcurl/testing"
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
	serverCreds, err := ServerTransportCredentials("", "testing/tls/server.crt", "testing/tls/server.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "testing/tls/ca.crt", "", "")
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
	serverCreds, err := ServerTransportCredentials("", "testing/tls/server.crt", "testing/tls/server.key", false)
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
	serverCreds, err := ServerTransportCredentials("testing/tls/ca.crt", "testing/tls/server.crt", "testing/tls/server.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "testing/tls/ca.crt", "testing/tls/client.crt", "testing/tls/client.key")
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
	serverCreds, err := ServerTransportCredentials("testing/tls/ca.crt", "testing/tls/server.crt", "testing/tls/server.key", true)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "testing/tls/ca.crt", "testing/tls/client.crt", "testing/tls/client.key")
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
	serverCreds, err := ServerTransportCredentials("", "testing/tls/server.crt", "testing/tls/server.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	// client connection succeeds since client is not waiting for TLS handshake
	e, err := createTestServerAndClient(serverCreds, nil)
	if err != nil {
		t.Fatalf("failed to setup server and client: %v", err)
	}
	defer e.Close()

	// but request fails because server closes connection upon seeing request
	// bytes that are not a TLS handshake
	cl := grpc_testing.NewTestServiceClient(e.cc)
	_, err = cl.UnaryCall(context.Background(), &grpc_testing.SimpleRequest{})
	if err == nil {
		t.Fatal("expecting failure")
	}
	// various errors possible when server closes connection
	if !strings.Contains(err.Error(), "transport is closing") &&
		!strings.Contains(err.Error(), "connection is unavailable") &&
		!strings.Contains(err.Error(), "use of closed network connection") {

		t.Fatalf("expecting transport failure, got: %v", err)
	}
}

func TestBrokenTLS_ServerPlainText(t *testing.T) {
	clientCreds, err := ClientTransportCredentials(false, "testing/tls/ca.crt", "", "")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(nil, clientCreds)
	if err == nil {
		t.Fatal("expecting TLS failure setting up server and client")
		e.Close()
	}
}

func TestBrokenTLS_ServerUsesWrongCert(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("", "testing/tls/other.crt", "testing/tls/other.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "testing/tls/ca.crt", "", "")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err == nil {
		t.Fatal("expecting TLS failure setting up server and client")
		e.Close()
	}
}

func TestBrokenTLS_ClientHasExpiredCert(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("testing/tls/ca.crt", "testing/tls/server.crt", "testing/tls/server.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "testing/tls/ca.crt", "testing/tls/expired.crt", "testing/tls/expired.key")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err == nil {
		t.Fatal("expecting TLS failure setting up server and client")
		e.Close()
	}
}

func TestBrokenTLS_ServerHasExpiredCert(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("", "testing/tls/expired.crt", "testing/tls/expired.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "testing/tls/ca.crt", "", "")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err == nil {
		t.Fatal("expecting TLS failure setting up server and client")
		e.Close()
	}
}

func TestBrokenTLS_ClientNotTrusted(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("testing/tls/ca.crt", "testing/tls/server.crt", "testing/tls/server.key", true)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "testing/tls/ca.crt", "testing/tls/wrong-client.crt", "testing/tls/wrong-client.key")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err == nil {
		t.Fatal("expecting TLS failure setting up server and client")
		e.Close()
	}
}

func TestBrokenTLS_ServerNotTrusted(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("", "testing/tls/server.crt", "testing/tls/server.key", false)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "", "testing/tls/client.crt", "testing/tls/client.key")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err == nil {
		t.Fatal("expecting TLS failure setting up server and client")
		e.Close()
	}
}

func TestBrokenTLS_RequireClientCertButNonePresented(t *testing.T) {
	serverCreds, err := ServerTransportCredentials("testing/tls/ca.crt", "testing/tls/server.crt", "testing/tls/server.key", true)
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}
	clientCreds, err := ClientTransportCredentials(false, "testing/tls/ca.crt", "", "")
	if err != nil {
		t.Fatalf("failed to create server creds: %v", err)
	}

	e, err := createTestServerAndClient(serverCreds, clientCreds)
	if err == nil {
		t.Fatal("expecting TLS failure setting up server and client")
		e.Close()
	}
}

func simpleTest(t *testing.T, cc *grpc.ClientConn) {
	cl := grpc_testing.NewTestServiceClient(cc)
	_, err := cl.UnaryCall(context.Background(), &grpc_testing.SimpleRequest{})
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
	grpc_testing.RegisterTestServiceServer(svr, grpcurl_testing.TestServer{})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return e, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	go svr.Serve(l)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var tlsOpt grpc.DialOption
	if clientCreds != nil {
		tlsOpt = grpc.WithTransportCredentials(clientCreds)
	} else {
		tlsOpt = grpc.WithInsecure()
	}
	cc, err := grpc.DialContext(ctx, fmt.Sprintf("127.0.0.1:%d", port), grpc.WithBlock(), tlsOpt)
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

func (e testEnv) Close() {
	if e.cc != nil {
		e.cc.Close()
		e.cc = nil
	}
	if e.svr != nil {
		e.svr.GracefulStop()
		e.svr = nil
	}
}
