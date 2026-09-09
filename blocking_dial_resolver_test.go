package grpcurl_test

import (
	"context"
	"net"
	"testing"

	. "github.com/fullstorydev/grpcurl"
	"google.golang.org/grpc"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
)

func TestBlockingDialDNSWithoutProxy(t *testing.T) {
	address := "dns:///" + startTCPServer(t)
	for _, network := range []string{"", "tcp"} {
		t.Run("network="+network, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
			defer cancel()
			cc, err := BlockingDial(ctx, network, address, nil, grpc.WithNoProxy())
			if err != nil {
				t.Fatalf("BlockingDial(%q, %q) failed: %v", network, address, err)
			}
			defer cc.Close()

			// Canceling the dial context must not close a returned connection.
			cancel()
			simpleTest(t, cc)
		})
	}
}

func TestBlockingDialCustomDNSResolver(t *testing.T) {
	backend := startTCPServer(t)
	const target = "dns://nameserver.invalid/grpcurl-resolver-test.invalid:443"
	built := make(chan resolver.Target, 1)
	dialed := make(chan string, 1)
	r := manual.NewBuilderWithScheme("dns")
	r.BuildCallback = func(target resolver.Target, _ resolver.ClientConn, _ resolver.BuildOptions) {
		select {
		case built <- target:
		default:
		}
	}
	r.InitialState(resolver.State{Addresses: []resolver.Address{{Addr: backend}}})
	dialer := func(ctx context.Context, address string) (net.Conn, error) {
		select {
		case dialed <- address:
		default:
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	cc, err := BlockingDial(ctx, "", target, nil,
		grpc.WithResolvers(r), grpc.WithContextDialer(dialer), grpc.WithNoProxy())
	if err != nil {
		t.Fatalf("BlockingDial(%q) failed: %v", target, err)
	}
	defer cc.Close()

	// Rewriting dns targets to passthrough would bypass the caller's resolver
	// and pass the unresolvable logical target to the custom dialer.
	select {
	case actual := <-built:
		if actual.URL.String() != target {
			t.Errorf("resolver target = %q, want %q", actual.URL.String(), target)
		}
	default:
		t.Fatal("custom DNS resolver was not used")
	}
	select {
	case actual := <-dialed:
		if actual != backend {
			t.Errorf("dialer address = %q, want resolved backend %q", actual, backend)
		}
	default:
		t.Fatal("custom dialer was not used")
	}
	simpleTest(t, cc)
}
