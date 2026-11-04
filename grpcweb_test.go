package grpcurl_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	. "github.com/fullstorydev/grpcurl"
	grpcurl_testing "github.com/fullstorydev/grpcurl/internal/testing"
)

// grpcWebGateway is a minimal gRPC-Web to gRPC gateway, in the spirit of
// Envoy's grpc_web filter. It lets the gRPC-Web channel be tested against the
// same test server (and reflection service) as the native client.
type grpcWebGateway struct {
	cc *grpc.ClientConn
	// onRequest, if set, is called with each incoming request.
	onRequest func(*http.Request)
}

type rawCodec struct{}

func (rawCodec) Marshal(v any) ([]byte, error) { return v.([]byte), nil }
func (rawCodec) Unmarshal(data []byte, v any) error {
	*(v.(*[]byte)) = append([]byte(nil), data...)
	return nil
}
func (rawCodec) Name() string { return "proto" }

func (g grpcWebGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if g.onRequest != nil {
		g.onRequest(r)
	}
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/grpc-web+proto" {
		http.Error(w, "not a gRPC-Web request", http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var reqs [][]byte
	for len(body) > 0 {
		if len(body) < 5 || body[0] != 0 {
			http.Error(w, "bad frame", http.StatusBadRequest)
			return
		}
		n := binary.BigEndian.Uint32(body[1:5])
		reqs = append(reqs, body[5:5+n])
		body = body[5+n:]
	}

	ctx := r.Context()
	md := metadata.MD{}
	for k, vs := range r.Header {
		k = strings.ToLower(k)
		switch k {
		case "content-type", "accept", "x-grpc-web", "user-agent", "content-length",
			"accept-encoding", "grpc-accept-encoding", "grpc-timeout":
			continue
		}
		for _, v := range vs {
			if strings.HasSuffix(k, "-bin") {
				b, err := base64.RawStdEncoding.DecodeString(v)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				v = string(b)
			}
			md.Append(k, v)
		}
	}
	if t := r.Header.Get("Grpc-Timeout"); t != "" {
		d, err := time.ParseDuration(strings.NewReplacer("n", "ns", "u", "us", "m", "ms", "S", "s", "M", "m", "H", "h").Replace(t))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	ctx = metadata.NewOutgoingContext(ctx, md)

	str, err := g.cc.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true, ServerStreams: true}, r.URL.Path, grpc.ForceCodec(rawCodec{}))
	if err == nil {
		for _, req := range reqs {
			if err = str.SendMsg(req); err != nil {
				break
			}
		}
		_ = str.CloseSend()
	}

	if str != nil {
		hdr, _ := str.Header()
		for k, vs := range hdr {
			for _, v := range vs {
				w.Header().Add(k, encodeBinValue(k, v))
			}
		}
	}
	w.Header().Set("Content-Type", "application/grpc-web+proto")
	w.WriteHeader(http.StatusOK)
	for err == nil {
		var msg []byte
		if err = str.RecvMsg(&msg); err != nil {
			break
		}
		_, _ = w.Write(grpcWebFrame(0, msg))
		w.(http.Flusher).Flush()
	}
	if err == io.EOF {
		err = nil
	}

	st := status.Convert(err)
	var tr bytes.Buffer
	fmt.Fprintf(&tr, "grpc-status: %d\r\n", st.Code())
	if st.Message() != "" {
		fmt.Fprintf(&tr, "grpc-message: %s\r\n", strings.ReplaceAll(st.Message(), "%", "%25"))
	}
	if str != nil {
		for k, vs := range str.Trailer() {
			for _, v := range vs {
				fmt.Fprintf(&tr, "%s: %s\r\n", k, encodeBinValue(k, v))
			}
		}
	}
	_, _ = w.Write(grpcWebFrame(0x80, tr.Bytes()))
}

func encodeBinValue(k, v string) string {
	if strings.HasSuffix(k, "-bin") {
		return base64.StdEncoding.EncodeToString([]byte(v))
	}
	return v
}

func grpcWebFrame(flags byte, data []byte) []byte {
	frame := make([]byte, 5+len(data))
	frame[0] = flags
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(data)))
	copy(frame[5:], data)
	return frame
}

func trailerFrame(lines ...string) []byte {
	return grpcWebFrame(0x80, []byte(strings.Join(lines, "\r\n")+"\r\n"))
}

func newWebChannel(t *testing.T, target string, opts GRPCWebOptions) *GRPCWebChannel {
	t.Helper()
	ch, err := NewGRPCWebChannel(target, opts)
	if err != nil {
		t.Fatalf("failed to create gRPC-Web channel: %v", err)
	}
	t.Cleanup(func() { _ = ch.Close() })
	return ch
}

// fakeWebServer returns a channel to a server that replies to every request
// using the given handler.
func fakeWebServer(t *testing.T, h http.HandlerFunc) *GRPCWebChannel {
	t.Helper()
	svr := httptest.NewServer(h)
	t.Cleanup(svr.Close)
	return newWebChannel(t, svr.Listener.Addr().String(), GRPCWebOptions{})
}

func writeWebResponse(w http.ResponseWriter, frames ...[]byte) {
	w.Header().Set("Content-Type", "application/grpc-web+proto")
	for _, f := range frames {
		_, _ = w.Write(f)
	}
}

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGRPCWebOverHTTP2(t *testing.T) {
	var protoMajor int
	svr := httptest.NewUnstartedServer(grpcWebGateway{cc: ccReflect, onRequest: func(r *http.Request) {
		protoMajor = r.ProtoMajor
	}})
	svr.EnableHTTP2 = true
	svr.StartTLS()
	defer svr.Close()

	tlsConf := svr.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	ch := newWebChannel(t, svr.Listener.Addr().String(), GRPCWebOptions{TLSConfig: tlsConf})

	h := &handler{reqMessages: []string{payload1}}
	if err := invoke(context.Background(), sourceProtoset, ch, "testing.TestService/UnaryCall", makeHeaders(codes.OK), h); err != nil {
		t.Fatalf("unexpected error during RPC: %v", err)
	}
	if h.check(t, "testing.TestService.UnaryCall", codes.OK, 1, 1) && h.respMessages[0] != payload1 {
		t.Errorf("unexpected response: %s", h.respMessages[0])
	}
	if protoMajor != 2 {
		t.Errorf("expected request to use HTTP/2; got HTTP/%d", protoMajor)
	}
}

func TestGRPCWebPathPrefixAndHeaders(t *testing.T) {
	var gotPath, gotHost, gotUA, gotTimeout string
	var gotBin []string
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotHost, gotUA = r.URL.Path, r.Host, r.UserAgent()
		gotTimeout = r.Header.Get("Grpc-Timeout")
		gotBin = r.Header.Values("Token-Bin")
		w.Header().Set("Reply-Bin", base64.StdEncoding.EncodeToString([]byte{0, 1, 2}))
		w.Header().Set("Some-Header", "abc")
		writeWebResponse(w,
			grpcWebFrame(0, mustMarshal(t, &grpcurl_testing.Empty{})),
			trailerFrame("grpc-status: 0", "Some-Trailer: xyz"))
	}))
	defer svr.Close()
	ch := newWebChannel(t, svr.Listener.Addr().String()+"/api/", GRPCWebOptions{
		Authority: "override.example.com",
		UserAgent: "grpcurl/test",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "token-bin", string([]byte{0xff, 0x00, 0x10}))
	var hdr, trl metadata.MD
	err := ch.Invoke(ctx, "/testing.TestService/EmptyCall", &grpcurl_testing.Empty{}, &grpcurl_testing.Empty{}, grpc.Header(&hdr), grpc.Trailer(&trl))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/api/testing.TestService/EmptyCall" {
		t.Errorf("wrong request path: %q", gotPath)
	}
	if gotHost != "override.example.com" {
		t.Errorf("wrong host: %q", gotHost)
	}
	if gotUA != "grpcurl/test" {
		t.Errorf("wrong user agent: %q", gotUA)
	}
	if !strings.HasSuffix(gotTimeout, "m") && !strings.HasSuffix(gotTimeout, "u") {
		t.Errorf("unexpected grpc-timeout: %q", gotTimeout)
	}
	if len(gotBin) != 1 || gotBin[0] != base64.RawStdEncoding.EncodeToString([]byte{0xff, 0x00, 0x10}) {
		t.Errorf("binary request metadata not base64 encoded: %v", gotBin)
	}
	if v := hdr["reply-bin"]; len(v) != 1 || v[0] != string([]byte{0, 1, 2}) {
		t.Errorf("binary response header not decoded: %q", v)
	}
	if v := hdr["some-header"]; len(v) != 1 || v[0] != "abc" {
		t.Errorf("wrong response header: %v", hdr)
	}
	if _, ok := hdr["content-length"]; ok {
		t.Errorf("HTTP framing headers should not be in metadata: %v", hdr)
	}
	if v := trl["some-trailer"]; len(v) != 1 || v[0] != "xyz" {
		t.Errorf("wrong trailers: %v", trl)
	}
	if _, ok := trl["grpc-status"]; ok {
		t.Errorf("grpc-status should not be in trailer metadata: %v", trl)
	}
}

func TestGRPCWebTrailersOnly(t *testing.T) {
	ch := fakeWebServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc-web+proto")
		w.Header().Set("Grpc-Status", "5")
		w.Header().Set("Grpc-Message", "no%20such%20widget%3A%20%E2%9C%93")
		w.Header().Set("Some-Trailer", "abc")
	})
	var hdr, trl metadata.MD
	err := ch.Invoke(context.Background(), "/testing.TestService/EmptyCall", &grpcurl_testing.Empty{}, &grpcurl_testing.Empty{}, grpc.Header(&hdr), grpc.Trailer(&trl))
	st, _ := status.FromError(err)
	if st.Code() != codes.NotFound || st.Message() != "no such widget: \u2713" {
		t.Fatalf("unexpected status: %v", err)
	}
	if v := trl["some-trailer"]; len(v) != 1 || v[0] != "abc" {
		t.Errorf("trailers-only metadata should be reported as trailers: %v", trl)
	}
	if len(hdr) != 0 {
		t.Errorf("trailers-only response should have no headers: %v", hdr)
	}
}

func TestGRPCWebStatusDetails(t *testing.T) {
	want, err := status.New(codes.InvalidArgument, "bad request").WithDetails(&errdetails.BadRequest{
		FieldViolations: []*errdetails.BadRequest_FieldViolation{{Field: "name", Description: "required"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	details := base64.StdEncoding.EncodeToString(mustMarshal(t, want.Proto()))
	ch := fakeWebServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeWebResponse(w, trailerFrame("grpc-status: 3", "grpc-message: bad request", "grpc-status-details-bin: "+details))
	})
	err = ch.Invoke(context.Background(), "/testing.TestService/EmptyCall", &grpcurl_testing.Empty{}, &grpcurl_testing.Empty{})
	got := status.Convert(err)
	if got.Code() != codes.InvalidArgument || len(got.Details()) != 1 {
		t.Fatalf("status details were not decoded: %v", got.Proto())
	}
	if br, ok := got.Details()[0].(*errdetails.BadRequest); !ok || br.FieldViolations[0].Field != "name" {
		t.Errorf("wrong details: %v", got.Details())
	}
}

func TestGRPCWebErrors(t *testing.T) {
	empty := mustMarshal(t, &grpcurl_testing.Empty{})
	big := mustMarshal(t, &grpcurl_testing.SimpleResponse{Payload: &grpcurl_testing.Payload{Body: make([]byte, 100)}})
	testCases := []struct {
		name     string
		handler  http.HandlerFunc
		opts     GRPCWebOptions
		wantCode codes.Code
		wantMsg  string
	}{
		{
			name: "not found html",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("<html>nope</html>"))
			},
			wantCode: codes.Unimplemented,
			wantMsg:  "a gRPC-Web endpoint?",
		},
		{
			name: "redirect is not followed",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("redirect was followed with a %s request", r.Method)
				}
				http.Redirect(w, r, "https://elsewhere.example.com/", http.StatusMovedPermanently)
			},
			wantCode: codes.Unknown,
			wantMsg:  `server redirected to "https://elsewhere.example.com/"`,
		},
		{
			name: "service unavailable",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/grpc-web+proto")
				w.WriteHeader(http.StatusServiceUnavailable)
			},
			wantCode: codes.Unavailable,
			wantMsg:  "503",
		},
		{
			name: "200 but not grpc-web",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("{}"))
			},
			wantCode: codes.Unknown,
			wantMsg:  `unexpected content-type "application/json"`,
		},
		{
			name: "grpc-web-text reply",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/grpc-web-text")
			},
			wantCode: codes.Internal,
			wantMsg:  "only binary gRPC-Web is supported",
		},
		{
			name: "message too large",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeWebResponse(w, grpcWebFrame(0, big), trailerFrame("grpc-status: 0"))
			},
			opts:     GRPCWebOptions{MaxRecvMsgSize: 50},
			wantCode: codes.ResourceExhausted,
			wantMsg:  "larger than max",
		},
		{
			name: "truncated frame",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeWebResponse(w, grpcWebFrame(0, big)[:20])
			},
			wantCode: codes.Internal,
			wantMsg:  "truncated",
		},
		{
			name: "missing trailers",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeWebResponse(w, grpcWebFrame(0, empty))
			},
			wantCode: codes.Internal,
			wantMsg:  "without sending trailers",
		},
		{
			name: "no response message",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeWebResponse(w, trailerFrame("grpc-status: 0"))
			},
			wantCode: codes.Internal,
			wantMsg:  "received no response message",
		},
		{
			name: "two response messages",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeWebResponse(w, grpcWebFrame(0, empty), grpcWebFrame(0, empty), trailerFrame("grpc-status: 0"))
			},
			wantCode: codes.Internal,
			wantMsg:  "more than one response message",
		},
		{
			name: "compressed with unknown encoding",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Grpc-Encoding", "snappy")
				writeWebResponse(w, grpcWebFrame(1, empty), trailerFrame("grpc-status: 0"))
			},
			wantCode: codes.Internal,
			wantMsg:  `unsupported grpc-encoding "snappy"`,
		},
		{
			name: "malformed grpc-status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				writeWebResponse(w, trailerFrame("grpc-status: abc"))
			},
			wantCode: codes.Unknown,
			wantMsg:  "malformed grpc-status",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			svr := httptest.NewServer(tc.handler)
			defer svr.Close()
			ch := newWebChannel(t, svr.Listener.Addr().String(), tc.opts)
			err := ch.Invoke(context.Background(), "/testing.TestService/UnaryCall", &grpcurl_testing.SimpleRequest{}, &grpcurl_testing.SimpleResponse{})
			st := status.Convert(err)
			if st.Code() != tc.wantCode || !strings.Contains(st.Message(), tc.wantMsg) {
				t.Errorf("wrong error: want %v containing %q; got %v", tc.wantCode, tc.wantMsg, err)
			}
		})
	}
}

func TestGRPCWebGzipMessages(t *testing.T) {
	want := &grpcurl_testing.SimpleResponse{Username: "gopher", Payload: &grpcurl_testing.Payload{Body: bytes.Repeat([]byte("a"), 1000)}}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(mustMarshal(t, want))
	_ = zw.Close()
	ch := fakeWebServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Grpc-Encoding", "gzip")
		writeWebResponse(w, grpcWebFrame(1, buf.Bytes()), trailerFrame("grpc-status: 0"))
	})
	got := &grpcurl_testing.SimpleResponse{}
	if err := ch.Invoke(context.Background(), "/testing.TestService/UnaryCall", &grpcurl_testing.SimpleRequest{}, got); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !proto.Equal(got, want) {
		t.Errorf("wrong response: %v", got)
	}
}

func TestGRPCWebDeadlineAndUnavailable(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	ch := fakeWebServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := ch.Invoke(ctx, "/testing.TestService/EmptyCall", &grpcurl_testing.Empty{}, &grpcurl_testing.Empty{})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded; got %v", err)
	}

	// nothing is listening on this port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	ch = newWebChannel(t, addr, GRPCWebOptions{})
	err = ch.Invoke(context.Background(), "/testing.TestService/EmptyCall", &grpcurl_testing.Empty{}, &grpcurl_testing.Empty{})
	if status.Code(err) != codes.Unavailable {
		t.Errorf("expected Unavailable; got %v", err)
	}
}

func TestGRPCWebServerStreamIsIncremental(t *testing.T) {
	// Response messages of a server stream must be delivered as they arrive,
	// not only once the server finishes the call.
	gotFirst := make(chan struct{})
	ch := fakeWebServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeWebResponse(w, grpcWebFrame(0, mustMarshal(t, &grpcurl_testing.StreamingOutputCallResponse{})))
		w.(http.Flusher).Flush()
		select {
		case <-gotFirst:
		case <-time.After(5 * time.Second):
			return // the client never saw the first message; don't finish the stream
		}
		_, _ = w.Write(trailerFrame("grpc-status: 0"))
	})
	str, err := ch.NewStream(context.Background(), &grpc.StreamDesc{ServerStreams: true}, "/testing.TestService/StreamingOutputCall")
	if err != nil {
		t.Fatal(err)
	}
	if err := str.SendMsg(&grpcurl_testing.StreamingOutputCallRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := str.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if err := str.RecvMsg(&grpcurl_testing.StreamingOutputCallResponse{}); err != nil {
		t.Fatalf("failed to receive first message: %v", err)
	}
	close(gotFirst)
	if err := str.RecvMsg(&grpcurl_testing.StreamingOutputCallResponse{}); err != io.EOF {
		t.Errorf("expected io.EOF at end of stream; got %v", err)
	}
}

func TestGRPCWebStreamCancel(t *testing.T) {
	ch := fakeWebServer(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	str, err := ch.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true, ServerStreams: true}, "/testing.TestService/FullDuplexCall")
	if err != nil {
		t.Fatal(err)
	}
	// RecvMsg on a half-duplex stream waits for CloseSend; cancellation
	// must unblock it.
	time.AfterFunc(50*time.Millisecond, cancel)
	if err := str.RecvMsg(&grpcurl_testing.StreamingOutputCallResponse{}); status.Code(err) != codes.Canceled {
		t.Errorf("expected Canceled; got %v", err)
	}
}

func TestGRPCWebTLSHandshakeTimeout(t *testing.T) {
	// A server that accepts connections but never completes a TLS handshake.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	defer func() {
		_ = l.Close()
		close(done)
	}()
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				<-done
				_ = conn.Close()
			}()
		}
	}()

	ch := newWebChannel(t, l.Addr().String(), GRPCWebOptions{
		TLSConfig:           &tls.Config{InsecureSkipVerify: true},
		TLSHandshakeTimeout: 200 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	err = ch.Invoke(ctx, "/testing.TestService/EmptyCall", &grpcurl_testing.Empty{}, &grpcurl_testing.Empty{})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("TLS handshake timeout was not applied; RPC took %v", elapsed)
	}
	if code := status.Code(err); code != codes.Unavailable {
		t.Fatalf("expected Unavailable, got %v: %v", code, err)
	}
	if !strings.Contains(err.Error(), "TLS handshake timeout") {
		t.Fatalf("expected TLS handshake timeout error, got %v", err)
	}
}
