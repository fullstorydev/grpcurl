package lib

import (
	"testing"
)

func TestClientTLSConfig(t *testing.T) {
	derfmt := CertKeyFormatDER
	pemfmt := CertKeyFormatPEM
	pfxfmt := CertKeyFormatPKCS12
	testTLSConfig(t, false, "../../testing/tls/ca.crt", pemfmt, "../../testing/tls/client.crt", pemfmt, "../../testing/tls/client.key", pemfmt, "")
	testTLSConfig(t, false, "../../testing/tls/ca.crt", pemfmt, "../../testing/tls/client.der", derfmt, "../../testing/tls/client.key", pemfmt, "")
	testTLSConfig(t, false, "../../testing/tls/ca.crt", pemfmt, "../../testing/tls/client.pfx", pfxfmt, "../../testing/tls/client.key", pemfmt, "")
	testTLSConfig(t, false, "../../testing/tls/ca.crt", pemfmt, "../../testing/tls/client_pass.pfx", pfxfmt, "", pemfmt, "pfxpassword")
	testTLSConfig(t, false, "../../testing/tls/ca.der", derfmt, "../../testing/tls/client.pfx", pfxfmt, "", pemfmt, "")
	testTLSConfig(t, false, "../../testing/tls/ca.crt", pemfmt, "../../testing/tls/testcert.pem", pemfmt, "../../testing/tls/testkey.pem", pemfmt, "")
}

func testTLSConfig(
	t *testing.T,
	insecure bool,
	cacert string,
	cacertFormat CertificateKeyFormat,
	cert string,
	certFormat CertificateKeyFormat,
	key string,
	keyFormat CertificateKeyFormat,
	pass string,
) {
	tlsConf, err := ClientTLSConfigV2(insecure, cacert, cacertFormat, cert, certFormat, key, keyFormat, pass)
	if err != nil {
		t.Fatalf("Failed to create TLS config err: %v", err)
	}
	if tlsConf == nil || tlsConf.Certificates == nil || tlsConf.RootCAs == nil {
		t.Fatal("Failed to create TLS config tlsConf is nil")
	}

}

func TestGuessFormat(t *testing.T) {
	guessFormat(t, "../../testing/tls/client.crt", CertKeyFormatPEM)
	guessFormat(t, "../../testing/tls/client.cer", CertKeyFormatPEM)
	guessFormat(t, "../../testing/tls/client.key", CertKeyFormatPEM)
	guessFormat(t, "../../testing/tls/client.pfx", CertKeyFormatPKCS12)
	guessFormat(t, "../../testing/tls/client.der", CertKeyFormatDER)
	forceFormat(t, "../../testing/tls/client.guess", CertKeyFormatPEM, CertKeyFormatPEM)
}

func guessFormat(t *testing.T, filename string, formatExpected CertificateKeyFormat) {
	forceFormat(t, filename, formatExpected, CertKeyFormatNONE)
}

func forceFormat(t *testing.T, filename string, formatExpected, formatForce CertificateKeyFormat) {
	guessFormat, err := GuessFormatForFile(filename, formatForce)
	if err != nil {
		t.Fatalf("failed to guess file err: %v", err)
	}
	if guessFormat != formatExpected {
		t.Fatalf("failed to guess file %v format: %v expected: %v", filename, guessFormat, formatExpected)
	}
	t.Logf("format %v filename %v", guessFormat, filename)
}
