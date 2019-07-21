package membership

import (
	"context"
	"time"

	"github.com/canonical/go-dqlite/client"
	"github.com/pkg/errors"
)

// Membership manages dqlite cluster membership.
type Membership struct {
	address     string
	bindAddress string
	store       client.NodeStore
	dial        client.DialFunc
}

func New(address, bindAddress string, store client.NodeStore, dial client.DialFunc) *Membership {
	return &Membership{
		address:     address,
		bindAddress: bindAddress,
		store:       store,
		dial:        dial,
	}
}

func (m *Membership) List() ([]client.NodeInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	leader, err := m.getLeader()
	if err != nil {
		return nil, err
	}
	defer leader.Close()
	return leader.Cluster(ctx)
}

func (m *Membership) Add(id uint64, address string) error {
	info := client.NodeInfo{
		ID:      id,
		Address: address,
		Role:    client.Spare,
	}
	leader, err := m.getLeader()
	if err != nil {
		return errors.Wrap(err, "find leader")
	}
	defer leader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := leader.Add(ctx, info); err != nil {
		return errors.Wrap(err, "join cluster")
	}
	return nil
}

func (m *Membership) Leader() (string, error) {
	// FIXME: this is a best-effort optimization to avoid establishing TLS
	// connections if we locally know about the leader, but it won't work
	// for spare nodes.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := client.New(ctx, m.bindAddress)
	if err != nil {
		return "", errors.Wrap(err, "connect to dqlite node")
	}
	defer client.Close()

	info, err := client.Leader(ctx)
	if err != nil {
		return "", errors.Wrap(err, "get leader")
	}
	if info != nil && info.Address != "" {
		return info.Address, nil
	}

	leader, err := m.getLeader()
	if err != nil {
		return "", err
	}
	defer leader.Close()

	info, err = leader.Leader(ctx)
	if err != nil {
		return "", err
	}

	if info == nil {
		return "", nil
	}

	return info.Address, nil
}

func (m *Membership) Adjust() {
	// Check if we are the current leader.
	leader, err := m.getLeader()
	if err != nil {
		return
	}
	defer leader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	info, err := leader.Leader(ctx)
	if err != nil {
		return
	}

	if info == nil || info.Address != m.address {
		return
	}

	servers, err := leader.Cluster(ctx)
	if err != nil {
		return
	}

	// If there's only one server, there's nothing to do.
	if len(servers) == 1 {
		return
	}

	// If there are two servers, we want the second one to be a spare.
	if len(servers) == 2 {
		for _, server := range servers {
			if server.Address == info.Address {
				continue
			}
			if server.Role == client.Voter {
				leader.Assign(ctx, server.ID, client.Spare)
			}
		}
		return
	}

	voters := []client.NodeInfo{}
	spares := []client.NodeInfo{}

	for _, server := range servers {
		switch server.Role {
		case client.Voter:
			voters = append(voters, server)
		case client.Spare:
			spares = append(spares, server)
		}
	}

	if len(voters) < 3 {
		for _, server := range spares {
			if err := leader.Assign(ctx, server.ID, client.Voter); err == nil {
				return
			}
		}
	} else if len(voters) > 3 {
		for _, server := range voters {
			if server.Address == info.Address {
				continue
			}
			if err := leader.Assign(ctx, server.ID, client.Spare); err == nil {
				return
			}
		}
	}
}

// Best effort to shutdown gracefully.
func (m *Membership) Shutdown() {
	leader, err := m.getLeader()
	if err != nil {
		return
	}
	defer leader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// If the node which is shutting down is not a voter, there's nothing
	// to do.
	servers, err := leader.Cluster(ctx)
	if err != nil {
		return
	}
	id := uint64(0)
	for _, server := range servers {
		if server.Address != m.address {
			continue
		}
		if server.Role != client.Voter {
			return
		}
		id = server.ID
		break
	}
	if id == 0 {
		return
	}

	for _, server := range servers {
		if server.Role == client.Voter || server.Address == m.address {
			continue
		}
		if err := leader.Assign(ctx, server.ID, client.Voter); err == nil {
			leader.Assign(ctx, id, client.Spare)
			break
		}
	}

	info, err := leader.Leader(ctx)
	if err != nil {
		return
	}

	if info == nil || info.Address != m.address {
		return
	}

	leader.Transfer(ctx, 0)
}

func (m *Membership) getLeader() (*client.Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return client.FindLeader(ctx, m.store, client.WithDialFunc(m.dial))
}
