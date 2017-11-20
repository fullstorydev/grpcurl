# GRPCurl
`grpcurl` is a command-line tool that lets you interact with GRPC servers. It's
basically `curl` for GRPC servers.

The main purpose for this tool is to invoke RPC methods on a GRPC server from the
command-line. GRPC servers use a binary encoding on the wire
([protocol buffers](https://developers.google.com/protocol-buffers/), or "protobufs"
for short). So they are basically impossible to interact with using regular `curl`
(and older versions of `curl` that do not support HTTP/2 are of course non-starters).
This program accepts messages using JSON encoding, which is much more friendly for both
humans and scripts.

With this tool you can also browse the schema for GRPC services, either by querying
a server that supports [service reflection](https://github.com/grpc/grpc/blob/master/src/proto/grpc/reflection/v1alpha/reflection.proto)
or by loading in "protoset" files (files that contain encoded file
[descriptor protos](https://github.com/google/protobuf/blob/master/src/google/protobuf/descriptor.proto)).
In fact, the way the tool transforms JSON request data into a binary encoded protobuf
is using that very same schema. So, if the server you interact with does not support
reflection, you will need to build "protoset" files that `grpcurl` can use.

This code for this tool is also a great example of how to use the various packages of
the [protoreflect](https://godoc.org/github.com/jhump/protoreflect) library, and shows
off what they can do.

## Features
`grpcurl` supports all kinds of RPC methods, including streaming methods. You can even
operate bi-directional streaming methods interactively by running `grpcurl` from an
interactive terminal and using stdin as the request body!

`grpcurl` supports both plain-text and TLS servers and has numerous options for TLS
configuration. It also supports mutual TLS, where the client is required to present a
client certificate.

As mentioned above, `grpcurl` works seamlessly if the server supports the reflection
service. If not, you must use `protoc` to build protoset files and provide those to
`grpcurl`.

## Example Usage
Invoking an RPC on a trusted server (e.g. TLS without self-signed key or custom CA)
that requires no client certs and supports service reflection is the simplest thing to
do with `grpcurl`. This minimal invocation sends an empty request body:
```
grpcurl grpc.server.com:443 my.custom.server.Service/Method
```

To list all services exposed by a server, use the "list" verb. When using protoset files
instead of server reflection, this lists all services defined in the protoset files.
```
grpcurl localhost:80808 list

grpcurl -protoset my-protos.bin list
```

The "list" verb also lets you see all methods in a particular service:
```
grpcurl localhost:80808 list my.custom.server.Service
```

The "describe" verb will print the type of any symbol that the server knows about
or that is found in a given protoset file and also print the full descriptor for the
symbol, in JSON.
```
grpcurl localhost:80808 describe my.custom.server.Service.MethodOne

grpcurl -protoset my-protos.bin describe my.custom.server.Service.MethodOne
```

The usage doc for the tool explains the numerous options:
```
grpcurl -help
```

## Protoset Files
To use `grpcurl` on servers that do not support reflection, you need to compile the
`*.proto` files that describe the service into files containing encoded
`FileDescriptorSet` protos, also known as "protoset" files.

```
protoc --proto_path=. \
    --descriptor_set_out=myservice.protoset \
    --include_imports \
    my/custom/server/service.proto
```

The `--descriptor_set_out` argument is what tells `protoc` to produce a protoset,
and the `--include_imports` arguments is necessary for the protoset to contain
everything that `grpcurl` needs to process and understand the schema.