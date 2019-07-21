// +build cgo

package factory

import (
	"context"
	"fmt"

	"github.com/freeekanayaka/kvsql/clientv3"
	etcd3 "github.com/freeekanayaka/kvsql/storage"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
	"k8s.io/apiserver/pkg/storage/value"
)

func NewKVSQLHealthCheck(c storagebackend.Config) (func() error, error) {
	// TODO: implement a reasonable health check for dqlite
	return func() error { return nil }, nil
}

func newETCD3Client(c storagebackend.Config) (*clientv3.Client, error) {
	if c.Dir == "" {
		return nil, fmt.Errorf("no storage directory provided")
	}

	cfg := clientv3.Config{
		Dir: c.Dir,
	}

	client, err := clientv3.New(cfg)
	return client, err
}

func NewKVSQLStorage(c storagebackend.Config) (storage.Interface, func(), error) {
	client, err := newETCD3Client(c)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	etcd3.StartCompactor(ctx, client, c.CompactionInterval)
	destroyFunc := func() {
		cancel()
		client.Close()
	}
	transformer := c.Transformer
	if transformer == nil {
		transformer = value.IdentityTransformer
	}

	return etcd3.New(client, c.Codec, c.Prefix, transformer, c.Paging), destroyFunc, nil
}

// XXX: find a better way to shutdown the storage.
func Close() {
	clientv3.Shutdown()
}
