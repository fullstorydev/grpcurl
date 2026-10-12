package grpcurl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	"github.com/jhump/protoreflect/desc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestFormatStatusDetails(t *testing.T) {
	// The source deliberately has no descriptors for error details. Do not import
	// errdetails here: that would register its types and hide a missing production
	// import. These payloads are protobuf wire encodings of the expected details.
	source, err := DescriptorSourceFromFileDescriptors()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "google.rpc.ErrorInfo",
			payload: "\x0a\x06DENIED\x12\x0bexample.com",
			want:    `{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"DENIED","domain":"example.com"}`,
		},
		{
			name:    "google.rpc.BadRequest",
			payload: "\x0a\x10\x0a\x04name\x12\x08required",
			want:    `{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"name","description":"required"}]}`,
		},
		{
			name:    "google.rpc.RetryInfo",
			payload: "\x0a\x02\x08\x03",
			want:    `{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"3s"}`,
		},
		{
			name:    "test.UnknownErrorDetail",
			payload: "\x0a\x03foo",
			want:    `{"@type":"type.googleapis.com/test.UnknownErrorDetail","@error":"test.UnknownErrorDetail is not recognized; see @value for raw binary message data","@value":"CgNmb28="}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, formatter, err := RequestParserAndFormatter(FormatJSON, source, nil, FormatOptions{})
			if err != nil {
				t.Fatal(err)
			}
			stat := status.New(codes.InvalidArgument, "invalid request").Proto()
			stat.Details = []*anypb.Any{{
				TypeUrl: "type.googleapis.com/" + tc.name,
				Value:   []byte(tc.payload),
			}}
			got, err := formatter(stat)
			if err != nil {
				t.Fatal(err)
			}
			want := `{"code":3,"message":"invalid request","details":[` + tc.want + `]}`
			var gotJSON, wantJSON interface{}
			if err := json.Unmarshal([]byte(got), &gotJSON); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(want), &wantJSON); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotJSON, wantJSON) {
				t.Errorf("unexpected status JSON:\nwant: %s\ngot: %s", want, got)
			}
		})
	}
}

func TestRequestParser(t *testing.T) {
	source, err := DescriptorSourceFromProtoSets("internal/testing/example.protoset")
	if err != nil {
		t.Fatalf("failed to create descriptor source: %v", err)
	}

	msg, err := makeProto()
	if err != nil {
		t.Fatalf("failed to create message: %v", err)
	}

	testCases := []struct {
		format         Format
		input          string
		expectedOutput []proto.Message
	}{
		{
			format: FormatJSON,
			input:  "",
		},
		{
			format:         FormatJSON,
			input:          messageAsJSON,
			expectedOutput: []proto.Message{msg},
		},
		{
			format:         FormatJSON,
			input:          messageAsJSON + messageAsJSON + messageAsJSON,
			expectedOutput: []proto.Message{msg, msg, msg},
		},
		{
			// unlike JSON, empty input yields one empty message (vs. zero messages)
			format:         FormatText,
			input:          "",
			expectedOutput: []proto.Message{&structpb.Value{}},
		},
		{
			format:         FormatText,
			input:          messageAsText,
			expectedOutput: []proto.Message{msg},
		},
		{
			format:         FormatText,
			input:          messageAsText + string(textSeparatorChar),
			expectedOutput: []proto.Message{msg, &structpb.Value{}},
		},
		{
			format:         FormatText,
			input:          messageAsText + string(textSeparatorChar) + messageAsText + string(textSeparatorChar) + messageAsText,
			expectedOutput: []proto.Message{msg, msg, msg},
		},
	}

	for i, tc := range testCases {
		name := fmt.Sprintf("#%d, %s, %d message(s)", i+1, tc.format, len(tc.expectedOutput))
		rf, _, err := RequestParserAndFormatter(tc.format, source, strings.NewReader(tc.input), FormatOptions{})
		if err != nil {
			t.Errorf("Failed to create parser and formatter: %v", err)
			continue
		}
		numReqs := 0
		for {
			var req structpb.Value
			err := rf.Next(&req)
			if err == io.EOF {
				break
			} else if err != nil {
				t.Errorf("%s, msg %d: unexpected error: %v", name, numReqs, err)
			}
			if !proto.Equal(&req, tc.expectedOutput[numReqs]) {
				t.Errorf("%s, msg %d: incorrect message;\nexpecting:\n%v\ngot:\n%v", name, numReqs, tc.expectedOutput[numReqs], &req)
			}
			numReqs++
		}
		if rf.NumRequests() != numReqs {
			t.Errorf("%s: factory reported wrong number of requests: expecting %d, got %d", name, numReqs, rf.NumRequests())
		}
	}
}

// Handler prints response data (and headers/trailers in verbose mode).
// This verifies that we get the right output in both JSON and proto text modes.
func TestHandler(t *testing.T) {
	source, err := DescriptorSourceFromProtoSets("internal/testing/example.protoset")
	if err != nil {
		t.Fatalf("failed to create descriptor source: %v", err)
	}
	d, err := source.FindSymbol("TestService.GetFiles")
	if err != nil {
		t.Fatalf("failed to find method 'TestService.GetFiles': %v", err)
	}
	md, ok := d.(*desc.MethodDescriptor)
	if !ok {
		t.Fatalf("wrong kind of descriptor found: %T", d)
	}

	reqHeaders := metadata.Pairs("foo", "123", "bar", "456")
	respHeaders := metadata.Pairs("foo", "abc", "bar", "def", "baz", "xyz")
	respTrailers := metadata.Pairs("a", "1", "b", "2", "c", "3")
	rsp, err := makeProto()
	if err != nil {
		t.Fatalf("failed to create response message: %v", err)
	}

	for _, format := range []Format{FormatJSON, FormatText} {
		for _, numMessages := range []int{1, 3} {
			for verbosityLevel := 0; verbosityLevel <= 2; verbosityLevel++ {
				name := fmt.Sprintf("%s, %d message(s)", format, numMessages)
				if verbosityLevel > 0 {
					name += fmt.Sprintf(", verbosityLevel=%d", verbosityLevel)
				}

				verbose := verbosityLevel > 0

				_, formatter, err := RequestParserAndFormatter(format, source, bytes.NewReader(nil), FormatOptions{IncludeTextSeparator: !verbose})
				if err != nil {
					t.Errorf("Failed to create parser and formatter: %v", err)
					continue
				}

				var buf bytes.Buffer
				h := &DefaultEventHandler{
					Out:            &buf,
					Formatter:      formatter,
					VerbosityLevel: verbosityLevel,
				}

				h.OnResolveMethod(md)
				h.OnSendHeaders(reqHeaders)
				h.OnReceiveHeaders(respHeaders)
				for i := 0; i < numMessages; i++ {
					h.OnReceiveResponse(rsp)
				}
				h.OnReceiveTrailers(nil, respTrailers)

				expectedOutput := ""
				if verbose {
					expectedOutput += verbosePrefix
				}
				for i := 0; i < numMessages; i++ {
					if verbosityLevel > 1 {
						expectedOutput += verboseResponseSize
					}
					if verbose {
						expectedOutput += verboseResponseHeader
					}
					if format == "json" {
						expectedOutput += messageAsJSON
					} else {
						if i > 0 && !verbose {
							expectedOutput += string(textSeparatorChar)
						}
						expectedOutput += messageAsText
					}
				}
				if verbose {
					expectedOutput += verboseSuffix
				}

				out := buf.String()
				if !compare(out, expectedOutput) {
					t.Errorf("%s: Incorrect output. Expected:\n%s\nGot:\n%s", name, expectedOutput, out)
				}
			}
		}
	}
}

// compare checks that actual and expected are equal, returning true if so.
// A simple equality check (==) does not suffice because jsonpb formats
// structpb.Value strangely. So if that formatting gets fixed, we don't
// want this test in grpcurl to suddenly start failing. So we check each
// line and compare the lines after stripping whitespace (which removes
// the jsonpb format anomalies).
func compare(actual, expected string) bool {
	actualLines := strings.Split(actual, "\n")
	expectedLines := strings.Split(expected, "\n")
	if len(actualLines) != len(expectedLines) {
		return false
	}
	for i := 0; i < len(actualLines); i++ {
		if strings.TrimSpace(actualLines[i]) != strings.TrimSpace(expectedLines[i]) {
			return false
		}
	}
	return true
}

func makeProto() (proto.Message, error) {
	var rsp structpb.Value
	err := jsonpb.UnmarshalString(`{
		"foo": ["abc", "def", "ghi"],
		"bar": { "a": 1, "b": 2 },
		"baz": true,
		"null": null
	}`, &rsp)
	if err != nil {
		return nil, err
	}
	return &rsp, nil
}

var (
	verbosePrefix = `
Resolved method descriptor:
rpc GetFiles ( .TestRequest ) returns ( .TestResponse );

Request metadata to send:
bar: 456
foo: 123

Response headers received:
bar: def
baz: xyz
foo: abc
`
	verboseSuffix = `
Response trailers received:
a: 1
b: 2
c: 3
`
	verboseResponseSize = `
Estimated response size: 100 bytes
`
	verboseResponseHeader = `
Response contents:
`
	messageAsJSON = `{
  "bar": {
    "a": 1,
    "b": 2
  },
  "baz": true,
  "foo": [
    "abc",
    "def",
    "ghi"
  ],
  "null": null
}
`
	messageAsText = `struct_value: <
  fields: <
    key: "bar"
    value: <
      struct_value: <
        fields: <
          key: "a"
          value: <
            number_value: 1
          >
        >
        fields: <
          key: "b"
          value: <
            number_value: 2
          >
        >
      >
    >
  >
  fields: <
    key: "baz"
    value: <
      bool_value: true
    >
  >
  fields: <
    key: "foo"
    value: <
      list_value: <
        values: <
          string_value: "abc"
        >
        values: <
          string_value: "def"
        >
        values: <
          string_value: "ghi"
        >
      >
    >
  >
  fields: <
    key: "null"
    value: <
      null_value: NULL_VALUE
    >
  >
>
`
)
