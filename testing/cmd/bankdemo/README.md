# bankdemo

The `bankdemo` program is an example gRPC server that was used to demo `grpcurl` at Gophercon 2018.

It demonstrates interesting concepts for building a gRPC server, including chat functionality (that relies on full-duplex bidirectional streams). This code was written specifically to provide an interesting concrete demonstration and, as such, should not be considered in any way production-worthy.

The demo app tracks user accounts, transactions, and balances completely in memory. Every few seconds, as well as on graceful shutdown (like when the server receives a SIGTERM or SIGINT signal), this state is saved to a file named `accounts.json`, so that the data can be restored if the process restarts.

In addition to bank account data, the server also tracks "chat sessions", for demonstrating bidirectional streams in the form of an application where customers can chat with support agents.
