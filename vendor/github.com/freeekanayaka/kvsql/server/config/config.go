package config

import (
	"github.com/canonical/go-dqlite/client"
	"github.com/freeekanayaka/kvsql/transport"
)

// Config holds the server configuraton loaded from disk.
type Config struct {
	Cert    *transport.Cert  // TLS configuration
	Init    *Init            // Initialization parameters, for new servers.
	Store   client.NodeStore // Hold members of the dqlite cluster
	ID      uint64           // Server ID
	Address string           // Server address
	Update  *Update          // Configuration updates
}

// Load current the configuration from disk.
func Load(dir string) (*Config, error) {
	// Load the TLS certificates.
	cert, err := transport.LoadCert(dir)
	if err != nil {
		return nil, err
	}

	// Check if we're initializing a new node (i.e. there's an init.yaml).
	init, err := loadInit(dir)
	if err != nil {
		return nil, err
	}

	// Open the node store, effectively creating a new empty one if we're
	// initializing.
	store, err := loadNodeStore(dir)
	if err != nil {
		return nil, err
	}

	id := uint64(0)
	address := ""
	var update *Update
	if init == nil {
		id, address, err = loadInfo(dir)
		if err != nil {
			return nil, err
		}
		update, err = loadUpdate(dir)
		if err != nil {
			return nil, err
		}
	}

	config := &Config{
		Init:    init,
		Store:   store,
		Cert:    cert,
		ID:      id,
		Address: address,
		Update:  update,
	}

	return config, nil
}

// Save the configuration to disk.
func (c *Config) Save(dir string) error {
	if err := saveInfo(c.ID, c.Address, dir); err != nil {
		return err
	}
	return nil
}
