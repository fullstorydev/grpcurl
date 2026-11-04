package grpcurl

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	_ "google.golang.org/grpc/encoding/proto" // register the proto codec
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	grpcWebContentType = "application/grpc-web+proto"

	grpcWebFlagCompressed = 0x01
	grpcWebFlagTrailer    = 0x80

	defaultGRPCWebMaxRecvMsgSize = 4 * 1024 * 1024
)

// GRPCWebOptions configures a channel created with NewGRPCWebChannel.
type GRPCWebOptions struct {
	// TLSConfig is used to make HTTPS requests. If nil, requests are sent
	// over plain-text HTTP/1.1.
	TLSConfig *tls.Config
	// Authority, if non-empty, overrides the Host header (the ":authority"
	// pseudo-header for HTTP/2) sent with each request. It does not change
	// the address that is dialed.
	Authority string
	// UserAgent, if non-empty, is sent as the User-Agent header.
	UserAgent string
	// MaxRecvMsgSize is the largest response message, in bytes, that will be
	// accepted. Defaults to 4 MiB, the same default as grpc-go.
	MaxRecvMsgSize int
	// DialContext, if non-nil, is used to establish connections. It can be
	// used to apply a connect timeout or to connect to a unix domain socket.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// Transport, if non-nil, is used to send requests instead of a transport
	// built from the fields above. TLSConfig and DialContext are ignored
	// when this is set.
	Transport http.RoundTripper
}

// GRPCWebChannel invokes RPCs using the gRPC-Web protocol instead of native
// gRPC. This allows grpcurl to talk to servers that are only reachable through
// a gRPC-Web gateway, such as Envoy's grpc_web filter, grpcwebproxy, or a
// Connect server.
//
// GRPCWebChannel implements grpc.ClientConnInterface, so it can be passed to
// InvokeRPC and to grpcreflect.NewClientAuto.
//
// gRPC-Web has no full-duplex streaming, so client-streaming and
// bidirectional-streaming calls are sent half-duplex: all request messages are
// sent in a single HTTP request when the client closes its side of the stream,
// and response messages are read after that. As a consequence, RecvMsg on such
// a stream blocks until CloseSend is called (from another goroutine, for bidi
// streams). Server reflection streams are the exception: each reflection
// request is sent as its own HTTP request so that reflection works through
// any gRPC-Web gateway.
type GRPCWebChannel struct {
	client         *http.Client
	baseURL        string
	authority      string
	userAgent      string
	maxRecvMsgSize int
	codec          encoding.CodecV2
}

var _ grpc.ClientConnInterface = (*GRPCWebChannel)(nil)

// NewGRPCWebChannel returns a channel that sends gRPC-Web requests to the
// given target. The target is a "host:port" address, optionally followed by a
// path prefix (e.g. "example.com:443/api") for servers that mount their
// gRPC-Web endpoint below the root path. A full "http://" or "https://" URL is
// also accepted.
func NewGRPCWebChannel(target string, opts GRPCWebOptions) (*GRPCWebChannel, error) {
	baseURL, err := grpcWebBaseURL(target, opts.TLSConfig != nil)
	if err != nil {
		return nil, err
	}
	rt := opts.Transport
	if rt == nil {
		t := &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			TLSClientConfig:     opts.TLSConfig,
			DialContext:         opts.DialContext,
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: 10 * time.Second,
			// We decompress gzip messages ourselves, per the gRPC framing.
			DisableCompression: true,
		}
		if t.DialContext == nil {
			t.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		}
		rt = t
	}
	maxRecv := opts.MaxRecvMsgSize
	if maxRecv <= 0 {
		maxRecv = defaultGRPCWebMaxRecvMsgSize
	}
	codec := encoding.GetCodecV2("proto")
	if codec == nil {
		return nil, errors.New("proto codec is not registered")
	}
	return &GRPCWebChannel{
		client:         &http.Client{Transport: rt},
		baseURL:        baseURL,
		authority:      opts.Authority,
		userAgent:      opts.UserAgent,
		maxRecvMsgSize: maxRecv,
		codec:          codec,
	}, nil
}

func grpcWebBaseURL(target string, useTLS bool) (string, error) {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return strings.TrimSuffix(target, "/"), nil
	}
	host, prefix := target, ""
	if i := strings.IndexByte(target, '/'); i >= 0 {
		host, prefix = target[:i], target[i:]
	}
	if host == "" {
		return "", fmt.Errorf("invalid gRPC-Web target %q: missing host", target)
	}
	scheme := "http"
	if useTLS {
		scheme = "https"
	}
	return scheme + "://" + host + strings.TrimSuffix(prefix, "/"), nil
}

// Close releases idle connections held by the channel.
func (c *GRPCWebChannel) Close() error {
	c.client.CloseIdleConnections()
	return nil
}

// Invoke performs a unary RPC.
func (c *GRPCWebChannel) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	co := c.callOptions(opts)
	frame, err := c.encodeFrame(args)
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, method, frame)
	if err != nil {
		return err
	}
	defer resp.close()
	co.setHeader(resp.header)

	err = resp.recv(reply, co.maxRecvMsgSize)
	if err == nil {
		// Unary RPCs must have exactly one response message.
		var extra []byte
		if err = resp.recvRaw(&extra, co.maxRecvMsgSize); err == nil {
			err = status.Error(codes.Internal, "cardinality violation: received more than one response message for unary RPC")
		} else if err == io.EOF {
			err = nil
		}
	} else if err == io.EOF {
		err = status.Error(codes.Internal, "cardinality violation: received no response message for unary RPC")
		if resp.status != nil && resp.status.Code() != codes.OK {
			err = resp.status.Err()
		}
	}
	co.setTrailer(resp.trailer)
	return err
}

// NewStream begins a streaming RPC.
func (c *GRPCWebChannel) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	return &grpcWebStream{
		ch:         c,
		ctx:        ctx,
		cancel:     cancel,
		method:     method,
		opts:       c.callOptions(opts),
		perMessage: isReflectionMethod(method),
		ready:      make(chan struct{}),
	}, nil
}

func isReflectionMethod(method string) bool {
	switch method {
	case "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
		"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo":
		return true
	}
	return false
}

type grpcWebCallOptions struct {
	headers        []*metadata.MD
	trailers       []*metadata.MD
	maxRecvMsgSize int
}

func (c *GRPCWebChannel) callOptions(opts []grpc.CallOption) *grpcWebCallOptions {
	co := &grpcWebCallOptions{maxRecvMsgSize: c.maxRecvMsgSize}
	for _, opt := range opts {
		switch opt := opt.(type) {
		case grpc.HeaderCallOption:
			co.headers = append(co.headers, opt.HeaderAddr)
		case grpc.TrailerCallOption:
			co.trailers = append(co.trailers, opt.TrailerAddr)
		case grpc.MaxRecvMsgSizeCallOption:
			co.maxRecvMsgSize = opt.MaxRecvMsgSize
		}
	}
	return co
}

func (co *grpcWebCallOptions) setHeader(md metadata.MD) {
	for _, addr := range co.headers {
		*addr = md
	}
}

func (co *grpcWebCallOptions) setTrailer(md metadata.MD) {
	for _, addr := range co.trailers {
		*addr = md
	}
}

func (c *GRPCWebChannel) encodeFrame(msg any) ([]byte, error) {
	data, err := c.codec.Marshal(msg)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to marshal request: %v", err)
	}
	defer data.Free()
	frame := make([]byte, 5+data.Len())
	binary.BigEndian.PutUint32(frame[1:5], uint32(data.Len()))
	data.CopyTo(frame[5:])
	return frame, nil
}

// do sends a single gRPC-Web request whose body contains the given,
// already-framed request messages, and returns once response headers arrive.
func (c *GRPCWebChannel) do(ctx context.Context, method string, body []byte) (*grpcWebResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+method, bytes.NewReader(body))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create gRPC-Web request: %v", err)
	}
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		for k, vs := range md {
			if isReservedGRPCWebHeader(k) {
				continue
			}
			for _, v := range vs {
				if strings.HasSuffix(k, "-bin") {
					v = base64.RawStdEncoding.EncodeToString([]byte(v))
				}
				req.Header.Add(k, v)
			}
		}
	}
	req.Header.Set("Content-Type", grpcWebContentType)
	req.Header.Set("Accept", grpcWebContentType)
	req.Header.Set("X-Grpc-Web", "1")
	req.Header.Set("Grpc-Accept-Encoding", "gzip")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if c.authority != "" {
		req.Host = c.authority
	}
	if deadline, ok := ctx.Deadline(); ok {
		req.Header.Set("Grpc-Timeout", encodeGRPCTimeout(time.Until(deadline)))
	}

	httpResp, err := c.client.Do(req)
	if err != nil {
		return nil, grpcWebTransportError(ctx, err)
	}
	resp := &grpcWebResponse{ctx: ctx, httpResp: httpResp, body: httpResp.Body}
	if err := resp.readHeaders(); err != nil {
		resp.close()
		return nil, err
	}
	return resp, nil
}

func grpcWebTransportError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return status.FromContextError(ctxErr).Err()
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	return status.Errorf(codes.Unavailable, "gRPC-Web request failed: %v", err)
}

func isReservedGRPCWebHeader(k string) bool {
	if k == "" || k[0] == ':' {
		return true
	}
	switch k {
	case "content-type", "user-agent", "te", "host", "connection", "content-length",
		"transfer-encoding", "grpc-timeout", "grpc-encoding", "grpc-accept-encoding",
		"x-grpc-web", "grpc-status", "grpc-message", "grpc-status-details-bin":
		return true
	}
	return false
}

// grpcWebResponse reads the frames of a single gRPC-Web response.
type grpcWebResponse struct {
	ctx      context.Context
	httpResp *http.Response
	body     io.ReadCloser
	encoding string

	header  metadata.MD
	trailer metadata.MD
	// status is set once the trailers have been read (or the response was
	// trailers-only, or the response turned out not to be gRPC-Web).
	status *status.Status
}

func (r *grpcWebResponse) close() {
	_ = r.body.Close()
}

func (r *grpcWebResponse) readHeaders() error {
	h := r.httpResp.Header
	contentType := h.Get("Content-Type")
	isGRPCWeb := strings.HasPrefix(contentType, "application/grpc-web")
	if strings.HasPrefix(contentType, "application/grpc-web-text") {
		return status.Errorf(codes.Internal, "server replied with unsupported content-type %q; only binary gRPC-Web is supported", contentType)
	}

	if h.Get("Grpc-Status") != "" {
		// Trailers-only response: the status is in the HTTP headers and there
		// are no message frames.
		md := headerToMetadata(h)
		r.header = metadata.MD{}
		r.trailer = md
		r.status = statusFromMetadata(md)
		return nil
	}
	if r.httpResp.StatusCode != http.StatusOK || !isGRPCWeb {
		code := codes.Unknown
		if r.httpResp.StatusCode != http.StatusOK {
			code = httpStatusToCode(r.httpResp.StatusCode)
		}
		msg := fmt.Sprintf("unexpected HTTP status code received from server: %d (%s)", r.httpResp.StatusCode, http.StatusText(r.httpResp.StatusCode))
		if !isGRPCWeb {
			msg += fmt.Sprintf("; received unexpected content-type %q; is %s a gRPC-Web endpoint?", contentType, r.httpResp.Request.URL)
		}
		return status.Error(code, msg)
	}

	r.encoding = h.Get("Grpc-Encoding")
	r.header = headerToMetadata(h)
	return nil
}

// recv reads the next message into m. It returns io.EOF once the trailers have
// been read and the status is OK, or the RPC's status error otherwise.
func (r *grpcWebResponse) recv(m any, maxSize int) error {
	var data []byte
	if err := r.recvRaw(&data, maxSize); err != nil {
		return err
	}
	codec := encoding.GetCodecV2("proto")
	if err := codec.Unmarshal(mem.BufferSlice{mem.SliceBuffer(data)}, m); err != nil {
		return status.Errorf(codes.Internal, "failed to unmarshal response message: %v", err)
	}
	return nil
}

func (r *grpcWebResponse) recvRaw(data *[]byte, maxSize int) error {
	for {
		if r.status != nil {
			if r.status.Code() == codes.OK {
				return io.EOF
			}
			return r.status.Err()
		}
		var hdr [5]byte
		if _, err := io.ReadFull(r.body, hdr[:]); err != nil {
			if err == io.EOF {
				// Body ended without a trailer frame. Some servers send real
				// HTTP trailers instead, so look there before giving up.
				if r.httpResp.Trailer.Get("Grpc-Status") != "" {
					r.trailer = headerToMetadata(r.httpResp.Trailer)
					r.status = statusFromMetadata(r.trailer)
					continue
				}
				r.status = status.New(codes.Internal, "server closed the stream without sending trailers")
				continue
			}
			return r.readError(err)
		}
		flags := hdr[0]
		length := binary.BigEndian.Uint32(hdr[1:])
		if flags&grpcWebFlagTrailer == 0 && int64(length) > int64(maxSize) {
			return status.Errorf(codes.ResourceExhausted, "grpc: received message larger than max (%d vs. %d)", length, maxSize)
		}
		if flags&grpcWebFlagTrailer != 0 && length > 1<<20 {
			return status.Errorf(codes.Internal, "trailer frame too large (%d bytes)", length)
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(r.body, payload); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return status.Error(codes.Internal, "truncated gRPC-Web frame: server closed the stream mid-message")
			}
			return r.readError(err)
		}
		if flags&grpcWebFlagCompressed != 0 {
			var err error
			if payload, err = r.decompress(payload, maxSize); err != nil {
				return err
			}
		}
		if flags&grpcWebFlagTrailer != 0 {
			md, err := parseGRPCWebTrailers(payload)
			if err != nil {
				return err
			}
			r.trailer = md
			r.status = statusFromMetadata(md)
			continue
		}
		*data = payload
		return nil
	}
}

func (r *grpcWebResponse) readError(err error) error {
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		return status.FromContextError(ctxErr).Err()
	}
	return status.Errorf(codes.Unavailable, "failed to read gRPC-Web response: %v", err)
}

func (r *grpcWebResponse) decompress(payload []byte, maxSize int) ([]byte, error) {
	if r.encoding != "gzip" {
		return nil, status.Errorf(codes.Internal, "received compressed message with unsupported grpc-encoding %q", r.encoding)
	}
	zr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to decompress message: %v", err)
	}
	out, err := io.ReadAll(io.LimitReader(zr, int64(maxSize)+1))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to decompress message: %v", err)
	}
	if len(out) > maxSize {
		return nil, status.Errorf(codes.ResourceExhausted, "grpc: received message after decompression larger than max %d", maxSize)
	}
	return out, nil
}

// parseGRPCWebTrailers parses the body of a trailer frame, which is a block
// of HTTP/1-style "name: value\r\n" lines.
func parseGRPCWebTrailers(b []byte) (metadata.MD, error) {
	md := metadata.MD{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		i := strings.IndexByte(line, ':')
		if i <= 0 {
			return nil, status.Errorf(codes.Internal, "malformed gRPC-Web trailer line %q", line)
		}
		k := strings.ToLower(strings.TrimSpace(line[:i]))
		v := strings.TrimSpace(line[i+1:])
		if err := appendMetadataValue(md, k, v); err != nil {
			return nil, err
		}
	}
	return md, nil
}

// headerToMetadata converts HTTP response headers (or HTTP trailers) to gRPC
// metadata, dropping headers that only matter to the HTTP framing.
func headerToMetadata(h http.Header) metadata.MD {
	md := metadata.MD{}
	for k, vs := range h {
		k = strings.ToLower(k)
		switch k {
		case "content-length", "connection", "keep-alive", "transfer-encoding", "trailer":
			continue
		}
		for _, v := range vs {
			// Malformed binary values are dropped rather than failing the RPC.
			_ = appendMetadataValue(md, k, v)
		}
	}
	return md
}

func appendMetadataValue(md metadata.MD, k, v string) error {
	if strings.HasSuffix(k, "-bin") {
		b, err := decodeBinHeader(v)
		if err != nil {
			return status.Errorf(codes.Internal, "malformed binary metadata %q: %v", k, err)
		}
		v = string(b)
	}
	md[k] = append(md[k], v)
	return nil
}

func decodeBinHeader(v string) ([]byte, error) {
	if len(v)%4 == 0 {
		return base64.StdEncoding.DecodeString(v)
	}
	return base64.RawStdEncoding.DecodeString(v)
}

// statusFromMetadata builds the RPC status from the grpc-status, grpc-message
// and grpc-status-details-bin keys, removing them from md.
func statusFromMetadata(md metadata.MD) *status.Status {
	codeStr := firstValue(md, "grpc-status")
	msg := decodeGRPCMessage(firstValue(md, "grpc-message"))
	details := firstValue(md, "grpc-status-details-bin")
	delete(md, "grpc-status")
	delete(md, "grpc-message")
	delete(md, "grpc-status-details-bin")

	if codeStr == "" {
		return status.New(codes.Internal, "server sent trailers without a grpc-status")
	}
	code, err := strconv.ParseUint(codeStr, 10, 32)
	if err != nil {
		return status.Newf(codes.Unknown, "malformed grpc-status: %q", codeStr)
	}
	if details != "" {
		st := &spb.Status{}
		if err := proto.Unmarshal([]byte(details), st); err == nil && st.Code == int32(code) {
			return status.FromProto(st)
		}
	}
	return status.New(codes.Code(code), msg)
}

func firstValue(md metadata.MD, k string) string {
	if vs := md[k]; len(vs) > 0 {
		return vs[0]
	}
	return ""
}

// decodeGRPCMessage undoes the percent-encoding that servers apply to the
// grpc-message value.
func decodeGRPCMessage(msg string) string {
	if !strings.Contains(msg, "%") {
		return msg
	}
	var sb strings.Builder
	for i := 0; i < len(msg); i++ {
		c := msg[i]
		if c == '%' && i+2 < len(msg) {
			if b, err := strconv.ParseUint(msg[i+1:i+3], 16, 8); err == nil {
				sb.WriteByte(byte(b))
				i += 2
				continue
			}
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

// httpStatusToCode maps an HTTP status code to a gRPC code, as described in
// https://github.com/grpc/grpc/blob/master/doc/http-grpc-status-mapping.md.
func httpStatusToCode(httpStatus int) codes.Code {
	switch httpStatus {
	case http.StatusBadRequest:
		return codes.Internal
	case http.StatusUnauthorized:
		return codes.Unauthenticated
	case http.StatusForbidden:
		return codes.PermissionDenied
	case http.StatusNotFound:
		return codes.Unimplemented
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return codes.Unavailable
	default:
		return codes.Unknown
	}
}

// encodeGRPCTimeout formats a timeout for the grpc-timeout header, which
// allows at most 8 digits followed by a unit.
func encodeGRPCTimeout(t time.Duration) string {
	const maxValue = 99999999
	if t <= 0 {
		return "1n"
	}
	units := []struct {
		d time.Duration
		s string
	}{
		{time.Nanosecond, "n"}, {time.Microsecond, "u"}, {time.Millisecond, "m"},
		{time.Second, "S"}, {time.Minute, "M"},
	}
	for _, u := range units {
		// round up so that the server never sees a shorter deadline
		if v := divRoundUp(t, u.d); v <= maxValue {
			return strconv.FormatInt(v, 10) + u.s
		}
	}
	return strconv.FormatInt(divRoundUp(t, time.Hour), 10) + "H"
}

func divRoundUp(d, r time.Duration) int64 {
	if d%r > 0 {
		return int64(d/r + 1)
	}
	return int64(d / r)
}

// grpcWebStream is a grpc.ClientStream backed by gRPC-Web requests.
type grpcWebStream struct {
	ch         *GRPCWebChannel
	ctx        context.Context
	cancel     context.CancelFunc
	method     string
	opts       *grpcWebCallOptions
	perMessage bool

	mu         sync.Mutex
	reqBuf     bytes.Buffer
	pending    [][]byte // reflection streams only
	closedSend bool

	// ready is closed once resp (or respErr) is set, for half-duplex streams.
	ready   chan struct{}
	resp    *grpcWebResponse
	respErr error
	header  metadata.MD
	trailer metadata.MD
	done    bool
}

func (s *grpcWebStream) Context() context.Context {
	return s.ctx
}

func (s *grpcWebStream) SendMsg(m any) error {
	frame, err := s.ch.encodeFrame(m)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closedSend {
		return status.Error(codes.Internal, "SendMsg called after CloseSend")
	}
	if s.perMessage {
		s.pending = append(s.pending, frame)
		return nil
	}
	s.reqBuf.Write(frame)
	return nil
}

func (s *grpcWebStream) CloseSend() error {
	s.mu.Lock()
	if s.closedSend {
		s.mu.Unlock()
		return nil
	}
	s.closedSend = true
	body := s.reqBuf.Bytes()
	s.mu.Unlock()
	if s.perMessage {
		return nil
	}

	resp, err := s.ch.do(s.ctx, s.method, body)
	s.mu.Lock()
	s.resp, s.respErr = resp, err
	if err == nil {
		s.header = resp.header
		s.opts.setHeader(resp.header)
	}
	s.mu.Unlock()
	close(s.ready)
	return nil
}

// waitForResponse blocks until the half-duplex request has been sent and its
// response headers received.
func (s *grpcWebStream) waitForResponse() (*grpcWebResponse, error) {
	select {
	case <-s.ready:
	case <-s.ctx.Done():
		// CloseSend may have raced with cancellation; prefer its result.
		select {
		case <-s.ready:
		default:
			return nil, status.FromContextError(s.ctx.Err()).Err()
		}
	}
	return s.resp, s.respErr
}

func (s *grpcWebStream) Header() (metadata.MD, error) {
	if s.perMessage {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.header, nil
	}
	if _, err := s.waitForResponse(); err != nil {
		// Like grpc-go, the error itself is reported by RecvMsg.
		return nil, nil
	}
	return s.header, nil
}

func (s *grpcWebStream) Trailer() metadata.MD {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trailer
}

func (s *grpcWebStream) RecvMsg(m any) error {
	if s.perMessage {
		return s.recvPerMessage(m)
	}
	resp, err := s.waitForResponse()
	if err != nil {
		s.finish(nil)
		return err
	}
	err = resp.recv(m, s.opts.maxRecvMsgSize)
	if err != nil {
		s.finish(resp)
	}
	return err
}

// recvPerMessage sends the oldest pending request as its own gRPC-Web request
// and returns its (single) response message.
func (s *grpcWebStream) recvPerMessage(m any) error {
	s.mu.Lock()
	if len(s.pending) == 0 {
		closed := s.closedSend
		s.mu.Unlock()
		if closed {
			return io.EOF
		}
		return status.Error(codes.Internal, "RecvMsg called with no outstanding request on reflection stream")
	}
	frame := s.pending[0]
	s.pending = s.pending[1:]
	s.mu.Unlock()

	resp, err := s.ch.do(s.ctx, s.method, frame)
	if err != nil {
		return err
	}
	defer resp.close()
	s.mu.Lock()
	s.header = resp.header
	s.mu.Unlock()
	s.opts.setHeader(resp.header)

	err = resp.recv(m, s.opts.maxRecvMsgSize)
	if err == nil {
		// drain any remaining frames so that a non-OK status is not missed
		var rest []byte
		for err == nil {
			err = resp.recvRaw(&rest, s.opts.maxRecvMsgSize)
		}
		if err == io.EOF {
			err = nil
		}
	} else if err == io.EOF {
		err = status.Error(codes.Internal, "server sent no response to reflection request")
	}
	s.mu.Lock()
	s.trailer = resp.trailer
	s.mu.Unlock()
	s.opts.setTrailer(resp.trailer)
	return err
}

func (s *grpcWebStream) finish(resp *grpcWebResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	s.done = true
	if resp != nil {
		s.trailer = resp.trailer
		resp.close()
	}
	s.opts.setTrailer(s.trailer)
	s.cancel()
}
