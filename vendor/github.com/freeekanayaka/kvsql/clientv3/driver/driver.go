package driver

import (
	"context"
	"net"

	"github.com/freeekanayaka/kvsql/server"
)

type Driver struct {
	server *server.Server
	notify map[string]net.Conn // Map leader addresses to open notification conn
}

func New(server *server.Server) *Driver {
	driver := &Driver{
		server: server,
		notify: map[string]net.Conn{},
	}
	return driver
}

func (d *Driver) WaitStopped() {
	d.server.Close(context.Background())
}
