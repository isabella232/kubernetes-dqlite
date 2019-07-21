package config

import (
	"path/filepath"

	"github.com/canonical/go-dqlite/client"
	"github.com/pkg/errors"
)

// LoadNodeStore open the servers.sql SQLite database containing the addresses
// of the servers in the cluster.
func loadNodeStore(dir string) (client.NodeStore, error) {
	store, err := client.DefaultNodeStore(filepath.Join(dir, "servers.sql"))
	if err != nil {
		return nil, errors.Wrap(err, "open node store")
	}
	return store, nil
}
