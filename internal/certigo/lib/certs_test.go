package lib

import (
	"testing"
)

func TestClientTLSConfig(t *testing.T) {
	derfmt := CertKeyFormatDER
	pemfmt := CertKeyFormatPEM
	pfxfmt := CertKeyFormatPKCS12
	testTLSConfig(t, false, "tls/ca.crt", pemfmt, "tls/client.crt", pemfmt, "tls/client.key", pemfmt, "")
	testTLSConfig(t, false, "tls/ca.crt", pemfmt, "tls/client.der", derfmt, "tls/client.key", pemfmt, "")
	testTLSConfig(t, false, "tls/ca.crt", pemfmt, "tls/client.pfx", pfxfmt, "tls/client.key", pemfmt, "")
	testTLSConfig(t, false, "tls/ca.crt", pemfmt, "tls/client_pass.pfx", pfxfmt, "", pemfmt, "pfxpassword")
	testTLSConfig(t, false, "tls/ca.der", derfmt, "tls/client.pfx", pfxfmt, "", pemfmt, "")
	//testTLSConfig(t, false, "tls/ca.crt", pemfmt, "tls/client.crt", pemfmt, "tls/client.key.pass", pemfmt, "123456") // not support
	//testTLSConfig(t, false, "tls/ca.crt", pemfmt, "tls/client_pass.pfx", pfxfmt, "", pemfmt, "invalidpwd") // invalid
	//testTLSConfig(t, false, "tls/ca.crt", pemfmt, "tls/client.der", derfmt, "tls/client.key.der", derfmt, "") key can not be der
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
	guessFormat(t, "tls/client.crt", CertKeyFormatPEM)
	guessFormat(t, "tls/client.cer", CertKeyFormatPEM)
	guessFormat(t, "tls/client.key", CertKeyFormatPEM)
	guessFormat(t, "tls/client.pfx", CertKeyFormatPKCS12)
	guessFormat(t, "tls/client.der", CertKeyFormatDER)
	forceFormat(t, "tls/client.guess", CertKeyFormatPEM, CertKeyFormatPEM)
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
