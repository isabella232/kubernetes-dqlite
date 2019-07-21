package driver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/freeekanayaka/kvsql/db"
	"github.com/freeekanayaka/kvsql/transport"
)

func (d *Driver) List(ctx context.Context, revision, limit int64, rangeKey, startKey string) ([]*db.KeyValue, int64, error) {
	db := d.server.DB()
	return db.List(ctx, revision, limit, rangeKey, startKey)
}

func (d *Driver) Get(ctx context.Context, key string) (*db.KeyValue, error) {
	db := d.server.DB()
	return db.Get(ctx, key)
}

func (d *Driver) Create(ctx context.Context, key string, value []byte, ttl int64) (*db.KeyValue, *db.KeyValue, error) {
	db := d.server.DB()
	kv, err := db.Create(ctx, key, value, ttl)
	if err != nil {
		return nil, nil, err
	}
	if err := d.postWatchChange(ctx, kv); err != nil {
		return nil, nil, err
	}

	if kv.Version == 1 {
		return nil, kv, nil
	}

	oldKv := *kv
	oldKv.Revision = oldKv.OldRevision
	oldKv.Value = oldKv.OldValue
	return &oldKv, kv, nil
}

func (d *Driver) Update(ctx context.Context, key string, value []byte, revision, ttl int64) (*db.KeyValue, *db.KeyValue, error) {
	db := d.server.DB()
	kv, err := db.Mod(ctx, false, key, value, revision, ttl)
	if err != nil {
		return nil, nil, err
	}
	if err := d.postWatchChange(ctx, kv); err != nil {
		return nil, nil, err
	}

	if kv.Version == 1 {
		return nil, kv, nil
	}

	oldKv := *kv
	oldKv.Revision = oldKv.OldRevision
	oldKv.Value = oldKv.OldValue
	return &oldKv, kv, nil
}

func (d *Driver) Delete(ctx context.Context, key string, revision int64) ([]*db.KeyValue, error) {
	if strings.HasSuffix(key, "%") {
		panic("can not delete list revision")
	}
	db := d.server.DB()
	kv, err := db.Mod(ctx, true, key, []byte{}, revision, 0)
	if err != nil {
		return nil, err
	}
	if err := d.postWatchChange(ctx, kv); err != nil {
		return nil, err
	}
	return nil, err
}

func (d *Driver) postWatchChange(ctx context.Context, kv *db.KeyValue) error {
	addr, err := d.server.Leader(ctx)
	if err != nil {
		return err
	}
	if addr == d.server.Address() {
		// Shortcut if we are the leader.
		d.server.Notify(kv)
		return nil
	}

	conn, ok := d.notify[addr]
	if !ok {
		var err error
		request := &http.Request{
			Method:     "POST",
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     make(http.Header),
			Host:       addr,
		}
		path := fmt.Sprintf("http://%s/watch", addr)

		request.URL, err = url.Parse(path)
		if err != nil {
			return err
		}

		request.Header.Set("Upgrade", "watch")
		request = request.WithContext(ctx)

		conn, err = transport.Dial(ctx, d.server.Cert(), addr)
		if err != nil {
			return err
		}
		if err := request.Write(conn); err != nil {
			return err
		}

		response, err := http.ReadResponse(bufio.NewReader(conn), request)
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusSwitchingProtocols {
			err = fmt.Errorf("Dialing failed: expected status code 101 got %d", response.StatusCode)
			return err
		}
		if response.Header.Get("Upgrade") != "watch" {
			err = fmt.Errorf("Missing or unexpected Upgrade header in response")
			return err
		}

		d.notify[addr] = conn
	}

	b := new(bytes.Buffer)
	json.NewEncoder(b).Encode(*kv)
	data := b.Bytes()

	n, err := conn.Write(data)
	if err != nil || n != len(data) {
		conn.Close()
		d.notify[addr] = nil
		return err
	}

	return nil
}
