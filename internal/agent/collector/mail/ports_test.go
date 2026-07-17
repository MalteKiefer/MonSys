package mail

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"
)

// selfSignedTLSConfig generates an in-memory self-signed TLS certificate valid
// for ~1 year and returns a tls.Config suitable for a test server plus the
// NotAfter time embedded in the leaf certificate.
func selfSignedTLSConfig(t *testing.T) (*tls.Config, time.Time) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	notBefore := time.Now().Add(-time.Minute)
	notAfter := notBefore.Add(365 * 24 * time.Hour)

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, priv.Public(), priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("x509 key pair: %v", err)
	}

	cfg := &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	return cfg, notAfter
}

func TestCheckPort_PlainOpen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Accept loop so the client's TCP handshake completes.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	host, port := splitHostPort(t, ln.Addr().String())
	spec := portSpec{Port: port, Proto: "tcp", TLS: false}

	result := checkPort(context.Background(), host, host, spec)

	if !result.Open {
		t.Error("expected Open=true for a listening port")
	}
	if result.TLS {
		t.Error("expected TLS=false for a plain listener")
	}
}

func TestCheckPort_Closed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close() // close immediately so the port is no longer listening

	host, port := splitHostPort(t, addr)
	spec := portSpec{Port: port, Proto: "tcp", TLS: false}

	result := checkPort(context.Background(), host, host, spec)

	if result.Open {
		t.Error("expected Open=false for a closed port")
	}
}

func TestCheckPort_TLSSelfSigned(t *testing.T) {
	tlsCfg, wantNotAfter := selfSignedTLSConfig(t)

	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatalf("tls listen: %v", err)
	}
	defer ln.Close()

	// Accept loop — complete the TLS handshake and close.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				//nolint:errcheck
				c.(*tls.Conn).Handshake()
				c.Close()
			}(conn)
		}
	}()

	host, port := splitHostPort(t, ln.Addr().String())
	spec := portSpec{Port: port, Proto: "smtp", TLS: true}

	result := checkPort(context.Background(), host, host, spec)

	if !result.Open {
		t.Error("expected Open=true")
	}
	if !result.TLS {
		t.Error("expected TLS=true")
	}
	if result.CertTrusted {
		t.Error("expected CertTrusted=false for a self-signed cert")
	}
	if result.CertNotAfter == nil {
		t.Fatal("expected CertNotAfter to be set")
	}
	// Truncate to second precision for comparison.
	got := result.CertNotAfter.UTC().Truncate(time.Second)
	want := wantNotAfter.UTC().Truncate(time.Second)
	if !got.Equal(want) {
		t.Errorf("CertNotAfter: got %v, want %v", got, want)
	}
}

// splitHostPort is a test helper that splits an address string and converts
// the port to an int.
func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	h, ps, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host port %q: %v", addr, err)
	}
	port, err := strconv.Atoi(ps)
	if err != nil {
		t.Fatalf("parse port %q: %v", ps, err)
	}
	return h, port
}
