package grpcurl

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"testing"
	"time"
)

// reproducer case to show that TLS 1.3 handshake fails to reject bad client certs
func TestTLS13RejectsBadCerts(t *testing.T) {
	serverTlsConf, err := makeServerConfig()
	if err != nil {
		t.Fatalf("Could not create server TLS config: %v", err.Error())
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Could not create TCP listener: %v", err.Error())
	}
	defer l.Close()
	startServer(l, serverTlsConf)
	addr := l.Addr()

	// this one should work (valid, trusted client cert)
	err = tryClient(t, addr, "testing/tls/client.crt", "testing/tls/client.key")
	if err != nil {
		t.Errorf("Expecting success; instead got: %v", err.Error())
	}

	// use cert that is expired
	err = tryClient(t, addr, "testing/tls/expired.crt", "testing/tls/expired.key")
	if err == nil {
		t.Errorf("Expecting failure due to use of expired cert!")
	} else {
		t.Logf("Expired client cert resulted in failed handshake (as expected): %v", err.Error())
	}

	// use cert issued by ca that is not trusted
	err = tryClient(t, addr, "testing/tls/wrong-client.crt", "testing/tls/wrong-client.key")
	if err == nil {
		t.Errorf("Expecting failure due to use of untrusted cert!")
	} else {
		t.Logf("Untrusted client cert resulted in failed handshake (as expected): %v", err.Error())
	}

	// no client cert
	err = tryClient(t, addr, "", "")
	if err == nil {
		t.Errorf("Expecting failure due to missing cert!")
	} else {
		t.Logf("Absent client cert resulted in failed handshake (as expected): %v", err.Error())
	}
}

func makeServerConfig() (*tls.Config, error) {
	var tlsConf tls.Config

	// Load server cert
	certificate, err := tls.LoadX509KeyPair("testing/tls/server.crt", "testing/tls/server.key")
	if err != nil {
		return nil, fmt.Errorf("failed to create server creds: %v", err)
	}
	tlsConf.Certificates = []tls.Certificate{certificate}

	ca, err := makeCAs()
	if err != nil {
		return nil, fmt.Errorf("failed to create CA cert pool: %v", err)
	}
	tlsConf.ClientCAs = ca

	// Client certs required!
	tlsConf.ClientAuth = tls.RequireAndVerifyClientCert

	return &tlsConf, nil
}

func makeCAs() (*x509.CertPool, error) {
	// Create a certificate pool from the certificate authority
	certPool := x509.NewCertPool()
	ca, err := ioutil.ReadFile("testing/tls/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("could not read ca certificate: %v", err)
	}

	// Append the certificates from the CA
	if ok := certPool.AppendCertsFromPEM(ca); !ok {
		return nil, errors.New("failed to append ca certs")
	}

	return certPool, nil
}

func startServer(l net.Listener, tlsConf *tls.Config) {
	// spawn acceptor goroutine
	go func() {
		for {
			cn, err := l.Accept()
			if err != nil {
				return
			}

			// spawn per-connection goroutine
			go func() {
				tlsCn := tls.Server(cn, tlsConf)
				defer tlsCn.Close()

				if err := tlsCn.Handshake(); err != nil {
					fmt.Printf("Bad handshake: %v\n", err.Error())
				}
			}()
		}
	}()
}

func tryClient(t *testing.T, addr net.Addr, certFile, keyFile string) error {
	tlsConf, err := makeClientConfig(certFile, keyFile)
	if err != nil {
		t.Errorf("failed to load client certs: %v", err.Error())
		return nil
	}

	cn, err := net.DialTimeout(addr.Network(), addr.String(), 3*time.Second)
	if err != nil {
		t.Errorf("failed to connect to server: %v", err.Error())
		return nil
	}

	tlsCn := tls.Client(cn, tlsConf)
	defer tlsCn.Close()

	return tlsCn.Handshake()
}

func makeClientConfig(certFile, keyFile string) (*tls.Config, error) {
	var tlsConf tls.Config

	if certFile != "" {
		// Load client cert if specified
		certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to create client creds: %v", err)
		}
		tlsConf.Certificates = []tls.Certificate{certificate}
	}

	ca, err := makeCAs()
	if err != nil {
		return nil, fmt.Errorf("failed to create CA cert pool: %v", err)
	}
	tlsConf.RootCAs = ca

	tlsConf.ServerName = "127.0.0.1"

	return &tlsConf, nil
}
