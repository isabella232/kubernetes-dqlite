package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	model "github.com/freeekanayaka/kvsql/db"
	"github.com/pkg/errors"
)

type Event struct {
	KV    *model.KeyValue
	Err   error
	Start bool
}

type SubcribeFunc func(context.Context) (chan map[string]interface{}, error)

func WatchHandleFunc(db *model.DB, changes chan *model.KeyValue, subscribe SubcribeFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Receive change notifications.
		if r.Method == "POST" {
			if r.Header.Get("Upgrade") != "watch" {
				http.Error(w, "Missing or invalid upgrade header", http.StatusBadRequest)
				return
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "Webserver doesn't support hijacking", http.StatusInternalServerError)
				return
			}

			conn, _, err := hijacker.Hijack()
			if err != nil {
				message := errors.Wrap(err, "Failed to hijack connection").Error()
				http.Error(w, message, http.StatusInternalServerError)
				return
			}

			// Write the status line and upgrade header by hand since w.WriteHeader()
			// would fail after Hijack()
			data := []byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: watch\r\n\r\n")
			if n, err := conn.Write(data); err != nil || n != len(data) {
				conn.Close()
				return
			}
			defer conn.Close()

			dec := json.NewDecoder(conn)
			for {
				kv := model.KeyValue{}
				if err := dec.Decode(&kv); err != nil {
					break
				}
				changes <- &kv
			}
		}

		// Broadcast change notifications.
		if r.Method == "GET" {
			if r.Header.Get("Upgrade") != "watch" {
				http.Error(w, "Missing or invalid upgrade header", http.StatusBadRequest)
				return
			}

			key := r.Header.Get("X-Watch-Key")
			if key == "" {
				http.Error(w, "Missing key header", http.StatusBadRequest)
				return
			}

			rev := r.Header.Get("X-Watch-Rev")
			if rev == "" {
				http.Error(w, "Missing rev header", http.StatusBadRequest)
				return
			}
			revision, err := strconv.Atoi(rev)
			if err != nil {
				http.Error(w, "Bad revision", http.StatusBadRequest)
				return
			}

			hijacker, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "Webserver doesn't support hijacking", http.StatusInternalServerError)
				return
			}

			conn, _, err := hijacker.Hijack()
			if err != nil {
				message := errors.Wrap(err, "Failed to hijack connection").Error()
				http.Error(w, message, http.StatusInternalServerError)
				return
			}

			// Write the status line and upgrade header by hand since w.WriteHeader()
			// would fail after Hijack()
			data := []byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: watch\r\n\r\n")
			if n, err := conn.Write(data); err != nil || n != len(data) {
				conn.Close()
				return
			}

			prefix := strings.HasSuffix(key, "%")

			ctx, parentCancel := context.WithCancel(context.Background())

			defer func() {
				conn.Close()
				parentCancel()
			}()

			events, err := subscribe(ctx)
			if err != nil {
				panic(err)
			}

			writer := bufio.NewWriter(conn)

			if err := sendEvent(writer, &Event{Start: true}); err != nil {
				return
			}

			if revision > 0 {
				keys, err := db.Replay(ctx, key, int64(revision))
				if err != nil {
					return
				}

				for _, k := range keys {
					if err := sendEvent(writer, &Event{KV: k}); err != nil {
						return
					}
				}
			}

			for e := range events {
				k, ok := e["data"].(*model.KeyValue)
				if ok && matchesKey(prefix, key, k) {
					if err := sendEvent(writer, &Event{KV: k}); err != nil {
						return
					}
				}
			}

		}
	}
}

func matchesKey(prefix bool, key string, kv *model.KeyValue) bool {
	if kv == nil {
		return false
	}
	if prefix {
		return strings.HasPrefix(kv.Key, key[:len(key)-1])
	}
	return kv.Key == key
}

func sendEvent(writer *bufio.Writer, e *Event) error {
	b := new(bytes.Buffer)
	json.NewEncoder(b).Encode(e)

	if _, err := writer.Write(b.Bytes()); err != nil {
		return err
	}

	if err := writer.Flush(); err != nil {
		return err
	}

	return nil
}
