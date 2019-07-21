package driver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/freeekanayaka/kvsql/db"
	"github.com/freeekanayaka/kvsql/transport"
	"github.com/pkg/errors"
)

type Event struct {
	KV    *db.KeyValue
	Err   error
	Start bool
}

func (d *Driver) Watch(ctx context.Context, key string, revision int64) <-chan Event {
	ctx, parentCancel := context.WithCancel(ctx)

	watchChan := make(chan Event)
	go func() (returnErr error) {
		defer func() {
			sendErrorAndClose(watchChan, returnErr)
			parentCancel()
		}()
		ctx := context.Background()
		addr, err := d.server.Leader(ctx)
		if err != nil {
			returnErr = errors.Wrap(err, "get leader")
			return
		}

		request := &http.Request{
			Method:     "GET",
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     make(http.Header),
			Host:       addr,
		}
		path := fmt.Sprintf("https://%s/watch", addr)

		request.URL, returnErr = url.Parse(path)
		if returnErr != nil {
			return
		}

		request.Header.Set("Upgrade", "watch")
		request.Header.Set("X-Watch-Key", key)
		request.Header.Set("X-Watch-Rev", fmt.Sprintf("%d", revision))
		request = request.WithContext(ctx)

		conn, err := transport.Dial(ctx, d.server.Cert(), addr)
		if err != nil {
			returnErr = err
			return
		}
		defer conn.Close()

		if returnErr = request.Write(conn); returnErr != nil {
			return
		}

		response, err := http.ReadResponse(bufio.NewReader(conn), request)
		if err != nil {
			returnErr = err
			return
		}
		if response.StatusCode != http.StatusSwitchingProtocols {
			returnErr = fmt.Errorf("Dialing failed: expected status code 101 got %d", response.StatusCode)
			return
		}
		if response.Header.Get("Upgrade") != "watch" {
			returnErr = fmt.Errorf("Missing or unexpected Upgrade header in response")
			return
		}

		reader := bufio.NewReader(conn)
		for {
			b, err := reader.ReadBytes('\n')
			if err != nil {
				returnErr = err
				return
			}
			e := Event{}
			if returnErr = json.Unmarshal(b, &e); returnErr != nil {
				return
			}
			watchChan <- e
		}

		return nil
	}()

	return watchChan
}

func start(watchResponses chan Event) {
	watchResponses <- Event{
		Start: true,
	}
}

func sendErrorAndClose(watchResponses chan Event, err error) {
	if err != nil {
		watchResponses <- Event{Err: err}
	}
	close(watchResponses)
}

// Close closes the watcher and cancels all watch requests.
func (d *Driver) Close() error {
	return nil
}
