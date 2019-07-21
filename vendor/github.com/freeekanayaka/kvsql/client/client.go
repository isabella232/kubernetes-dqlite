package client

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/canonical/go-dqlite/client"
	"github.com/freeekanayaka/kvsql/transport"
	"github.com/pkg/errors"
)

type Client struct {
	server string
	cert   *transport.Cert
}

func New(server string, cert *transport.Cert) *Client {
	return &Client{
		server: server,
		cert:   cert,
	}
}

type Server = client.NodeInfo

func (c *Client) Servers(ctx context.Context) ([]Server, error) {
	url := fmt.Sprintf("http://%s/cluster", c.server)
	client := transport.HTTP(c.cert)

	response, err := client.Get(url)
	if err != nil {
		return nil, errors.Wrap(err, "HTTP request")
	}
	defer response.Body.Close()

	decoder := json.NewDecoder(response.Body)

	servers := []Server{}
	if err := decoder.Decode(&servers); err != nil {
		return nil, errors.Wrap(err, "decode servers")
	}

	return servers, nil
}
