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

// checkPort dials dialHost:port (loopback) to test reachability. For TLS ports
// the certificate is verified against serverName — the host's FQDN, NOT the dial
// address — because mail certs (e.g. Let's Encrypt) are issued for the public
// hostname; verifying against 127.0.0.1 would spuriously fail and mislabel a
// valid cert as untrusted (self-signed).
func checkPort(ctx context.Context, dialHost, serverName string, p portSpec) apitypes.MailPortCheck {
	addr := net.JoinHostPort(dialHost, strconv.Itoa(p.Port))

	// Plain TCP dial to verify the port is reachable at all.
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return apitypes.MailPortCheck{Port: p.Port, Proto: p.Proto, Open: false}
	}
	conn.Close()

	if !p.TLS {
		return apitypes.MailPortCheck{Port: p.Port, Proto: p.Proto, Open: true}
	}

	// TLS path: first attempt with full verification against the FQDN.
	result := apitypes.MailPortCheck{Port: p.Port, Proto: p.Proto, Open: true}

	if tlsConn, derr := tlsDial(ctx, addr, serverName, false, 2*time.Second); derr == nil {
		if exp, ok := leafNotAfter(tlsConn); ok {
			result.TLS = true
			result.CertTrusted = true
			result.CertNotAfter = &exp
		}
		tlsConn.Close()
		if result.TLS {
			return result
		}
	}

	// Verification/handshake failed — retry insecurely solely to read leaf NotAfter.
	tlsConn, err := tlsDial(ctx, addr, serverName, true, 2*time.Second)
	if err != nil {
		// Even insecure dial failed; port is open (TCP connected) but TLS unusable.
		return result
	}
	if exp, ok := leafNotAfter(tlsConn); ok {
		result.TLS = true
		result.CertTrusted = false
		result.CertNotAfter = &exp
	}
	tlsConn.Close()
	return result
}

// leafNotAfter returns the leaf certificate's expiry, guarding against an empty
// peer-cert slice (never expected after a successful handshake, but a panic in a
// collector would abort the whole ingest tick).
func leafNotAfter(c *tls.Conn) (time.Time, bool) {
	certs := c.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return time.Time{}, false
	}
	return certs[0].NotAfter, true
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
