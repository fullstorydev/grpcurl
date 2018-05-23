// Command grpcurl makes GRPC requests (a la cURL, but HTTP/2). It can use a supplied descriptor file or
// service reflection to translate JSON request data into the appropriate protobuf request data and vice
// versa for presenting the response contents.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	descpb "github.com/golang/protobuf/protoc-gen-go/descriptor"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/grpcreflect"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"

	"github.com/fullstorydev/grpcurl"
)

var (
	exit = os.Exit

	isUnixSocket func() bool // nil when run on non-unix platform

	help = flag.Bool("help", false,
		`Print usage instructions and exit.`)
	plaintext = flag.Bool("plaintext", false,
		`Use plain-text HTTP/2 when connecting to server (no TLS).`)
	insecure = flag.Bool("insecure", false,
		`Skip server certificate and domain verification. (NOT SECURE!). Not
    	valid with -plaintext option.`)
	cacert = flag.String("cacert", "",
		`File containing trusted root certificates for verifying the server.
    	Ignored if -insecure is specified.`)
	cert = flag.String("cert", "",
		`File containing client certificate (public key), to present to the
    	server. Not valid with -plaintext option. Must also provide -key option.`)
	key = flag.String("key", "",
		`File containing client private key, to present to the server. Not valid
    	with -plaintext option. Must also provide -cert option.`)
	protoset    multiString
	protoFiles  multiString
	importPaths multiString
	addlHeaders multiString
	rpcHeaders  multiString
	reflHeaders multiString
	data        = flag.String("d", "",
		`JSON request contents. If the value is '@' then the request contents are
    	read from stdin. For calls that accept a stream of requests, the
    	contents should include all such request messages concatenated together
    	(optionally separated by whitespace).`)
	connectTimeout = flag.String("connect-timeout", "",
		`The maximum time, in seconds, to wait for connection to be established.
    	Defaults to 10 seconds.`)
	keepaliveTime = flag.String("keepalive-time", "",
		`If present, the maximum idle time in seconds, after which a keepalive
    	probe is sent. If the connection remains idle and no keepalive response
    	is received for this same period then the connection is closed and the
    	operation fails.`)
	maxTime = flag.String("max-time", "",
		`The maximum total time the operation can take. This is useful for
    	preventing batch jobs that use grpcurl from hanging due to slow or bad
    	network links or due to incorrect stream method usage.`)
	emitDefaults = flag.Bool("emit-defaults", false,
		`Emit default values from JSON-encoded responses.`)
	msgTemplate = flag.Bool("msg-template", false,
		`When describing messages, show a JSON template for the message type.`)
	verbose = flag.Bool("v", false,
		`Enable verbose output.`)
	serverName = flag.String("servername", "", "Override servername when validating TLS certificate.")
)

func init() {
	flag.Var(&addlHeaders, "H",
		`Additional headers in 'name: value' format. May specify more than one
    	via multiple flags. These headers will also be included in reflection
    	requests requests to a server.`)
	flag.Var(&rpcHeaders, "rpc-header",
		`Additional RPC headers in 'name: value' format. May specify more than
    	one via multiple flags. These headers will *only* be used when invoking
    	the requested RPC method. They are excluded from reflection requests.`)
	flag.Var(&reflHeaders, "reflect-header",
		`Additional reflection headers in 'name: value' format. May specify more
    	than one via multiple flags. These headers will only be used during
    	reflection requests and will be excluded when invoking the requested RPC
    	method.`)
	flag.Var(&protoset, "protoset",
		`The name of a file containing an encoded FileDescriptorSet. This file's
    	contents will be used to determine the RPC schema instead of querying
    	for it from the remote server via the GRPC reflection API. When set: the
    	'list' action lists the services found in the given descriptors (vs.
    	those exposed by the remote server), and the 'describe' action describes
    	symbols found in the given descriptors. May specify more than one via
    	multiple -protoset flags. It is an error to use both -protoset and
    	-proto flags.`)
	flag.Var(&protoFiles, "proto",
		`The name of a proto source file. Source files given will be used to
    	determine the RPC schema instead of querying for it from the remote
    	server via the GRPC reflection API. When set: the 'list' action lists
    	the services found in the given files and their imports (vs. those
    	exposed by the remote server), and the 'describe' action describes
    	symbols found in the given files. May specify more than one via
    	multiple -proto flags. Imports will be resolved using the given
    	-import-path flags. Multiple proto files can be specified by specifying
    	multiple -proto flags. It is an error to use both -protoset and -proto
    	flags.`)
	flag.Var(&importPaths, "import-path",
		`The path to a directory from which proto sources can be imported,
    	for use with -proto flags. Multiple import paths can be configured by
    	specifying multiple -import-path flags. Paths will be searched in the
    	order given. If no import paths are given, all files (including all
    	imports) must be provided as -proto flags, and grpcurl will attempt to
    	resolve all import statements from the set of file names given.`)
}

type multiString []string

func (s *multiString) String() string {
	return strings.Join(*s, ",")
}

func (s *multiString) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	flag.CommandLine.Usage = usage
	flag.Parse()
	if *help {
		usage()
		os.Exit(0)
	}

	// Do extra validation on arguments and figure out what user asked us to do.
	if *plaintext && *insecure {
		fail(nil, "The -plaintext and -insecure arguments are mutually exclusive.")
	}
	if *plaintext && *cert != "" {
		fail(nil, "The -plaintext and -cert arguments are mutually exclusive.")
	}
	if *plaintext && *key != "" {
		fail(nil, "The -plaintext and -key arguments are mutually exclusive.")
	}
	if (*key == "") != (*cert == "") {
		fail(nil, "The -cert and -key arguments must be used together and both be present.")
	}

	args := flag.Args()

	if len(args) == 0 {
		fail(nil, "Too few arguments.")
	}
	var target string
	if args[0] != "list" && args[0] != "describe" {
		target = args[0]
		args = args[1:]
	}

	if len(args) == 0 {
		fail(nil, "Too few arguments.")
	}
	var list, describe, invoke bool
	if args[0] == "list" {
		list = true
		args = args[1:]
	} else if args[0] == "describe" {
		describe = true
		args = args[1:]
	} else {
		invoke = true
	}

	var symbol string
	if invoke {
		if len(args) == 0 {
			fail(nil, "Too few arguments.")
		}
		symbol = args[0]
		args = args[1:]
	} else {
		if *data != "" {
			warn("The -d argument is not used with 'list' or 'describe' verb.")
		}
		if len(rpcHeaders) > 0 {
			warn("The -rpc-header argument is not used with 'list' or 'describe' verb.")
		}
		if len(args) > 0 {
			symbol = args[0]
			args = args[1:]
		}
	}

	if len(args) > 0 {
		fail(nil, "Too many arguments.")
	}
	if invoke && target == "" {
		fail(nil, "No host:port specified.")
	}
	if len(protoset) == 0 && len(protoFiles) == 0 && target == "" {
		fail(nil, "No host:port specified, no protoset specified, and no proto sources specified.")
	}
	if len(protoset) > 0 && len(reflHeaders) > 0 {
		warn("The -reflect-header argument is not used when -protoset files are used.")
	}
	if len(protoset) > 0 && len(protoFiles) > 0 {
		fail(nil, "Use either -protoset files or -proto files, but not both.")
	}
	if len(importPaths) > 0 && len(protoFiles) == 0 {
		warn("The -import-path argument is not used unless -proto files are used.")
	}

	ctx := context.Background()
	if *maxTime != "" {
		t, err := strconv.ParseFloat(*maxTime, 64)
		if err != nil {
			fail(nil, "The -max-time argument must be a valid number.")
		}
		timeout := time.Duration(t * float64(time.Second))
		ctx, _ = context.WithTimeout(ctx, timeout)
	}

	dial := func() *grpc.ClientConn {
		dialTime := 10 * time.Second
		if *connectTimeout != "" {
			t, err := strconv.ParseFloat(*connectTimeout, 64)
			if err != nil {
				fail(nil, "The -connect-timeout argument must be a valid number.")
			}
			dialTime = time.Duration(t * float64(time.Second))
		}
		ctx, cancel := context.WithTimeout(ctx, dialTime)
		defer cancel()
		var opts []grpc.DialOption
		if *keepaliveTime != "" {
			t, err := strconv.ParseFloat(*keepaliveTime, 64)
			if err != nil {
				fail(nil, "The -keepalive-time argument must be a valid number.")
			}
			timeout := time.Duration(t * float64(time.Second))
			opts = append(opts, grpc.WithKeepaliveParams(keepalive.ClientParameters{
				Time:    timeout,
				Timeout: timeout,
			}))
		}
		var creds credentials.TransportCredentials
		if !*plaintext {
			var err error
			creds, err = grpcurl.ClientTransportCredentials(*insecure, *cacert, *cert, *key)
			if err != nil {
				fail(err, "Failed to configure transport credentials")
			}
			if *serverName != "" {
				if err := creds.OverrideServerName(*serverName); err != nil {
					fail(err, "Failed to override server name as %q", *serverName)
				}
			}
		}
		network := "tcp"
		if isUnixSocket != nil && isUnixSocket() {
			network = "unix"
		}
		cc, err := grpcurl.BlockingDial(ctx, network, target, creds, opts...)
		if err != nil {
			fail(err, "Failed to dial target host %q", target)
		}
		return cc
	}

	var cc *grpc.ClientConn
	var descSource grpcurl.DescriptorSource
	var refClient *grpcreflect.Client
	if len(protoset) > 0 {
		var err error
		descSource, err = grpcurl.DescriptorSourceFromProtoSets(protoset...)
		if err != nil {
			fail(err, "Failed to process proto descriptor sets.")
		}
	} else if len(protoFiles) > 0 {
		var err error
		descSource, err = grpcurl.DescriptorSourceFromProtoFiles(importPaths, protoFiles...)
		if err != nil {
			fail(err, "Failed to process proto source files.")
		}
	} else {
		md := grpcurl.MetadataFromHeaders(append(addlHeaders, reflHeaders...))
		refCtx := metadata.NewOutgoingContext(ctx, md)
		cc = dial()
		refClient = grpcreflect.NewClient(refCtx, reflectpb.NewServerReflectionClient(cc))
		descSource = grpcurl.DescriptorSourceFromServer(ctx, refClient)
	}

	// arrange for the RPCs to be cleanly shutdown
	reset := func() {
		if refClient != nil {
			refClient.Reset()
			refClient = nil
		}
		if cc != nil {
			cc.Close()
			cc = nil
		}
	}
	defer reset()
	exit = func(code int) {
		// since defers aren't run by os.Exit...
		reset()
		os.Exit(code)
	}

	if list {
		if symbol == "" {
			svcs, err := grpcurl.ListServices(descSource)
			if err != nil {
				fail(err, "Failed to list services")
			}
			if len(svcs) == 0 {
				fmt.Println("(No services)")
			} else {
				for _, svc := range svcs {
					fmt.Printf("%s\n", svc)
				}
			}
		} else {
			methods, err := grpcurl.ListMethods(descSource, symbol)
			if err != nil {
				fail(err, "Failed to list methods for service %q", symbol)
			}
			if len(methods) == 0 {
				fmt.Println("(No methods)") // probably unlikely
			} else {
				for _, m := range methods {
					fmt.Printf("%s\n", m)
				}
			}
		}

	} else if describe {
		var symbols []string
		if symbol != "" {
			symbols = []string{symbol}
		} else {
			// if no symbol given, describe all exposed services
			svcs, err := descSource.ListServices()
			if err != nil {
				fail(err, "Failed to list services")
			}
			if len(svcs) == 0 {
				fmt.Println("Server returned an empty list of exposed services")
			}
			symbols = svcs
		}
		for _, s := range symbols {
			dsc, err := descSource.FindSymbol(s)
			if err != nil {
				fail(err, "Failed to resolve symbol %q", s)
			}

			txt, err := grpcurl.GetDescriptorText(dsc, descSource)
			if err != nil {
				fail(err, "Failed to describe symbol %q", s)
			}

			switch dsc.(type) {
			case *desc.MessageDescriptor:
				fmt.Printf("%s is a message:\n", dsc.GetFullyQualifiedName())
			case *desc.FieldDescriptor:
				fmt.Printf("%s is a field:\n", dsc.GetFullyQualifiedName())
			case *desc.OneOfDescriptor:
				fmt.Printf("%s is a one-of:\n", dsc.GetFullyQualifiedName())
			case *desc.EnumDescriptor:
				fmt.Printf("%s is an enum:\n", dsc.GetFullyQualifiedName())
			case *desc.EnumValueDescriptor:
				fmt.Printf("%s is an enum value:\n", dsc.GetFullyQualifiedName())
			case *desc.ServiceDescriptor:
				fmt.Printf("%s is a service:\n", dsc.GetFullyQualifiedName())
			case *desc.MethodDescriptor:
				fmt.Printf("%s is a method:\n", dsc.GetFullyQualifiedName())
			default:
				err = fmt.Errorf("descriptor has unrecognized type %T", dsc)
				fail(err, "Failed to describe symbol %q", s)
			}
			fmt.Println(txt)

			if dsc, ok := dsc.(*desc.MessageDescriptor); ok && *msgTemplate {
				// for messages, also show a template in JSON, to make it easier to
				// create a request to invoke an RPC
				tmpl := makeTemplate(dynamic.NewMessage(dsc))
				fmt.Println("\nMessage template:")
				jsm := jsonpb.Marshaler{Indent: "  ", EmitDefaults: true}
				err := jsm.Marshal(os.Stdout, tmpl)
				if err != nil {
					fail(err, "Failed to print template for message %s", s)
				}
				fmt.Println()
			}
		}

	} else {
		// Invoke an RPC
		if cc == nil {
			cc = dial()
		}
		var dec *json.Decoder
		if *data == "@" {
			dec = json.NewDecoder(os.Stdin)
		} else {
			dec = json.NewDecoder(strings.NewReader(*data))
		}

		h := &handler{dec: dec, descSource: descSource}
		err := grpcurl.InvokeRpc(ctx, descSource, cc, symbol, append(addlHeaders, rpcHeaders...), h, h.getRequestData)
		if err != nil {
			fail(err, "Error invoking method %q", symbol)
		}
		reqSuffix := ""
		respSuffix := ""
		if h.reqCount != 1 {
			reqSuffix = "s"
		}
		if h.respCount != 1 {
			respSuffix = "s"
		}
		if *verbose {
			fmt.Printf("Sent %d request%s and received %d response%s\n", h.reqCount, reqSuffix, h.respCount, respSuffix)
		}
		if h.stat.Code() != codes.OK {
			fmt.Fprintf(os.Stderr, "ERROR:\n  Code: %s\n  Message: %s\n", h.stat.Code().String(), h.stat.Message())
			exit(1)
		}
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
	%s [flags] [address] [list|describe] [symbol]

The 'host:port' is only optional when used with 'list' or 'describe' and a
protoset flag is provided.

If 'list' is indicated, the symbol (if present) should be a fully-qualified
service name. If present, all methods of that service are listed. If not
present, all exposed services are listed, or all services defined in protosets.

If 'describe' is indicated, the descriptor for the given symbol is shown. The
symbol should be a fully-qualified service, enum, or message name. If no symbol
is given then the descriptors for all exposed or known services are shown.

If neither verb is present, the symbol must be a fully-qualified method name in
'service/method' or 'service.method' format. In this case, the request body will
be used to invoke the named method. If no body is given, an empty instance of
the method's request type will be sent.

The address will typically be in the form "host:port" where host can be an IP
address or a hostname and port is a numeric port or service name. If an IPv6
address is given, it must be surrounded by brackets, like "[2001:db8::1]". For
Unix variants, if a -unix=true flag is present, then the address must be the
path to the domain socket.
`, os.Args[0])
	flag.PrintDefaults()

}

func warn(msg string, args ...interface{}) {
	msg = fmt.Sprintf("Warning: %s\n", msg)
	fmt.Fprintf(os.Stderr, msg, args...)
}

func fail(err error, msg string, args ...interface{}) {
	if err != nil {
		msg += ": %v"
		args = append(args, err)
	}
	fmt.Fprintf(os.Stderr, msg, args...)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		exit(1)
	} else {
		// nil error means it was CLI usage issue
		fmt.Fprintf(os.Stderr, "Try '%s -help' for more details.\n", os.Args[0])
		exit(2)
	}
}

type handler struct {
	dec        *json.Decoder
	descSource grpcurl.DescriptorSource
	reqCount   int
	respCount  int
	stat       *status.Status
}

func (h *handler) OnResolveMethod(md *desc.MethodDescriptor) {
	if *verbose {
		txt, err := grpcurl.GetDescriptorText(md, h.descSource)
		if err == nil {
			fmt.Printf("\nResolved method descriptor:\n%s\n", txt)
		}
	}
}

func (*handler) OnSendHeaders(md metadata.MD) {
	if *verbose {
		fmt.Printf("\nRequest metadata to send:\n%s\n", grpcurl.MetadataToString(md))
	}
}

func (h *handler) getRequestData() ([]byte, error) {
	// we don't use a mutex, though this methods will be called from different goroutine
	// than other methods for bidi calls, because this method does not share any state
	// with the other methods.
	var msg json.RawMessage
	if err := h.dec.Decode(&msg); err != nil {
		return nil, err
	}
	h.reqCount++
	return msg, nil
}

func (*handler) OnReceiveHeaders(md metadata.MD) {
	if *verbose {
		fmt.Printf("\nResponse headers received:\n%s\n", grpcurl.MetadataToString(md))
	}
}

func (h *handler) OnReceiveResponse(resp proto.Message) {
	h.respCount++
	if *verbose {
		fmt.Print("\nResponse contents:\n")
	}
	jsm := jsonpb.Marshaler{EmitDefaults: *emitDefaults, Indent: "  "}
	respStr, err := jsm.MarshalToString(resp)
	if err != nil {
		fail(err, "failed to generate JSON form of response message")
	}
	fmt.Println(respStr)
}

func (h *handler) OnReceiveTrailers(stat *status.Status, md metadata.MD) {
	h.stat = stat
	if *verbose {
		fmt.Printf("\nResponse trailers received:\n%s\n", grpcurl.MetadataToString(md))
	}
}

// makeTemplate fleshes out the given message so that it is a suitable template for creating
// an instance of that message in JSON. In particular, it ensures that any repeated fields
// (which include map fields) are not empty, so they will render with a single element (to
// show the types and optionally nested fields). It also ensures that nested messages are
// not nil by setting them to a message that is also fleshed out as a template message.
func makeTemplate(msg proto.Message) proto.Message {
	dm, ok := msg.(*dynamic.Message)
	if !ok {
		return msg
	}
	// for repeated fields, add a single element with default value
	// and for message fields, add a message with all default fields
	// that also has non-nil message and non-empty repeated fields
	for _, fd := range dm.GetMessageDescriptor().GetFields() {
		if fd.IsRepeated() {
			switch fd.GetType() {
			case descpb.FieldDescriptorProto_TYPE_FIXED32,
				descpb.FieldDescriptorProto_TYPE_UINT32:
				dm.AddRepeatedField(fd, uint32(0))

			case descpb.FieldDescriptorProto_TYPE_SFIXED32,
				descpb.FieldDescriptorProto_TYPE_SINT32,
				descpb.FieldDescriptorProto_TYPE_INT32,
				descpb.FieldDescriptorProto_TYPE_ENUM:
				dm.AddRepeatedField(fd, int32(0))

			case descpb.FieldDescriptorProto_TYPE_FIXED64,
				descpb.FieldDescriptorProto_TYPE_UINT64:
				dm.AddRepeatedField(fd, uint64(0))

			case descpb.FieldDescriptorProto_TYPE_SFIXED64,
				descpb.FieldDescriptorProto_TYPE_SINT64,
				descpb.FieldDescriptorProto_TYPE_INT64:
				dm.AddRepeatedField(fd, int64(0))

			case descpb.FieldDescriptorProto_TYPE_STRING:
				dm.AddRepeatedField(fd, "")

			case descpb.FieldDescriptorProto_TYPE_BYTES:
				dm.AddRepeatedField(fd, []byte{})

			case descpb.FieldDescriptorProto_TYPE_BOOL:
				dm.AddRepeatedField(fd, false)

			case descpb.FieldDescriptorProto_TYPE_FLOAT:
				dm.AddRepeatedField(fd, float32(0))

			case descpb.FieldDescriptorProto_TYPE_DOUBLE:
				dm.AddRepeatedField(fd, float64(0))

			case descpb.FieldDescriptorProto_TYPE_MESSAGE,
				descpb.FieldDescriptorProto_TYPE_GROUP:
				dm.AddRepeatedField(fd, makeTemplate(dynamic.NewMessage(fd.GetMessageType())))
			}
		} else if fd.GetMessageType() != nil {
			dm.SetField(fd, makeTemplate(dynamic.NewMessage(fd.GetMessageType())))
		}
	}
	return dm
}
