package transport

import (
	"context"
	"net"
	"net/http"
	"time"
)

// HTTP returns an http client configured to perform client TLS authentication.
func HTTP(cert *Cert) *http.Client {
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network != "tcp" {
			panic("only TCP is supported")
		}
		return Dial(ctx, cert, addr)
	}
	transport := &http.Transport{
		DialContext:           dial,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	client := &http.Client{
		Transport: transport,
	}
	return client
}
