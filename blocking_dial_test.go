package grpcurl

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

func TestBlockingDialConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	_, err = BlockingDial(ctx, "", addr, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected dial error")
	}
	if elapsed > time.Second {
		t.Fatalf("dial took %s, want connection refused without waiting out the deadline", elapsed)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("got %v, want connection refused", err)
	}
}
