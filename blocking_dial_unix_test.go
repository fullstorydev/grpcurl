//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package grpcurl_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// tempSocketPath returns a path suitable for a unix domain socket. It uses a
// short temporary directory rather than t.TempDir() because socket paths are
// limited to around 100 characters, and t.TempDir() embeds the test name.
func tempSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "grpcurl")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// startUnixServer starts a gRPC server listening on a unix domain socket and
// returns the socket path. The server is stopped when the test finishes.
func startUnixServer(t *testing.T) string {
	t.Helper()
	path := tempSocketPath(t)
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("failed to listen on unix socket: %v", err)
	}
	serveOn(t, l)
	return path
}

func TestBlockingDialUnix(t *testing.T) {
	// The command line prepends "unix://" to the target and passes an empty
	// network, so that combination is the one that matters most. A bare path
	// with network "unix" is also supported, for backwards compatibility with
	// https://github.com/fullstorydev/grpcurl/pull/480
	t.Run("network= with unix:// address", func(t *testing.T) {
		assertDialSucceeds(t, "", "unix://"+startUnixServer(t))
	})
	t.Run("network=unix with bare path", func(t *testing.T) {
		assertDialSucceeds(t, "unix", startUnixServer(t))
	})
}

func TestBlockingDialUnixSocketMissing(t *testing.T) {
	// TODO: https://github.com/fullstorydev/grpcurl/issues/581
	// A missing socket should fail promptly with "no such file or directory".
	// The socket is never created, but BlockingDial currently retries until
	// the context expires and returns the generic timeout instead.
	path := tempSocketPath(t)

	t.Run("network= with unix:// address", func(t *testing.T) {
		assertDialTimesOut(t, "", "unix://"+path)
	})
	t.Run("network=unix with bare path", func(t *testing.T) {
		assertDialTimesOut(t, "unix", path)
	})
}
