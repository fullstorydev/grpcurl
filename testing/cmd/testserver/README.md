# testserver

The `testserver` program is a simple server that can be used for testing RPC clients such
as `grpcurl`. It implements an RPC interface that is defined in `grpcurl`'s [testing package](https://github.com/fullstorydev/grpcurl/blob/master/testing/example.proto) and also exposes [the implementation](https://godoc.org/github.com/fullstorydev/grpcurl/testing#TestServer) that is defined in that same package. This is the same test interface and implementation that is used in unit tests for `grpcurl`.

For a possibly more interesting test server, take a look at `bankdemo`, which is a demo gRPC app that provides a more concrete RPC interface, including full-duplex bidirectional streaming methods, plus an example implementation.