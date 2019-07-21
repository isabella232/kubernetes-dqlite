package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"time"

	"github.com/pkg/errors"
)

// Dial establishes a secure connection with the given server.
func Dial(ctx context.Context, cert *Cert, addr string) (net.Conn, error) {
	// TODO: honor the given context's deadline
	// deadline, _ := ctx.Deadline()
	// timeout := time.Until(deadline)
	timeout := 5 * time.Second
	dialer := &net.Dialer{Timeout: timeout}

	cfg := &tls.Config{
		MinVersion:               tls.VersionTLS12,
		CipherSuites:             tlsCipherSuites,
		PreferServerCipherSuites: true,
		RootCAs:                  cert.Pool,
		Certificates:             []tls.Certificate{cert.KeyPair},
	}

	crt, err := x509.ParseCertificate(cert.KeyPair.Certificate[0])
	if err != nil {
		return nil, errors.Wrap(err, "parse keypair certificate")
	}
	if len(crt.DNSNames) == 0 {
		return nil, fmt.Errorf("certificate has no DNS extension")
	}
	cfg.ServerName = crt.DNSNames[0]

	conn, err := tls.DialWithDialer(dialer, "tcp", addr, cfg)
	if err != nil {
		return nil, errors.Wrap(err, "connect to server")
	}

	return conn, nil
}
