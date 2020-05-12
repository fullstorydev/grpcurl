package grpcurl_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/grpcreflect"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"

	. "github.com/fullstorydev/grpcurl"
	grpcurl_testing "github.com/fullstorydev/grpcurl/testing"
	jsonpbtest "github.com/fullstorydev/grpcurl/testing/jsonpb_test_proto"
)

var (
	sourceProtoset   DescriptorSource
	sourceProtoFiles DescriptorSource
	ccNoReflect      *grpc.ClientConn

	sourceReflect DescriptorSource
	ccReflect     *grpc.ClientConn

	descSources []descSourceCase
)

type descSourceCase struct {
	name        string
	source      DescriptorSource
	includeRefl bool
}

// NB: These tests intentionally use the deprecated InvokeRpc since that
// calls the other (non-deprecated InvokeRPC). That allows the tests to
// easily exercise both functions.

func TestMain(m *testing.M) {
	var err error
	sourceProtoset, err = DescriptorSourceFromProtoSets("testing/test.protoset")
	if err != nil {
		panic(err)
	}
	sourceProtoFiles, err = DescriptorSourceFromProtoFiles(nil, "testing/test.proto")
	if err != nil {
		panic(err)
	}

	// Create a server that includes the reflection service
	svrReflect := grpc.NewServer()
	grpc_testing.RegisterTestServiceServer(svrReflect, grpcurl_testing.TestServer{})
	reflection.Register(svrReflect)
	var portReflect int
	if l, err := net.Listen("tcp", "127.0.0.1:0"); err != nil {
		panic(err)
	} else {
		portReflect = l.Addr().(*net.TCPAddr).Port
		go svrReflect.Serve(l)
	}
	defer svrReflect.Stop()

	// And a corresponding client
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if ccReflect, err = grpc.DialContext(ctx, fmt.Sprintf("127.0.0.1:%d", portReflect),
		grpc.WithInsecure(), grpc.WithBlock()); err != nil {
		panic(err)
	}
	defer ccReflect.Close()
	refClient := grpcreflect.NewClient(context.Background(), reflectpb.NewServerReflectionClient(ccReflect))
	defer refClient.Reset()

	sourceReflect = DescriptorSourceFromServer(context.Background(), refClient)

	// Also create a server that does *not* include the reflection service
	svrProtoset := grpc.NewServer()
	grpc_testing.RegisterTestServiceServer(svrProtoset, grpcurl_testing.TestServer{})
	var portProtoset int
	if l, err := net.Listen("tcp", "127.0.0.1:0"); err != nil {
		panic(err)
	} else {
		portProtoset = l.Addr().(*net.TCPAddr).Port
		go svrProtoset.Serve(l)
	}
	defer svrProtoset.Stop()

	// And a corresponding client
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if ccNoReflect, err = grpc.DialContext(ctx, fmt.Sprintf("127.0.0.1:%d", portProtoset),
		grpc.WithInsecure(), grpc.WithBlock()); err != nil {
		panic(err)
	}
	defer ccNoReflect.Close()

	descSources = []descSourceCase{
		{"protoset", sourceProtoset, false},
		{"proto", sourceProtoFiles, false},
		{"reflect", sourceReflect, true},
	}

	os.Exit(m.Run())
}

func TestServerDoesNotSupportReflection(t *testing.T) {
	refClient := grpcreflect.NewClient(context.Background(), reflectpb.NewServerReflectionClient(ccNoReflect))
	defer refClient.Reset()

	refSource := DescriptorSourceFromServer(context.Background(), refClient)

	_, err := ListServices(refSource)
	if err != ErrReflectionNotSupported {
		t.Errorf("ListServices should have returned ErrReflectionNotSupported; instead got %v", err)
	}

	_, err = ListMethods(refSource, "SomeService")
	if err != ErrReflectionNotSupported {
		t.Errorf("ListMethods should have returned ErrReflectionNotSupported; instead got %v", err)
	}

	err = InvokeRpc(context.Background(), refSource, ccNoReflect, "FooService/Method", nil, nil, nil)
	// InvokeRpc wraps the error, so we just verify the returned error includes the right message
	if err == nil || !strings.Contains(err.Error(), ErrReflectionNotSupported.Error()) {
		t.Errorf("InvokeRpc should have returned ErrReflectionNotSupported; instead got %v", err)
	}
}

func TestProtosetWithImports(t *testing.T) {
	sourceProtoset, err := DescriptorSourceFromProtoSets("testing/example.protoset")
	if err != nil {
		t.Fatalf("failed to load protoset: %v", err)
	}
	// really shallow check of the loaded descriptors
	if sd, err := sourceProtoset.FindSymbol("TestService"); err != nil {
		t.Errorf("failed to find TestService in protoset: %v", err)
	} else if sd == nil {
		t.Errorf("FindSymbol returned nil for TestService")
	} else if _, ok := sd.(*desc.ServiceDescriptor); !ok {
		t.Errorf("FindSymbol returned wrong kind of descriptor for TestService: %T", sd)
	}
	if md, err := sourceProtoset.FindSymbol("TestRequest"); err != nil {
		t.Errorf("failed to find TestRequest in protoset: %v", err)
	} else if md == nil {
		t.Errorf("FindSymbol returned nil for TestRequest")
	} else if _, ok := md.(*desc.MessageDescriptor); !ok {
		t.Errorf("FindSymbol returned wrong kind of descriptor for TestRequest: %T", md)
	}
}

func TestListServices(t *testing.T) {
	for _, ds := range descSources {
		t.Run(ds.name, func(t *testing.T) {
			doTestListServices(t, ds.source, ds.includeRefl)
		})
	}
}

func doTestListServices(t *testing.T, source DescriptorSource, includeReflection bool) {
	names, err := ListServices(source)
	if err != nil {
		t.Fatalf("failed to list services: %v", err)
	}
	var expected []string
	if includeReflection {
		// when using server reflection, we see the TestService as well as the ServerReflection service
		expected = []string{"grpc.reflection.v1alpha.ServerReflection", "grpc.testing.TestService"}
	} else {
		// without reflection, we see all services defined in the same test.proto file, which is the
		// TestService as well as UnimplementedService
		expected = []string{"grpc.testing.TestService", "grpc.testing.UnimplementedService"}
	}
	if !reflect.DeepEqual(expected, names) {
		t.Errorf("ListServices returned wrong results: wanted %v, got %v", expected, names)
	}
}

func TestListMethods(t *testing.T) {
	for _, ds := range descSources {
		t.Run(ds.name, func(t *testing.T) {
			doTestListMethods(t, ds.source, ds.includeRefl)
		})
	}
}

func doTestListMethods(t *testing.T, source DescriptorSource, includeReflection bool) {
	names, err := ListMethods(source, "grpc.testing.TestService")
	if err != nil {
		t.Fatalf("failed to list methods for TestService: %v", err)
	}
	expected := []string{
		"grpc.testing.TestService.EmptyCall",
		"grpc.testing.TestService.FullDuplexCall",
		"grpc.testing.TestService.HalfDuplexCall",
		"grpc.testing.TestService.StreamingInputCall",
		"grpc.testing.TestService.StreamingOutputCall",
		"grpc.testing.TestService.UnaryCall",
	}
	if !reflect.DeepEqual(expected, names) {
		t.Errorf("ListMethods returned wrong results: wanted %v, got %v", expected, names)
	}

	if includeReflection {
		// when using server reflection, we see the TestService as well as the ServerReflection service
		names, err = ListMethods(source, "grpc.reflection.v1alpha.ServerReflection")
		if err != nil {
			t.Fatalf("failed to list methods for ServerReflection: %v", err)
		}
		expected = []string{"grpc.reflection.v1alpha.ServerReflection.ServerReflectionInfo"}
	} else {
		// without reflection, we see all services defined in the same test.proto file, which is the
		// TestService as well as UnimplementedService
		names, err = ListMethods(source, "grpc.testing.UnimplementedService")
		if err != nil {
			t.Fatalf("failed to list methods for ServerReflection: %v", err)
		}
		expected = []string{"grpc.testing.UnimplementedService.UnimplementedCall"}
	}
	if !reflect.DeepEqual(expected, names) {
		t.Errorf("ListMethods returned wrong results: wanted %v, got %v", expected, names)
	}

	// force an error
	_, err = ListMethods(source, "FooService")
	if err != nil && !strings.Contains(err.Error(), "Symbol not found: FooService") {
		t.Errorf("ListMethods should have returned 'not found' error but instead returned %v", err)
	}
}

func TestGetAllFiles(t *testing.T) {
	expectedFiles := []string{"testing/test.proto"}
	// server reflection picks up filename from linked in Go package,
	// which indicates "grpc_testing/test.proto", not our local copy.
	expectedFilesWithReflection := [][]string{
		{"grpc_reflection_v1alpha/reflection.proto", "grpc_testing/test.proto"},
		// depending on the version of grpc, the filename could be prefixed with "interop/"
		{"grpc_reflection_v1alpha/reflection.proto", "interop/grpc_testing/test.proto"},
	}

	for _, ds := range descSources {
		t.Run(ds.name, func(t *testing.T) {
			files, err := GetAllFiles(ds.source)
			if err != nil {
				t.Fatalf("failed to get all files: %v", err)
			}
			names := fileNames(files)
			match := false
			var expected []string
			if ds.includeRefl {
				for _, expectedNames := range expectedFilesWithReflection {
					expected = expectedNames
					if reflect.DeepEqual(expected, names) {
						match = true
						break
					}
				}
			} else {
				expected = expectedFiles
				match = reflect.DeepEqual(expected, names)
			}
			if !match {
				t.Errorf("GetAllFiles returned wrong results: wanted %v, got %v", expected, names)
			}
		})
	}

	// try cases with more complicated set of files
	otherSourceProtoset, err := DescriptorSourceFromProtoSets("testing/test.protoset", "testing/example.protoset")
	if err != nil {
		t.Fatal(err.Error())
	}
	otherSourceProtoFiles, err := DescriptorSourceFromProtoFiles(nil, "testing/test.proto", "testing/example.proto")
	if err != nil {
		t.Fatal(err.Error())
	}
	otherDescSources := []descSourceCase{
		{"protoset[b]", otherSourceProtoset, false},
		{"proto[b]", otherSourceProtoFiles, false},
	}
	expectedFiles = []string{
		"google/protobuf/any.proto",
		"google/protobuf/descriptor.proto",
		"google/protobuf/empty.proto",
		"google/protobuf/timestamp.proto",
		"testing/example.proto",
		"testing/example2.proto",
		"testing/test.proto",
	}
	for _, ds := range otherDescSources {
		t.Run(ds.name, func(t *testing.T) {
			files, err := GetAllFiles(ds.source)
			if err != nil {
				t.Fatalf("failed to get all files: %v", err)
			}
			names := fileNames(files)
			if !reflect.DeepEqual(expectedFiles, names) {
				t.Errorf("GetAllFiles returned wrong results: wanted %v, got %v", expectedFiles, names)
			}
		})
	}
}

func TestExpandHeaders(t *testing.T) {
	inHeaders := []string{"key1: ${value}", "key2: bar", "key3: ${woo", "key4: woo}", "key5: ${TEST}",
		"key6: ${TEST_VAR}", "${TEST}: ${TEST_VAR}", "key8: ${EMPTY}"}
	os.Setenv("value", "value")
	os.Setenv("TEST", "value5")
	os.Setenv("TEST_VAR", "value6")
	os.Setenv("EMPTY", "")
	expectedHeaders := map[string]bool{"key1: value": true, "key2: bar": true, "key3: ${woo": true, "key4: woo}": true,
		"key5: value5": true, "key6: value6": true, "value5: value6": true, "key8: ": true}

	outHeaders, err := ExpandHeaders(inHeaders)
	if err != nil {
		t.Errorf("The ExpandHeaders function generated an unexpected error %s", err)
	}
	for _, expandedHeader := range outHeaders {
		if _, ok := expectedHeaders[expandedHeader]; !ok {
			t.Errorf("The ExpandHeaders function has returned an unexpected header. Received unexpected header %s", expandedHeader)
		}
	}

	badHeaders := []string{"key: ${DNE}"}
	_, err = ExpandHeaders(badHeaders)
	if err == nil {
		t.Errorf("The ExpandHeaders function should return an error for missing environment variables %q", badHeaders)
	}
}

func fileNames(files []*desc.FileDescriptor) []string {
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.GetName()
	}
	return names
}

const expectKnownType = `{
  "dur": "0s",
  "ts": "1970-01-01T00:00:00Z",
  "dbl": 0,
  "flt": 0,
  "i64": "0",
  "u64": "0",
  "i32": 0,
  "u32": 0,
  "bool": false,
  "str": "",
  "bytes": null,
  "st": {"google.protobuf.Struct": "supports arbitrary JSON objects"},
  "an": {"@type": "type.googleapis.com/google.protobuf.Empty", "value": {}},
  "lv": [{"google.protobuf.ListValue": "is an array of arbitrary JSON values"}],
  "val": {"google.protobuf.Value": "supports arbitrary JSON"}
}`

func TestMakeTemplateKnownTypes(t *testing.T) {
	descriptor, err := desc.LoadMessageDescriptorForMessage((*jsonpbtest.KnownTypes)(nil))
	if err != nil {
		t.Fatalf("failed to load descriptor: %v", err)
	}
	message := MakeTemplate(descriptor)

	jsm := jsonpb.Marshaler{EmitDefaults: true}
	out, err := jsm.MarshalToString(message)
	if err != nil {
		t.Fatalf("failed to marshal to JSON: %v", err)
	}

	// make sure template JSON matches expected
	var actual, expected interface{}
	if err := json.Unmarshal([]byte(out), &actual); err != nil {
		t.Fatalf("failed to parse actual JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(expectKnownType), &expected); err != nil {
		t.Fatalf("failed to parse expected JSON: %v", err)
	}

	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("template message is not as expected; want:\n%s\ngot:\n%s", expectKnownType, out)
	}
}

func TestDescribe(t *testing.T) {
	for _, ds := range descSources {
		t.Run(ds.name, func(t *testing.T) {
			doTestDescribe(t, ds.source)
		})
	}
}

func doTestDescribe(t *testing.T, source DescriptorSource) {
	sym := "grpc.testing.TestService.EmptyCall"
	dsc, err := source.FindSymbol(sym)
	if err != nil {
		t.Fatalf("failed to get descriptor for %q: %v", sym, err)
	}
	if _, ok := dsc.(*desc.MethodDescriptor); !ok {
		t.Fatalf("descriptor for %q was a %T (expecting a MethodDescriptor)", sym, dsc)
	}
	txt := proto.MarshalTextString(dsc.AsProto())
	expected :=
		`name: "EmptyCall"
input_type: ".grpc.testing.Empty"
output_type: ".grpc.testing.Empty"
`
	if expected != txt {
		t.Errorf("descriptor mismatch: expected %s, got %s", expected, txt)
	}

	sym = "grpc.testing.StreamingOutputCallResponse"
	dsc, err = source.FindSymbol(sym)
	if err != nil {
		t.Fatalf("failed to get descriptor for %q: %v", sym, err)
	}
	if _, ok := dsc.(*desc.MessageDescriptor); !ok {
		t.Fatalf("descriptor for %q was a %T (expecting a MessageDescriptor)", sym, dsc)
	}
	txt = proto.MarshalTextString(dsc.AsProto())
	expected =
		`name: "StreamingOutputCallResponse"
field: <
  name: "payload"
  number: 1
  label: LABEL_OPTIONAL
  type: TYPE_MESSAGE
  type_name: ".grpc.testing.Payload"
  json_name: "payload"
>
`
	if expected != txt {
		t.Errorf("descriptor mismatch: expected %s, got %s", expected, txt)
	}

	_, err = source.FindSymbol("FooService")
	if err != nil && !strings.Contains(err.Error(), "Symbol not found: FooService") {
		t.Errorf("FindSymbol should have returned 'not found' error but instead returned %v", err)
	}
}

const (
	// type == COMPRESSABLE, but that is default (since it has
	// numeric value == 0) and thus doesn't actually get included
	// on the wire
	payload1 = `{
  "payload": {
    "body": "SXQncyBCdXNpbmVzcyBUaW1l"
  }
}`
	payload2 = `{
  "payload": {
    "type": "RANDOM",
    "body": "Rm91eCBkdSBGYUZh"
  }
}`
	payload3 = `{
  "payload": {
    "type": "UNCOMPRESSABLE",
    "body": "SGlwaG9wb3BvdGFtdXMgdnMuIFJoeW1lbm9jZXJvcw=="
  }
}`
)

func getCC(includeRefl bool) *grpc.ClientConn {
	if includeRefl {
		return ccReflect
	} else {
		return ccNoReflect
	}
}

func TestUnary(t *testing.T) {
	for _, ds := range descSources {
		t.Run(ds.name, func(t *testing.T) {
			doTestUnary(t, getCC(ds.includeRefl), ds.source)
		})
	}
}

func doTestUnary(t *testing.T, cc *grpc.ClientConn, source DescriptorSource) {
	// Success
	h := &handler{reqMessages: []string{payload1}}
	err := InvokeRpc(context.Background(), source, cc, "grpc.testing.TestService/UnaryCall", makeHeaders(codes.OK), h, h.getRequestData)
	if err != nil {
		t.Fatalf("unexpected error during RPC: %v", err)
	}

	if h.check(t, "grpc.testing.TestService.UnaryCall", codes.OK, 1, 1) {
		if h.respMessages[0] != payload1 {
			t.Errorf("unexpected response from RPC: expecting %s; got %s", payload1, h.respMessages[0])
		}
	}

	// Failure
	h = &handler{reqMessages: []string{payload1}}
	err = InvokeRpc(context.Background(), source, cc, "grpc.testing.TestService/UnaryCall", makeHeaders(codes.NotFound), h, h.getRequestData)
	if err != nil {
		t.Fatalf("unexpected error during RPC: %v", err)
	}

	h.check(t, "grpc.testing.TestService.UnaryCall", codes.NotFound, 1, 0)
}

func TestClientStream(t *testing.T) {
	for _, ds := range descSources {
		t.Run(ds.name, func(t *testing.T) {
			doTestClientStream(t, getCC(ds.includeRefl), ds.source)
		})
	}
}

func doTestClientStream(t *testing.T, cc *grpc.ClientConn, source DescriptorSource) {
	// Success
	h := &handler{reqMessages: []string{payload1, payload2, payload3}}
	err := InvokeRpc(context.Background(), source, cc, "grpc.testing.TestService/StreamingInputCall", makeHeaders(codes.OK), h, h.getRequestData)
	if err != nil {
		t.Fatalf("unexpected error during RPC: %v", err)
	}

	if h.check(t, "grpc.testing.TestService.StreamingInputCall", codes.OK, 3, 1) {
		expected :=
			`{
  "aggregatedPayloadSize": 61
}`
		if h.respMessages[0] != expected {
			t.Errorf("unexpected response from RPC: expecting %s; got %s", expected, h.respMessages[0])
		}
	}

	// Fail fast (server rejects as soon as possible)
	h = &handler{reqMessages: []string{payload1, payload2, payload3}}
	err = InvokeRpc(context.Background(), source, cc, "grpc.testing.TestService/StreamingInputCall", makeHeaders(codes.InvalidArgument), h, h.getRequestData)
	if err != nil {
		t.Fatalf("unexpected error during RPC: %v", err)
	}

	h.check(t, "grpc.testing.TestService.StreamingInputCall", codes.InvalidArgument, -3, 0)

	// Fail late (server waits until stream is complete to reject)
	h = &handler{reqMessages: []string{payload1, payload2, payload3}}
	err = InvokeRpc(context.Background(), source, cc, "grpc.testing.TestService/StreamingInputCall", makeHeaders(codes.Internal, true), h, h.getRequestData)
	if err != nil {
		t.Fatalf("unexpected error during RPC: %v", err)
	}

	h.check(t, "grpc.testing.TestService.StreamingInputCall", codes.Internal, 3, 0)
}

func TestServerStream(t *testing.T) {
	for _, ds := range descSources {
		t.Run(ds.name, func(t *testing.T) {
			doTestServerStream(t, getCC(ds.includeRefl), ds.source)
		})
	}
}

func doTestServerStream(t *testing.T, cc *grpc.ClientConn, source DescriptorSource) {
	req := &grpc_testing.StreamingOutputCallRequest{
		ResponseType: grpc_testing.PayloadType_COMPRESSABLE,
		ResponseParameters: []*grpc_testing.ResponseParameters{
			{Size: 10}, {Size: 20}, {Size: 30}, {Size: 40}, {Size: 50},
		},
	}
	payload, err := (&jsonpb.Marshaler{}).MarshalToString(req)
	if err != nil {
		t.Fatalf("failed to construct request: %v", err)
	}

	// Success
	h := &handler{reqMessages: []string{payload}}
	err = InvokeRpc(context.Background(), source, cc, "grpc.testing.TestService/StreamingOutputCall", makeHeaders(codes.OK), h, h.getRequestData)
	if err != nil {
		t.Fatalf("unexpected error during RPC: %v", err)
	}

	if h.check(t, "grpc.testing.TestService.StreamingOutputCall", codes.OK, 1, 5) {
		resp := &grpc_testing.StreamingOutputCallResponse{}
		for i, msg := range h.respMessages {
			if err := jsonpb.UnmarshalString(msg, resp); err != nil {
				t.Errorf("failed to parse response %d: %v", i+1, err)
			}
			if resp.Payload.GetType() != grpc_testing.PayloadType_COMPRESSABLE {
				t.Errorf("response %d has wrong payload type; expecting %v, got %v", i, grpc_testing.PayloadType_COMPRESSABLE, resp.Payload.Type)
			}
			if len(resp.Payload.Body) != (i+1)*10 {
				t.Errorf("response %d has wrong payload size; expecting %d, got %d", i, (i+1)*10, len(resp.Payload.Body))
			}
			resp.Reset()
		}
	}

	// Fail fast (server rejects as soon as possible)
	h = &handler{reqMessages: []string{payload}}
	err = InvokeRpc(context.Background(), source, cc, "grpc.testing.TestService/StreamingOutputCall", makeHeaders(codes.Aborted), h, h.getRequestData)
	if err != nil {
		t.Fatalf("unexpected error during RPC: %v", err)
	}

	h.check(t, "grpc.testing.TestService.StreamingOutputCall", codes.Aborted, 1, 0)

	// Fail late (server waits until stream is complete to reject)
	h = &handler{reqMessages: []string{payload}}
	err = InvokeRpc(context.Background(), source, cc, "grpc.testing.TestService/StreamingOutputCall", makeHeaders(codes.AlreadyExists, true), h, h.getRequestData)
	if err != nil {
		t.Fatalf("unexpected error during RPC: %v", err)
	}

	h.check(t, "grpc.testing.TestService.StreamingOutputCall", codes.AlreadyExists, 1, 5)
}

func TestHalfDuplexStream(t *testing.T) {
	for _, ds := range descSources {
		t.Run(ds.name, func(t *testing.T) {
			doTestHalfDuplexStream(t, getCC(ds.includeRefl), ds.source)
		})
	}
}

func doTestHalfDuplexStream(t *testing.T, cc *grpc.ClientConn, source DescriptorSource) {
	reqs := []string{payload1, payload2, payload3}

	// Success
	h := &handler{reqMessages: reqs}
	err := InvokeRpc(context.Background(), source, cc, "grpc.testing.TestService/HalfDuplexCall", makeHeaders(codes.OK), h, h.getRequestData)
	if err != nil {
		t.Fatalf("unexpected error during RPC: %v", err)
	}

	if h.check(t, "grpc.testing.TestService.HalfDuplexCall", codes.OK, 3, 3) {
		for i, resp := range h.respMessages {
			if resp != reqs[i] {
				t.Errorf("unexpected response %d from RPC:\nexpecting %q\ngot %q", i, reqs[i], resp)
			}
		}
	}

	// Fail fast (server rejects as soon as possible)
	h = &handler{reqMessages: reqs}
	err = InvokeRpc(context.Background(), source, cc, "grpc.testing.TestService/HalfDuplexCall", makeHeaders(codes.Canceled), h, h.getRequestData)
	if err != nil {
		t.Fatalf("unexpected error during RPC: %v", err)
	}

	h.check(t, "grpc.testing.TestService.HalfDuplexCall", codes.Canceled, -3, 0)

	// Fail late (server waits until stream is complete to reject)
	h = &handler{reqMessages: reqs}
	err = InvokeRpc(context.Background(), source, cc, "grpc.testing.TestService/HalfDuplexCall", makeHeaders(codes.DataLoss, true), h, h.getRequestData)
	if err != nil {
		t.Fatalf("unexpected error during RPC: %v", err)
	}

	h.check(t, "grpc.testing.TestService.HalfDuplexCall", codes.DataLoss, 3, 3)
}

func TestFullDuplexStream(t *testing.T) {
	for _, ds := range descSources {
		t.Run(ds.name, func(t *testing.T) {
			doTestFullDuplexStream(t, getCC(ds.includeRefl), ds.source)
		})
	}
}

func doTestFullDuplexStream(t *testing.T, cc *grpc.ClientConn, source DescriptorSource) {
	reqs := make([]string, 3)
	req := &grpc_testing.StreamingOutputCallRequest{
		ResponseType: grpc_testing.PayloadType_RANDOM,
	}
	for i := range reqs {
		req.ResponseParameters = append(req.ResponseParameters, &grpc_testing.ResponseParameters{Size: int32((i + 1) * 10)})
		payload, err := (&jsonpb.Marshaler{}).MarshalToString(req)
		if err != nil {
			t.Fatalf("failed to construct request %d: %v", i, err)
		}
		reqs[i] = payload
	}

	// Success
	h := &handler{reqMessages: reqs}
	err := InvokeRpc(context.Background(), source, cc, "grpc.testing.TestService/FullDuplexCall", makeHeaders(codes.OK), h, h.getRequestData)
	if err != nil {
		t.Fatalf("unexpected error during RPC: %v", err)
	}

	if h.check(t, "grpc.testing.TestService.FullDuplexCall", codes.OK, 3, 6) {
		resp := &grpc_testing.StreamingOutputCallResponse{}
		i := 0
		for j := 1; j < 3; j++ {
			// three requests
			for k := 0; k < j; k++ {
				// 1 response for first request, 2 for second, etc
				msg := h.respMessages[i]
				if err := jsonpb.UnmarshalString(msg, resp); err != nil {
					t.Errorf("failed to parse response %d: %v", i+1, err)
				}
				if resp.Payload.GetType() != grpc_testing.PayloadType_RANDOM {
					t.Errorf("response %d has wrong payload type; expecting %v, got %v", i, grpc_testing.PayloadType_RANDOM, resp.Payload.Type)
				}
				if len(resp.Payload.Body) != (k+1)*10 {
					t.Errorf("response %d has wrong payload size; expecting %d, got %d", i, (k+1)*10, len(resp.Payload.Body))
				}
				resp.Reset()

				i++
			}
		}
	}

	// Fail fast (server rejects as soon as possible)
	h = &handler{reqMessages: reqs}
	err = InvokeRpc(context.Background(), source, cc, "grpc.testing.TestService/FullDuplexCall", makeHeaders(codes.PermissionDenied), h, h.getRequestData)
	if err != nil {
		t.Fatalf("unexpected error during RPC: %v", err)
	}

	h.check(t, "grpc.testing.TestService.FullDuplexCall", codes.PermissionDenied, -3, 0)

	// Fail late (server waits until stream is complete to reject)
	h = &handler{reqMessages: reqs}
	err = InvokeRpc(context.Background(), source, cc, "grpc.testing.TestService/FullDuplexCall", makeHeaders(codes.ResourceExhausted, true), h, h.getRequestData)
	if err != nil {
		t.Fatalf("unexpected error during RPC: %v", err)
	}

	h.check(t, "grpc.testing.TestService.FullDuplexCall", codes.ResourceExhausted, 3, 6)
}

type handler struct {
	method            *desc.MethodDescriptor
	methodCount       int
	reqHeaders        metadata.MD
	reqHeadersCount   int
	reqMessages       []string
	reqMessagesCount  int
	respHeaders       metadata.MD
	respHeadersCount  int
	respMessages      []string
	respTrailers      metadata.MD
	respStatus        *status.Status
	respTrailersCount int
}

func (h *handler) getRequestData() ([]byte, error) {
	// we don't use a mutex, though this method will be called from different goroutine
	// than other methods for bidi calls, because this method does not share any state
	// with the other methods.
	h.reqMessagesCount++
	if h.reqMessagesCount > len(h.reqMessages) {
		return nil, io.EOF
	}
	if h.reqMessagesCount > 1 {
		// insert delay between messages in request stream
		time.Sleep(time.Millisecond * 50)
	}
	return []byte(h.reqMessages[h.reqMessagesCount-1]), nil
}

func (h *handler) OnResolveMethod(md *desc.MethodDescriptor) {
	h.methodCount++
	h.method = md
}

func (h *handler) OnSendHeaders(md metadata.MD) {
	h.reqHeadersCount++
	h.reqHeaders = md
}

func (h *handler) OnReceiveHeaders(md metadata.MD) {
	h.respHeadersCount++
	h.respHeaders = md
}

func (h *handler) OnReceiveResponse(msg proto.Message) {
	jsm := jsonpb.Marshaler{Indent: "  "}
	respStr, err := jsm.MarshalToString(msg)
	if err != nil {
		panic(fmt.Errorf("failed to generate JSON form of response message: %v", err))
	}
	h.respMessages = append(h.respMessages, respStr)
}

func (h *handler) OnReceiveTrailers(stat *status.Status, md metadata.MD) {
	h.respTrailersCount++
	h.respTrailers = md
	h.respStatus = stat
}

func (h *handler) check(t *testing.T, expectedMethod string, expectedCode codes.Code, expectedRequestQueries, expectedResponses int) bool {
	// verify a few things were only ever called once
	if h.methodCount != 1 {
		t.Errorf("expected grpcurl to invoke OnResolveMethod once; was %d", h.methodCount)
	}
	if h.reqHeadersCount != 1 {
		t.Errorf("expected grpcurl to invoke OnSendHeaders once; was %d", h.reqHeadersCount)
	}
	if h.reqHeadersCount != 1 {
		t.Errorf("expected grpcurl to invoke OnSendHeaders once; was %d", h.reqHeadersCount)
	}
	if h.respHeadersCount != 1 {
		t.Errorf("expected grpcurl to invoke OnReceiveHeaders once; was %d", h.respHeadersCount)
	}
	if h.respTrailersCount != 1 {
		t.Errorf("expected grpcurl to invoke OnReceiveTrailers once; was %d", h.respTrailersCount)
	}

	// check other stuff against given expectations
	if h.method.GetFullyQualifiedName() != expectedMethod {
		t.Errorf("wrong method: expecting %v, got %v", expectedMethod, h.method.GetFullyQualifiedName())
	}
	if h.respStatus.Code() != expectedCode {
		t.Errorf("wrong code: expecting %v, got %v", expectedCode, h.respStatus.Code())
	}
	if expectedRequestQueries < 0 {
		// negative expectation means "negate and expect up to that number; could be fewer"
		if h.reqMessagesCount > -expectedRequestQueries+1 {
			// the + 1 is because there will be an extra query that returns EOF
			t.Errorf("wrong number of messages queried: expecting no more than %v, got %v", -expectedRequestQueries, h.reqMessagesCount-1)
		}
	} else {
		if h.reqMessagesCount != expectedRequestQueries+1 {
			// the + 1 is because there will be an extra query that returns EOF
			t.Errorf("wrong number of messages queried: expecting %v, got %v", expectedRequestQueries, h.reqMessagesCount-1)
		}
	}
	if len(h.respMessages) != expectedResponses {
		t.Errorf("wrong number of messages received: expecting %v, got %v", expectedResponses, len(h.respMessages))
	}

	// also check headers and trailers came through as expected
	v := h.respHeaders["some-fake-header-1"]
	if len(v) != 1 || v[0] != "val1" {
		t.Errorf("wrong request header for %q: %v", "some-fake-header-1", v)
	}
	v = h.respHeaders["some-fake-header-2"]
	if len(v) != 1 || v[0] != "val2" {
		t.Errorf("wrong request header for %q: %v", "some-fake-header-2", v)
	}
	v = h.respTrailers["some-fake-trailer-1"]
	if len(v) != 1 || v[0] != "valA" {
		t.Errorf("wrong request header for %q: %v", "some-fake-trailer-1", v)
	}
	v = h.respTrailers["some-fake-trailer-2"]
	if len(v) != 1 || v[0] != "valB" {
		t.Errorf("wrong request header for %q: %v", "some-fake-trailer-2", v)
	}

	return len(h.respMessages) == expectedResponses
}

func makeHeaders(code codes.Code, failLate ...bool) []string {
	if len(failLate) > 1 {
		panic("incorrect use of makeContext; should be at most one failLate flag")
	}

	hdrs := append(make([]string, 0, 5),
		fmt.Sprintf("%s: %s", grpcurl_testing.MetadataReplyHeaders, "some-fake-header-1: val1"),
		fmt.Sprintf("%s: %s", grpcurl_testing.MetadataReplyHeaders, "some-fake-header-2: val2"),
		fmt.Sprintf("%s: %s", grpcurl_testing.MetadataReplyTrailers, "some-fake-trailer-1: valA"),
		fmt.Sprintf("%s: %s", grpcurl_testing.MetadataReplyTrailers, "some-fake-trailer-2: valB"))
	if code != codes.OK {
		if len(failLate) > 0 && failLate[0] {
			hdrs = append(hdrs, fmt.Sprintf("%s: %d", grpcurl_testing.MetadataFailLate, code))
		} else {
			hdrs = append(hdrs, fmt.Sprintf("%s: %d", grpcurl_testing.MetadataFailEarly, code))
		}
	}

	return hdrs
}
