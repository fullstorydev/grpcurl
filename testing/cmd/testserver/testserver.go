// Command testserver spins up a test GRPC server.
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	"github.com/fullstorydev/grpcurl"
	grpcurl_testing "github.com/fullstorydev/grpcurl/testing"
)

var (
	getUnixSocket func() string // nil when run on non-unix platforms

	help   = flag.Bool("help", false, "Print usage instructions and exit.")
	cacert = flag.String("cacert", "",
		`File containing trusted root certificates for verifying  client certs. Ignored
    	if TLS is not in use (e.g. no -cert or -key specified).`)
	cert = flag.String("cert", "",
		`File containing server certificate (public key). Must also provide -key option.
    	Server uses plain-text if no -cert and -key options are given.`)
	key = flag.String("key", "",
		`File containing server private key. Must also provide -cert option. Server uses
    	plain-text if no -cert and -key options are given.`)
	requirecert = flag.Bool("requirecert", false,
		`Require clients to authenticate via client certs. Must be using TLS (e.g. must
    	also provide -cert and -key options).`)
	port      = flag.Int("p", 0, "Port on which to listen. Ephemeral port used if not specified.")
	noreflect = flag.Bool("noreflect", false, "Indicates that server should not support server reflection.")
	quiet     = flag.Bool("q", false, "Suppresses server request and stream logging.")
)

func main() {
	flag.Parse()

	if *help {
		flag.PrintDefaults()
		os.Exit(0)
	}

	grpclog.SetLoggerV2(grpclog.NewLoggerV2(os.Stdout, os.Stdout, os.Stderr))

	if len(flag.Args()) > 0 {
		fmt.Fprintln(os.Stderr, "No arguments expected.")
		os.Exit(2)
	}
	if (*cert == "") != (*key == "") {
		fmt.Fprintln(os.Stderr, "The -cert and -key arguments must be used together and both be present.")
		os.Exit(2)
	}
	if *requirecert && *cert == "" {
		fmt.Fprintln(os.Stderr, "The -requirecert arg cannot be used without -cert and -key arguments.")
		os.Exit(2)
	}

	var opts []grpc.ServerOption
	if *cert != "" {
		creds, err := grpcurl.ServerTransportCredentials(*cacert, *cert, *key, *requirecert)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to configure transport credentials: %v\n", err)
			os.Exit(1)
		}
		opts = []grpc.ServerOption{grpc.Creds(creds)}
	}
	if !*quiet {
		opts = append(opts, grpc.UnaryInterceptor(unaryLogger), grpc.StreamInterceptor(streamLogger))
	}

	var network, addr string
	if getUnixSocket != nil && getUnixSocket() != "" {
		network = "unix"
		addr = getUnixSocket()
	} else {
		network = "tcp"
		addr = fmt.Sprintf("127.0.0.1:%d", *port)
	}
	l, err := net.Listen(network, addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to listen on socket: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Listening on %v\n", l.Addr())

	svr := grpc.NewServer(opts...)

	grpc_testing.RegisterTestServiceServer(svr, grpcurl_testing.TestServer{})
	if !*noreflect {
		reflection.Register(svr)
	}

	if err := svr.Serve(l); err != nil {
		fmt.Fprintf(os.Stderr, "GRPC server returned error: %v\n", err)
		os.Exit(1)
	}
}

var id int32

func unaryLogger(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	i := atomic.AddInt32(&id, 1) - 1
	grpclog.Infof("start <%d>: %s\n", i, info.FullMethod)
	start := time.Now()
	rsp, err := handler(ctx, req)
	var code codes.Code
	if stat, ok := status.FromError(err); ok {
		code = stat.Code()
	} else {
		code = codes.Unknown
	}
	grpclog.Infof("completed <%d>: %v (%d) %v\n", i, code, code, time.Now().Sub(start))
	return rsp, err
}

func streamLogger(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	i := atomic.AddInt32(&id, 1) - 1
	start := time.Now()
	grpclog.Infof("start <%d>: %s\n", i, info.FullMethod)
	err := handler(srv, loggingStream{ss: ss, id: i})
	var code codes.Code
	if stat, ok := status.FromError(err); ok {
		code = stat.Code()
	} else {
		code = codes.Unknown
	}
	grpclog.Infof("completed <%d>: %v(%d) %v\n", i, code, code, time.Now().Sub(start))
	return err
}

type loggingStream struct {
	ss grpc.ServerStream
	id int32
}

func (l loggingStream) SetHeader(md metadata.MD) error {
	return l.ss.SetHeader(md)
}

func (l loggingStream) SendHeader(md metadata.MD) error {
	return l.ss.SendHeader(md)
}

func (l loggingStream) SetTrailer(md metadata.MD) {
	l.ss.SetTrailer(md)
}

func (l loggingStream) Context() context.Context {
	return l.ss.Context()
}

func (l loggingStream) SendMsg(m interface{}) error {
	err := l.ss.SendMsg(m)
	if err == nil {
		grpclog.Infof("stream <%d>: sent message\n", l.id)
	}
	return err
}

func (l loggingStream) RecvMsg(m interface{}) error {
	err := l.ss.RecvMsg(m)
	if err == nil {
		grpclog.Infof("stream <%d>: received message\n", l.id)
	}
	return err
}
