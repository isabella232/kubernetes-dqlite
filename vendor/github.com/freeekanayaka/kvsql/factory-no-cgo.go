// +build !cgo

package factory

import (
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/storagebackend"
)

func NewKVSQLHealthCheck(c storagebackend.Config) (func() error, error) {
	panic("unimplemented (CGO is disabled)")
}

func NewKVSQLStorage(c storagebackend.Config) (storage.Interface, func(), error) {
	panic("unimplemented (CGO is disabled)")
}

func Close() {
	panic("unimplemented (CGO is disabled)")
}
