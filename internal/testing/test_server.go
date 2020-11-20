package testing

//go:generate protoc --go_out=plugins=grpc:./ test.proto
//go:generate protoc --descriptor_set_out=./test.protoset test.proto
//go:generate protoc --descriptor_set_out=./example.protoset --include_imports example.proto

import (
	"io"
	"strconv"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/kai5263499/grpcurl"
)

// TestServer implements the TestService interface defined in example.proto.
type TestServer struct{}

// EmptyCall accepts one empty request and issues one empty response.
func (TestServer) EmptyCall(ctx context.Context, req *Empty) (*Empty, error) {
	headers, trailers, failEarly, failLate := processMetadata(ctx)
	grpc.SetHeader(ctx, headers)
	grpc.SetTrailer(ctx, trailers)
	if failEarly != codes.OK {
		return nil, status.Error(failEarly, "fail")
	}
	if failLate != codes.OK {
		return nil, status.Error(failLate, "fail")
	}

	return req, nil
}

// UnaryCall accepts one request and issues one response. The response includes
// the client's payload as-is.
func (TestServer) UnaryCall(ctx context.Context, req *SimpleRequest) (*SimpleResponse, error) {
	headers, trailers, failEarly, failLate := processMetadata(ctx)
	grpc.SetHeader(ctx, headers)
	grpc.SetTrailer(ctx, trailers)
	if failEarly != codes.OK {
		return nil, status.Error(failEarly, "fail")
	}
	if failLate != codes.OK {
		return nil, status.Error(failLate, "fail")
	}

	return &SimpleResponse{
		Payload: req.Payload,
	}, nil
}

// StreamingOutputCall accepts one request and issues a sequence of responses
// (streamed download). The server returns the payload with client desired type
// and sizes as specified in the request's ResponseParameters.
func (TestServer) StreamingOutputCall(req *StreamingOutputCallRequest, str TestService_StreamingOutputCallServer) error {
	headers, trailers, failEarly, failLate := processMetadata(str.Context())
	str.SetHeader(headers)
	str.SetTrailer(trailers)
	if failEarly != codes.OK {
		return status.Error(failEarly, "fail")
	}

	rsp := &StreamingOutputCallResponse{Payload: &Payload{}}
	for _, param := range req.ResponseParameters {
		if str.Context().Err() != nil {
			return str.Context().Err()
		}
		delayMicros := int64(param.GetIntervalUs()) * int64(time.Microsecond)
		if delayMicros > 0 {
			time.Sleep(time.Duration(delayMicros))
		}
		sz := int(param.GetSize())
		buf := make([]byte, sz)
		for i := 0; i < sz; i++ {
			buf[i] = byte(i)
		}
		rsp.Payload.Type = req.ResponseType
		rsp.Payload.Body = buf
		if err := str.Send(rsp); err != nil {
			return err
		}
	}

	if failLate != codes.OK {
		return status.Error(failLate, "fail")
	}
	return nil
}

// StreamingInputCall accepts a sequence of requests and issues one response
// (streamed upload). The server returns the aggregated size of client payloads
// as the result.
func (TestServer) StreamingInputCall(str TestService_StreamingInputCallServer) error {
	headers, trailers, failEarly, failLate := processMetadata(str.Context())
	str.SetHeader(headers)
	str.SetTrailer(trailers)
	if failEarly != codes.OK {
		return status.Error(failEarly, "fail")
	}

	sz := 0
	for {
		if str.Context().Err() != nil {
			return str.Context().Err()
		}
		if req, err := str.Recv(); err != nil {
			if err == io.EOF {
				break
			}
			return err
		} else {
			sz += len(req.Payload.Body)
		}
	}
	if err := str.SendAndClose(&StreamingInputCallResponse{AggregatedPayloadSize: int32(sz)}); err != nil {
		return err
	}

	if failLate != codes.OK {
		return status.Error(failLate, "fail")
	}
	return nil
}

// FullDuplexCall accepts a sequence of requests with each request served by the
// server immediately. As one request could lead to multiple responses, this
// interface demonstrates the idea of full duplexing.
func (TestServer) FullDuplexCall(str TestService_FullDuplexCallServer) error {
	headers, trailers, failEarly, failLate := processMetadata(str.Context())
	str.SetHeader(headers)
	str.SetTrailer(trailers)
	if failEarly != codes.OK {
		return status.Error(failEarly, "fail")
	}

	rsp := &StreamingOutputCallResponse{Payload: &Payload{}}
	for {
		if str.Context().Err() != nil {
			return str.Context().Err()
		}
		req, err := str.Recv()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		for _, param := range req.ResponseParameters {
			sz := int(param.GetSize())
			buf := make([]byte, sz)
			for i := 0; i < sz; i++ {
				buf[i] = byte(i)
			}
			rsp.Payload.Type = req.ResponseType
			rsp.Payload.Body = buf
			if err := str.Send(rsp); err != nil {
				return err
			}
		}
	}

	if failLate != codes.OK {
		return status.Error(failLate, "fail")
	}
	return nil
}

// HalfDuplexCall accepts a sequence of requests and issues a sequence of
// responses. The server buffers all the client requests and then serves them
// in order. A stream of responses is returned to the client once the client
// half-closes the stream.
func (TestServer) HalfDuplexCall(str TestService_HalfDuplexCallServer) error {
	headers, trailers, failEarly, failLate := processMetadata(str.Context())
	str.SetHeader(headers)
	str.SetTrailer(trailers)
	if failEarly != codes.OK {
		return status.Error(failEarly, "fail")
	}

	var reqs []*StreamingOutputCallRequest
	for {
		if str.Context().Err() != nil {
			return str.Context().Err()
		}
		if req, err := str.Recv(); err != nil {
			if err == io.EOF {
				break
			}
			return err
		} else {
			reqs = append(reqs, req)
		}
	}
	rsp := &StreamingOutputCallResponse{}
	for _, req := range reqs {
		rsp.Payload = req.Payload
		if err := str.Send(rsp); err != nil {
			return err
		}
	}

	if failLate != codes.OK {
		return status.Error(failLate, "fail")
	}
	return nil
}

const (
	// MetadataReplyHeaders is a request header that contains values that will
	// be echoed back to the client as response headers. The format of the value
	// is "key: val". To have the server reply with more than one response
	// header, supply multiple values in request metadata.
	MetadataReplyHeaders = "reply-with-headers"
	// MetadataReplyTrailers is a request header that contains values that will
	// be echoed back to the client as response trailers. Its format its the
	// same as MetadataReplyHeaders.
	MetadataReplyTrailers = "reply-with-trailers"
	// MetadataFailEarly is a request header that, if present and not zero,
	// indicates that the RPC should fail immediately with that code.
	MetadataFailEarly = "fail-early"
	// MetadataFailLate is a request header that, if present and not zero,
	// indicates that the RPC should fail at the end with that code. This is
	// different from MetadataFailEarly only for streaming calls. An early
	// failure means the call to fail before any request stream is read or any
	// response stream is generated. A late failure means the entire request and
	// response streams will be consumed/processed and only then will the error
	// code be sent.
	MetadataFailLate = "fail-late"
)

func processMetadata(ctx context.Context) (metadata.MD, metadata.MD, codes.Code, codes.Code) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, nil, codes.OK, codes.OK
	}
	return grpcurl.MetadataFromHeaders(md[MetadataReplyHeaders]),
		grpcurl.MetadataFromHeaders(md[MetadataReplyTrailers]),
		toCode(md[MetadataFailEarly]),
		toCode(md[MetadataFailLate])
}

func toCode(vals []string) codes.Code {
	if len(vals) == 0 {
		return codes.OK
	}
	i, err := strconv.Atoi(vals[len(vals)-1])
	if err != nil {
		return codes.Code(i)
	}
	return codes.Code(i)
}

var _ TestServiceServer = TestServer{}
