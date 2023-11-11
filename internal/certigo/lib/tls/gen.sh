
set -ex

# generate der and pkcs12 file
openssl x509  -outform der -in tls/ca.crt -out tls/ca.der
openssl x509  -outform der -in tls/client.crt -out tls/client.der
openssl pkcs12 -export \
	-in tls/client.crt \
	-inkey tls/client.key \
	-certfile tls/ca.crt \
	-out tls/client.pfx \
	-password pass:
openssl pkcs12 -export \
	-in tls/client.crt \
	-inkey tls/client.key \
	-certfile tls/ca.crt \
	-out tls/client_pass.pfx \
	-password pass:pfxpassword
