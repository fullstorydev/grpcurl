package testing

import (
	"io"
	"strconv"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/fullstorydev/grpcurl"
)

type TestServer struct{}

// EmptyCall is One empty request followed by one empty response.
func (TestServer) EmptyCall(ctx context.Context, req *grpc_testing.Empty) (*grpc_testing.Empty, error) {
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

// UnaryCall is One request followed by one response.
// The server returns the client payload as-is.
func (TestServer) UnaryCall(ctx context.Context, req *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
	headers, trailers, failEarly, failLate := processMetadata(ctx)
	grpc.SetHeader(ctx, headers)
	grpc.SetTrailer(ctx, trailers)
	if failEarly != codes.OK {
		return nil, status.Error(failEarly, "fail")
	}
	if failLate != codes.OK {
		return nil, status.Error(failLate, "fail")
	}

	return &grpc_testing.SimpleResponse{
		Payload: req.Payload,
	}, nil
}

// StreamingOutputCall is One request followed by a sequence of responses (streamed download).
// The server returns the payload with client desired type and sizes.
func (TestServer) StreamingOutputCall(req *grpc_testing.StreamingOutputCallRequest, str grpc_testing.TestService_StreamingOutputCallServer) error {
	headers, trailers, failEarly, failLate := processMetadata(str.Context())
	str.SetHeader(headers)
	str.SetTrailer(trailers)
	if failEarly != codes.OK {
		return status.Error(failEarly, "fail")
	}

	rsp := &grpc_testing.StreamingOutputCallResponse{Payload: &grpc_testing.Payload{}}
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

// StreamingInputCall is A sequence of requests followed by one response (streamed upload).
// The server returns the aggregated size of client payload as the result.
func (TestServer) StreamingInputCall(str grpc_testing.TestService_StreamingInputCallServer) error {
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
	if err := str.SendAndClose(&grpc_testing.StreamingInputCallResponse{AggregatedPayloadSize: int32(sz)}); err != nil {
		return err
	}

	if failLate != codes.OK {
		return status.Error(failLate, "fail")
	}
	return nil
}

// FullDuplexCall is A sequence of requests with each request served by the server immediately.
// As one request could lead to multiple responses, this interface
// demonstrates the idea of full duplexing.
func (TestServer) FullDuplexCall(str grpc_testing.TestService_FullDuplexCallServer) error {
	headers, trailers, failEarly, failLate := processMetadata(str.Context())
	str.SetHeader(headers)
	str.SetTrailer(trailers)
	if failEarly != codes.OK {
		return status.Error(failEarly, "fail")
	}

	rsp := &grpc_testing.StreamingOutputCallResponse{Payload: &grpc_testing.Payload{}}
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

// HalfDuplexCall is A sequence of requests followed by a sequence of responses.
// The server buffers all the client requests and then serves them in order. A
// stream of responses are returned to the client when the server starts with
// first request.
func (TestServer) HalfDuplexCall(str grpc_testing.TestService_HalfDuplexCallServer) error {
	headers, trailers, failEarly, failLate := processMetadata(str.Context())
	str.SetHeader(headers)
	str.SetTrailer(trailers)
	if failEarly != codes.OK {
		return status.Error(failEarly, "fail")
	}

	var reqs []*grpc_testing.StreamingOutputCallRequest
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
	rsp := &grpc_testing.StreamingOutputCallResponse{}
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
	MetadataReplyHeaders  = "reply-with-headers"
	MetadataReplyTrailers = "reply-with-trailers"
	MetadataFailEarly     = "fail-early"
	MetadataFailLate      = "fail-late"
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

var _ grpc_testing.TestServiceServer = TestServer{}
