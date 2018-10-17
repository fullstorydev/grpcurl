package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes/struct"
	"github.com/jhump/protoreflect/desc"
	"google.golang.org/grpc/metadata"

	"github.com/fullstorydev/grpcurl"
)

func TestRequestFactory(t *testing.T) {
	source, err := grpcurl.DescriptorSourceFromProtoSets("../../testing/example.protoset")
	if err != nil {
		t.Fatalf("failed to create descriptor source: %v", err)
	}

	msg, err := makeProto()
	if err != nil {
		t.Fatalf("failed to create message: %v", err)
	}

	testCases := []struct {
		format         string
		input          string
		expectedOutput []proto.Message
	}{
		{
			format: "json",
			input:  "",
		},
		{
			format:         "json",
			input:          messageAsJSON,
			expectedOutput: []proto.Message{msg},
		},
		{
			format:         "json",
			input:          messageAsJSON + messageAsJSON + messageAsJSON,
			expectedOutput: []proto.Message{msg, msg, msg},
		},
		{
			// unlike JSON, empty input yields one empty message (vs. zero messages)
			format:         "text",
			input:          "",
			expectedOutput: []proto.Message{&structpb.Value{}},
		},
		{
			format:         "text",
			input:          messageAsText,
			expectedOutput: []proto.Message{msg},
		},
		{
			format:         "text",
			input:          messageAsText + string(textSeparatorChar),
			expectedOutput: []proto.Message{msg, &structpb.Value{}},
		},
		{
			format:         "text",
			input:          messageAsText + string(textSeparatorChar) + messageAsText + string(textSeparatorChar) + messageAsText,
			expectedOutput: []proto.Message{msg, msg, msg},
		},
	}

	for i, tc := range testCases {
		name := fmt.Sprintf("#%d, %s, %d message(s)", i+1, tc.format, len(tc.expectedOutput))
		rf, _ := formatDetails(tc.format, source, false, strings.NewReader(tc.input))
		numReqs := 0
		for {
			var req structpb.Value
			err := rf.next(&req)
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
		if rf.numRequests() != numReqs {
			t.Errorf("%s: factory reported wrong number of requests: expecting %d, got %d", name, numReqs, rf.numRequests())
		}
	}
}

// Handler prints response data (and headers/trailers in verbose mode).
// This verifies that we get the right output in both JSON and proto text modes.
func TestHandler(t *testing.T) {
	source, err := grpcurl.DescriptorSourceFromProtoSets("../../testing/example.protoset")
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

	for _, format := range []string{"json", "text"} {
		for _, numMessages := range []int{1, 3} {
			for _, verbose := range []bool{true, false} {
				name := fmt.Sprintf("%s, %d message(s)", format, numMessages)
				if verbose {
					name += ", verbose"
				}

				_, formatter := formatDetails(format, source, verbose, nil)

				var buf bytes.Buffer
				h := handler{
					out:        &buf,
					descSource: source,
					verbose:    verbose,
					formatter:  formatter,
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
					t.Errorf("%s: Incorrect output.", name) // Expected:\n%s\nGot:\n%s", name, expectedOutput, out)
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
{
  "name": "GetFiles",
  "inputType": ".TestRequest",
  "outputType": ".TestResponse",
  "options": {
    
  }
}

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
