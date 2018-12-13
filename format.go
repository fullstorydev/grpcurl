package grpcurl

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/dynamic"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// RequestParser processes input into messages.
type RequestParser interface {
	// Next parses input data into the given request message. If called after
	// input is exhausted, it returns io.EOF. If the caller re-uses the same
	// instance in multiple calls to Next, it should call msg.Reset() in between
	// each call.
	Next(msg proto.Message) error
	// NumRequests returns the number of messages that have been parsed and
	// returned by a call to Next.
	NumRequests() int
}

type jsonRequestParser struct {
	dec          *json.Decoder
	unmarshaler  jsonpb.Unmarshaler
	requestCount int
}

// NewJSONRequestParser returns a RequestParser that reads data in JSON format
// from the given reader. The given resolver is used to assist with decoding of
// google.protobuf.Any messages.
//
// Input data that contains more than one message should just include all
// messages concatenated (though whitespace is necessary to separate some kinds
// of values in JSON).
//
// If the given reader has no data, the returned parser will return io.EOF on
// the very first call.
func NewJSONRequestParser(in io.Reader, resolver jsonpb.AnyResolver) RequestParser {
	return &jsonRequestParser{
		dec:         json.NewDecoder(in),
		unmarshaler: jsonpb.Unmarshaler{AnyResolver: resolver},
	}
}

func (f *jsonRequestParser) Next(m proto.Message) error {
	var msg json.RawMessage
	if err := f.dec.Decode(&msg); err != nil {
		return err
	}
	f.requestCount++
	return f.unmarshaler.Unmarshal(bytes.NewReader(msg), m)
}

func (f *jsonRequestParser) NumRequests() int {
	return f.requestCount
}

const (
	textSeparatorChar = 0x1e
)

type textRequestParser struct {
	r            *bufio.Reader
	err          error
	requestCount int
}

// NewTextRequestParser returns a RequestParser that reads data in the protobuf
// text format from the given reader.
//
// Input data that contains more than one message should include an ASCII
// 'Record Separator' character (0x1E) between each message.
//
// Empty text is a valid text format and represents an empty message. So if the
// given reader has no data, the returned parser will yield an empty message
// for the first call to Next and then return io.EOF thereafter. This also means
// that if the input data ends with a record separator, then a final empty
// message will be parsed *after* the separator.
func NewTextRequestParser(in io.Reader) RequestParser {
	return &textRequestParser{r: bufio.NewReader(in)}
}

func (f *textRequestParser) Next(m proto.Message) error {
	if f.err != nil {
		return f.err
	}

	var b []byte
	b, f.err = f.r.ReadBytes(textSeparatorChar)
	if f.err != nil && f.err != io.EOF {
		return f.err
	}
	// remove delimiter
	if len(b) > 0 && b[len(b)-1] == textSeparatorChar {
		b = b[:len(b)-1]
	}

	f.requestCount++

	return proto.UnmarshalText(string(b), m)
}

func (f *textRequestParser) NumRequests() int {
	return f.requestCount
}

// Formatter translates messages into string representations.
type Formatter func(proto.Message) (string, error)

// NewJSONFormatter returns a formatter that returns JSON strings. The JSON will
// include empty/default values (instead of just omitted them) if emitDefaults
// is true. The given resolver is used to assist with encoding of
// google.protobuf.Any messages.
func NewJSONFormatter(emitDefaults bool, resolver jsonpb.AnyResolver) Formatter {
	marshaler := jsonpb.Marshaler{
		EmitDefaults: emitDefaults,
		Indent:       "  ",
		AnyResolver:  resolver,
	}
	return marshaler.MarshalToString
}

// NewTextFormatter returns a formatter that returns strings in the protobuf
// text format. If includeSeparator is true then, when invoked to format
// multiple messages, all messages after the first one will be prefixed with the
// ASCII 'Record Separator' character (0x1E).
func NewTextFormatter(includeSeparator bool) Formatter {
	tf := textFormatter{useSeparator: includeSeparator}
	return tf.format
}

type textFormatter struct {
	useSeparator bool
	numFormatted int
}

func (tf *textFormatter) format(m proto.Message) (string, error) {
	var buf bytes.Buffer
	if tf.useSeparator && tf.numFormatted > 0 {
		if err := buf.WriteByte(textSeparatorChar); err != nil {
			return "", err
		}
	}

	// If message implements MarshalText method (such as a *dynamic.Message),
	// it won't get details about whether or not to format to text compactly
	// or with indentation. So first see if the message also implements a
	// MarshalTextIndent method and use that instead if available.
	type indentMarshaler interface {
		MarshalTextIndent() ([]byte, error)
	}

	if indenter, ok := m.(indentMarshaler); ok {
		b, err := indenter.MarshalTextIndent()
		if err != nil {
			return "", err
		}
		if _, err := buf.Write(b); err != nil {
			return "", err
		}
	} else if err := proto.MarshalText(&buf, m); err != nil {
		return "", err
	}

	// no trailing newline needed
	str := buf.String()
	if str[len(str)-1] == '\n' {
		str = str[:len(str)-1]
	}

	tf.numFormatted++

	return str, nil
}

type Format string

const (
	FormatJSON = Format("json")
	FormatText = Format("text")
)

func anyResolver(source DescriptorSource) (jsonpb.AnyResolver, error) {
	// TODO: instead of pro-actively downloading file descriptors to
	// build a dynamic resolver, it would be better if the resolver
	// impl was lazy, and simply downloaded the descriptors as needed
	// when asked to resolve a particular type URL

	// best effort: build resolver with whatever files we can
	// load, ignoring any errors
	files, _ := GetAllFiles(source)

	var er dynamic.ExtensionRegistry
	for _, fd := range files {
		er.AddExtensionsFromFile(fd)
	}
	mf := dynamic.NewMessageFactoryWithExtensionRegistry(&er)
	return dynamic.AnyResolver(mf, files...), nil
}

// RequestParserAndFormatterFor returns a request parser and formatter for the
// given format. The given descriptor source may be used for parsing message
// data (if needed by the format). The flags emitJSONDefaultFields and
// includeTextSeparator are options for JSON and protobuf text formats,
// respectively. Requests will be parsed from the given in.
func RequestParserAndFormatterFor(format Format, descSource DescriptorSource, emitJSONDefaultFields, includeTextSeparator bool, in io.Reader) (RequestParser, Formatter, error) {
	switch format {
	case FormatJSON:
		resolver, err := anyResolver(descSource)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating message resolver: %v", err)
		}
		return NewJSONRequestParser(in, resolver), NewJSONFormatter(emitJSONDefaultFields, resolver), nil
	case FormatText:
		return NewTextRequestParser(in), NewTextFormatter(includeTextSeparator), nil
	default:
		return nil, nil, fmt.Errorf("unknown format: %s", format)
	}
}

// DefaultEventHandler logs events to a writer. This is not thread-safe, but is
// safe for use with InvokeRPC as long as NumResponses and Status are not read
// until the call to InvokeRPC completes.
type DefaultEventHandler struct {
	out        io.Writer
	descSource DescriptorSource
	formatter  func(proto.Message) (string, error)
	verbose    bool

	// NumResponses is the number of responses that have been received.
	NumResponses int
	// Status is the status that was received at the end of an RPC. It is
	// nil if the RPC is still in progress.
	Status *status.Status
}

// NewDefaultEventHandler returns an InvocationEventHandler that logs events to
// the given output. If verbose is true, all events are logged. Otherwise, only
// response messages are logged.
func NewDefaultEventHandler(out io.Writer, descSource DescriptorSource, formatter Formatter, verbose bool) *DefaultEventHandler {
	return &DefaultEventHandler{
		out:        out,
		descSource: descSource,
		formatter:  formatter,
		verbose:    verbose,
	}
}

var _ InvocationEventHandler = (*DefaultEventHandler)(nil)

func (h *DefaultEventHandler) OnResolveMethod(md *desc.MethodDescriptor) {
	if h.verbose {
		txt, err := GetDescriptorText(md, h.descSource)
		if err == nil {
			fmt.Fprintf(h.out, "\nResolved method descriptor:\n%s\n", txt)
		}
	}
}

func (h *DefaultEventHandler) OnSendHeaders(md metadata.MD) {
	if h.verbose {
		fmt.Fprintf(h.out, "\nRequest metadata to send:\n%s\n", MetadataToString(md))
	}
}

func (h *DefaultEventHandler) OnReceiveHeaders(md metadata.MD) {
	if h.verbose {
		fmt.Fprintf(h.out, "\nResponse headers received:\n%s\n", MetadataToString(md))
	}
}

func (h *DefaultEventHandler) OnReceiveResponse(resp proto.Message) {
	h.NumResponses++
	if h.verbose {
		fmt.Fprint(h.out, "\nResponse contents:\n")
	}
	if respStr, err := h.formatter(resp); err != nil {
		fmt.Fprintf(h.out, "Failed to format response message %d: %v\n", h.NumResponses, err)
	} else {
		fmt.Fprintln(h.out, respStr)
	}
}

func (h *DefaultEventHandler) OnReceiveTrailers(stat *status.Status, md metadata.MD) {
	h.Status = stat
	if h.verbose {
		fmt.Fprintf(h.out, "\nResponse trailers received:\n%s\n", MetadataToString(md))
	}
}
