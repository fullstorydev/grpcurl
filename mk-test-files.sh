#!/bin/bash

set -ex

cd "$(dirname $0)"

# Run this script to generate files used by tests.

echo "Creating protosets..."
protoc testing/test.proto \
	--include_imports \
	--descriptor_set_out=testing/test.protoset

protoc testing/example.proto \
	--include_imports \
	--descriptor_set_out=testing/example.protoset

protoc testing/jsonpb_test_proto/test_objects.proto \
	--go_out=paths=source_relative:.

echo "Creating certs for TLS testing..."
if ! hash certstrap 2>/dev/null; then
  # certstrap not found: try to install it
  go get github.com/square/certstrap
  go install github.com/square/certstrap
fi

function cs() {
	certstrap --depot-path testing/tls "$@" --passphrase ""
}

rm -rf testing/tls

# Create CA
cs init --years 10 --common-name ca

# Create client cert
cs request-cert --common-name client
cs sign client --years 10 --CA ca

# Create server cert
cs request-cert --common-name server --ip 127.0.0.1 --domain localhost
cs sign server --years 10 --CA ca

# Create another server cert for error testing
cs request-cert --common-name other --ip 1.2.3.4 --domain foobar.com
cs sign other --years 10 --CA ca

# Create another CA and client cert for more
# error testing
cs init --years 10 --common-name wrong-ca
cs request-cert --common-name wrong-client
cs sign wrong-client --years 10 --CA wrong-ca

# Create expired cert
cs request-cert --common-name expired --ip 127.0.0.1 --domain localhost
cs sign expired --years 0 --CA ca

## Create DER PKCS12 file
#openssl x509  -outform der -in testing/tls/ca.crt -out testing/tls/ca.der
#openssl x509  -outform der -in testing/tls/client.crt -out testing/tls/client.der
#openssl x509  -outform der -in testing/tls/client.crt -out testing/tls/client.der
#openssl x509  -text -in testing/tls/client.crt > testing/tls/client.cer
#sed '1s/^/invalidGuess/' testing/tls/client.cer > testing/tls/client.guess
#openssl pkcs12 -export \
#	-in        testing/tls/client.crt \
#	-inkey     testing/tls/client.key \
#	-certfile  testing/tls/ca.crt \
#	-out       testing/tls/client.pfx \
#	-password pass:
#openssl pkcs12 -export \
#	-in        testing/tls/client.crt \
#	-inkey     testing/tls/client.key \
#	-certfile  testing/tls/ca.crt \
#	-out       testing/tls/client_pass.pfx \
#	-password pass:pfxpassword

