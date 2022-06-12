"""Bazel macro to run a binary copy of gRPCurl."""

def grpcurl(name, descriptor, args = []):
    """
    Run a gRPCurl command with the specified `args` and `descriptor` set.

    Parameters:
        name: The name of the target to run.
        descriptor: The target for the protocol buffer descriptor set.
        args: Arguments to pass to the command.
    """
    pass
