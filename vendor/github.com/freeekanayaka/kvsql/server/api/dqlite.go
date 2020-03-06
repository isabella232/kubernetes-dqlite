package api

import (
	"net"
	"net/http"

	"github.com/freeekanayaka/kvsql/transport"
	"github.com/pkg/errors"
)

func dqliteHandleFunc(localNodeAddress string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "dqlite" {
			http.Error(w, "Missing or invalid upgrade header", http.StatusBadRequest)
			return
		}

		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "Webserver doesn't support hijacking", http.StatusInternalServerError)
			return
		}

		tlsConn, _, err := hijacker.Hijack()
		if err != nil {
			message := errors.Wrap(err, "Failed to hijack connection").Error()
			http.Error(w, message, http.StatusInternalServerError)
			return
		}

		// Write the status line and upgrade header by hand since w.WriteHeader()
		// would fail after Hijack()
		data := []byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: dqlite\r\n\r\n")
		if n, err := tlsConn.Write(data); err != nil || n != len(data) {
			tlsConn.Close()
			return
		}

		unixConn, err := net.Dial("unix", localNodeAddress)
		if err != nil {
			panic("dqlite node is not listening to the given Unix socket")
		}

		transport.Proxy(tlsConn, unixConn)
	}
}
