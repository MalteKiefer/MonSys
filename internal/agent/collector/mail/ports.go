package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

type portSpec struct {
	Port  int
	Proto string
	TLS   bool
}

func checkPort(ctx context.Context, host string, p portSpec) apitypes.MailPortCheck {
	addr := net.JoinHostPort(host, strconv.Itoa(p.Port))

	// Plain TCP dial to verify the port is reachable at all.
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return apitypes.MailPortCheck{Port: p.Port, Proto: p.Proto, Open: false}
	}
	conn.Close()

	if !p.TLS {
		return apitypes.MailPortCheck{Port: p.Port, Proto: p.Proto, Open: true}
	}

	// TLS path: first attempt with full verification.
	result := apitypes.MailPortCheck{Port: p.Port, Proto: p.Proto, Open: true}

	tlsConn, err := tlsDial(ctx, addr, host, false, 2*time.Second)
	if err == nil {
		t := tlsConn.ConnectionState().PeerCertificates[0].NotAfter
		tlsConn.Close()
		result.TLS = true
		result.CertTrusted = true
		result.CertNotAfter = &t
		return result
	}

	// Verification/handshake failed — retry insecurely solely to read leaf NotAfter.
	tlsConn, err = tlsDial(ctx, addr, host, true, 2*time.Second)
	if err != nil {
		// Even insecure dial failed; port is open (TCP connected) but TLS unusable.
		return result
	}
	t := tlsConn.ConnectionState().PeerCertificates[0].NotAfter
	tlsConn.Close()
	result.TLS = true
	result.CertTrusted = false
	result.CertNotAfter = &t
	return result
}

// tlsDial opens a TLS connection to addr. When insecure is true the server
// certificate is not verified — this is used solely to read the leaf certificate
// expiry date; CertTrusted is surfaced as false to the caller.
func tlsDial(ctx context.Context, addr, serverName string, insecure bool, timeout time.Duration) (*tls.Conn, error) {
	cfg := &tls.Config{
		ServerName: serverName,
	}
	if insecure {
		cfg.InsecureSkipVerify = true //nolint:gosec // localhost cert-expiry read, not a trust decision; CertTrusted surfaced
	}
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: timeout}, Config: cfg}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("dial did not return a TLS connection")
	}
	return tlsConn, nil
}
