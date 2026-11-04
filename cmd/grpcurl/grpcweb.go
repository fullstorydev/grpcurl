package main

import (
	"context"
	"net"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"

	"github.com/fullstorydev/grpcurl"
)

// channel is what RPCs are invoked on: a *grpc.ClientConn normally, or a
// *grpcurl.GRPCWebChannel when -grpc-web is used.
type channel interface {
	grpc.ClientConnInterface
	Close() error
}

// dialGRPCWeb creates a gRPC-Web channel for the given target. Unlike a
// native gRPC dial, no connection is made until the first request is sent.
func dialGRPCWeb(dialTiming *timingData, target string, dialTime time.Duration, userAgent string) channel {
	dialer := &net.Dialer{Timeout: dialTime, KeepAlive: 30 * time.Second}
	opts := grpcurl.GRPCWebOptions{
		UserAgent:      userAgent,
		MaxRecvMsgSize: *maxMsgSz,
		DialContext:    dialer.DialContext,
	}
	if isUnixSocket != nil && isUnixSocket() {
		path := strings.TrimPrefix(target, "unix://")
		opts.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", path)
		}
		target = "localhost"
	}

	if *plaintext {
		opts.Authority = *authority
	} else {
		tlsTiming := dialTiming.Child("TLS Setup")
		tlsConf, err := grpcurl.ClientTLSConfig(*insecure, *cacert, *cert, *key)
		if err != nil {
			fail(err, "Failed to create TLS config")
		}
		if sslKeylogFile := os.Getenv("SSLKEYLOGFILE"); sslKeylogFile != "" {
			w, err := os.OpenFile(sslKeylogFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
			if err != nil {
				fail(err, "Could not open SSLKEYLOGFILE %s", sslKeylogFile)
			}
			tlsConf.KeyLogWriter = w
		}
		// can use either -servername or -authority; but not both
		if *serverName != "" && *authority != "" {
			if *serverName == *authority {
				warn("Both -servername and -authority are present; prefer only -authority.")
			} else {
				fail(nil, "Cannot specify different values for -servername and -authority.")
			}
		}
		overrideName := *serverName
		if overrideName == "" {
			overrideName = *authority
		}
		if overrideName != "" {
			opts.Authority = overrideName
			tlsConf.ServerName = hostOnly(overrideName)
		}
		opts.TLSConfig = tlsConf
		tlsTiming.Done()
	}

	ch, err := grpcurl.NewGRPCWebChannel(target, opts)
	if err != nil {
		fail(err, "Failed to create gRPC-Web channel for %q", target)
	}
	return ch
}

// hostOnly strips an optional port from an authority value, for use as the
// TLS server name.
func hostOnly(authority string) string {
	if host, _, err := net.SplitHostPort(authority); err == nil {
		return host
	}
	return authority
}
