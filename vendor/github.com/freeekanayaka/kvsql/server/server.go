package server

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/canonical/go-dqlite"
	"github.com/canonical/go-dqlite/client"
	"github.com/canonical/go-dqlite/driver"
	"github.com/freeekanayaka/kvsql/db"
	"github.com/freeekanayaka/kvsql/pkg/broadcast"
	"github.com/freeekanayaka/kvsql/server/api"
	"github.com/freeekanayaka/kvsql/server/config"
	"github.com/freeekanayaka/kvsql/server/membership"
	"github.com/freeekanayaka/kvsql/transport"
	"github.com/pkg/errors"
	kinedriver "github.com/rancher/kine/pkg/drivers/dqlite"
	"github.com/rancher/kine/pkg/endpoint"
	"github.com/sirupsen/logrus"
)

// Server sets up a single dqlite node and serves the cluster management API.
type Server struct {
	dir           string                 // Data directory
	address       string                 // Network address
	cert          *transport.Cert        // TLS configuration
	api           *http.Server           // API server
	node          *dqlite.Node           // Dqlite node
	db            *db.DB                 // Database connection
	membership    *membership.Membership // Cluster membership
	changes       chan *db.KeyValue
	cancelWatcher context.CancelFunc
	cancelUpdater context.CancelFunc
	cancelKine    context.CancelFunc
}

func New(dir string, full bool) (*Server, error) {
	// Check if we're initializing a new node (i.e. there's an init.yaml).
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Possibly initialize our ID, address and initial node store content.
	if cfg.Init != nil {
		if err := initConfig(ctx, cfg); err != nil {
			return nil, err
		}
		if err := cfg.Save(dir); err != nil {
			return nil, err
		}
		if err := os.Remove(filepath.Join(dir, "init.yaml")); err != nil {
			return nil, err
		}
	}

	node, err := newNode(cfg, dir)
	if err != nil {
		return nil, err
	}

	dial := dqliteDialFunc(cfg.Cert)
	membership := membership.New(cfg.Address, node.BindAddress(), cfg.Store, dial)
	mux := api.New(node.BindAddress(), membership)
	apiserver := &http.Server{Handler: mux}

	if err := startAPI(cfg, apiserver); err != nil {
		return nil, err
	}

	var dbObj *db.DB

	if full {
		driver, err := registerDriver(cfg)
		if err != nil {
			return nil, err
		}
		dbObj, err = db.Open(driver, "k8s")
		if err != nil {
			return nil, errors.Wrap(err, "open cluster database")
		}
	}

	// If we are initializing a new server, update the cluster state
	// accordingly.
	if cfg.Init != nil {
		if err := initServer(ctx, cfg, dbObj, membership); err != nil {
			return nil, err
		}
	}

	if full {
		if err := dbObj.Bump(ctx); err != nil {
			return nil, err
		}
	}

	var changes chan *db.KeyValue
	var cancelWatcher context.CancelFunc
	var cancelKine context.CancelFunc

	if full {
		changes = make(chan *db.KeyValue, 1024)

		connectFunc, cancel := globalWatcher(changes)
		broadcaster := &broadcast.Broadcaster{}
		subscribe := func(ctx context.Context) (chan map[string]interface{}, error) {
			return broadcaster.Subscribe(ctx, connectFunc)
		}
		mux.HandleFunc("/watch", api.WatchHandleFunc(dbObj, changes, subscribe))
		cancelWatcher = cancel
	} else {
		kinedriver.Dialer = dial
		kinedriver.Logger = dqliteLogFunc
		socket := filepath.Join(dir, "kine.sock")
		peers := filepath.Join(dir, "servers.sql")
		// logrus.SetLevel(logrus.DebugLevel)
		config := endpoint.Config{
			Listener: fmt.Sprintf("unix://%s", socket),
			Endpoint: fmt.Sprintf("dqlite://k8s?peer-file=%s", peers),
		}
		ctx, cancel := context.WithCancel(context.Background())
		_, err := endpoint.Listen(ctx, config)
		if err != nil {
			return nil, errors.Wrap(err, "kine")
		}
		cancelKine = cancel
	}

	cancelUpdater := startUpdater(dbObj, cfg.Store, membership)

	s := &Server{
		dir:           dir,
		address:       cfg.Address,
		cert:          cfg.Cert,
		api:           apiserver,
		node:          node,
		db:            dbObj,
		membership:    membership,
		changes:       changes,
		cancelWatcher: cancelWatcher,
		cancelUpdater: cancelUpdater,
		cancelKine:    cancelKine,
	}

	return s, nil
}

func (s *Server) Address() string {
	return s.address
}

func (s *Server) Notify(kv *db.KeyValue) {
	s.changes <- kv
}

// Register a new Dqlite driver and return the registration name.
func registerDriver(cfg *config.Config) (string, error) {
	dial := dqliteDialFunc(cfg.Cert)
	timeout := time.Minute
	driver, err := driver.New(
		cfg.Store, driver.WithDialFunc(dial),
		driver.WithConnectionTimeout(timeout),
		driver.WithContextTimeout(timeout),
	)
	if err != nil {
		return "", errors.Wrap(err, "create dqlite driver")
	}

	// Create a unique name to pass to sql.Register.
	driverIndex++
	name := fmt.Sprintf("dqlite-%d", driverIndex)

	sql.Register(name, driver)

	return name, nil
}

var driverIndex = 0

// Initializes the configuration according to the content of the init.yaml
// file, possibly obtaining a new node ID.
func initConfig(ctx context.Context, cfg *config.Config) error {
	servers := []client.NodeInfo{}

	if len(cfg.Init.Cluster) == 0 {
		servers = append(servers, client.NodeInfo{
			Address: cfg.Init.Address,
		})
	} else {
		for _, address := range cfg.Init.Cluster {
			servers = append(servers, client.NodeInfo{
				Address: address,
			})
		}
	}

	if err := cfg.Store.Set(context.Background(), servers); err != nil {
		return errors.Wrap(err, "initialize node store")
	}

	if len(cfg.Init.Cluster) == 0 {
		cfg.ID = dqlite.BootstrapID
	} else {
		// Generate a new ID.
		cfg.ID = dqlite.GenerateID(cfg.Init.Address)
	}

	cfg.Address = cfg.Init.Address

	return nil
}

// Create a new dqlite node.
func newNode(cfg *config.Config, dir string) (*dqlite.Node, error) {
	// Wrap the regular dial function which one that also proxies the TLS
	// connection, since the raft connect function only supports Unix and
	// TCP connections.
	dial := func(ctx context.Context, addr string) (net.Conn, error) {
		dial := dqliteDialFunc(cfg.Cert)
		tlsConn, err := dial(ctx, addr)
		if err != nil {
			return nil, err
		}
		goUnix, cUnix, err := transport.Socketpair()
		if err != nil {
			return nil, errors.Wrap(err, "create pair of Unix sockets")
		}

		transport.Proxy(tlsConn, goUnix)

		return cUnix, nil
	}

	// Possibly update the address
	if cfg.Update != nil {
		cfg.Address = cfg.Update.Address
	}

	node, err := dqlite.New(cfg.ID, cfg.Address, dir, dqlite.WithBindAddress("@"), dqlite.WithDialFunc(dial))
	if err != nil {
		return nil, errors.Wrap(err, "create dqlite node")
	}

	if cfg.Update != nil {
		nodes := []dqlite.NodeInfo{{ID: cfg.ID, Address: cfg.Address}}
		if err := node.Recover(nodes); err != nil {
			return nil, errors.Wrap(err, "update configuration")
		}
		if err := cfg.Save(dir); err != nil {
			return nil, err
		}
		if err := cfg.Store.Set(context.Background(), nodes); err != nil {
			return nil, errors.Wrap(err, "update node store")
		}
		if err := os.Remove(filepath.Join(dir, "update.yaml")); err != nil {
			return nil, errors.Wrap(err, "remove update.yaml")
		}
	}

	if err := node.Start(); err != nil {
		return nil, errors.Wrap(err, "start dqlite node")
	}

	return node, nil
}

// Create and start the server.
func startAPI(cfg *config.Config, api *http.Server) error {
	listener, err := transport.Listen(cfg.Address, cfg.Cert)
	if err != nil {
		return err
	}
	go func() {
		if err := api.Serve(listener); err != http.ErrServerClosed {
			panic(err)
		}
	}()
	return nil
}

func initServer(ctx context.Context, cfg *config.Config, db *db.DB, membership *membership.Membership) error {
	if len(cfg.Init.Cluster) == 0 {
		if db != nil {
			if err := db.CreateSchema(ctx); err != nil {
				return err
			}
		}
	} else {
		if err := membership.Add(cfg.ID, cfg.Address); err != nil {
			return err
		}
	}
	return nil
}

// Returns a dqlite dial function that will establish the connection
// using the target server's /dqlite HTTP endpoint.
func dqliteDialFunc(cert *transport.Cert) client.DialFunc {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		request := &http.Request{
			Method:     "POST",
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     make(http.Header),
			Host:       addr,
		}
		path := fmt.Sprintf("https://%s/dqlite", addr)

		var err error
		request.URL, err = url.Parse(path)
		if err != nil {
			return nil, err
		}

		request.Header.Set("Upgrade", "dqlite")
		request = request.WithContext(ctx)

		conn, err := transport.Dial(ctx, cert, addr)
		if err != nil {
			return nil, errors.Wrap(err, "connect to HTTP endpoint")
		}

		err = request.Write(conn)
		if err != nil {
			return nil, errors.Wrap(err, "HTTP request failed")
		}

		response, err := http.ReadResponse(bufio.NewReader(conn), request)
		if err != nil {
			return nil, errors.Wrap(err, "read response")
		}
		if response.StatusCode != http.StatusSwitchingProtocols {
			return nil, fmt.Errorf("expected status code 101 got %d", response.StatusCode)
		}
		if response.Header.Get("Upgrade") != "dqlite" {
			return nil, fmt.Errorf("missing or unexpected Upgrade header in response")
		}

		return conn, nil
	}
}

func globalWatcher(changes chan *db.KeyValue) (broadcast.ConnectFunc, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	f := func() (chan map[string]interface{}, error) {
		result := make(chan map[string]interface{}, 100)
		go func() {
			defer close(result)
			for {
				select {
				case <-ctx.Done():
					return
				case e := <-changes:
					result <- map[string]interface{}{
						"data": e,
					}
				}
			}
		}()

		return result, nil
	}
	return f, cancel
}

func startUpdater(db *db.DB, store client.NodeStore, membership *membership.Membership) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				servers, err := membership.List()
				if err != nil {
					fmt.Printf("Failed to get servers: %v\n", err)
					continue

				}
				if err := store.Set(ctx, servers); err != nil {
					fmt.Printf("Failed to update servers: %v\n", err)
				}
			}
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
				membership.Adjust()
			}
		}
	}()
	if db != nil {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Minute):
					if err := db.Cleanup(ctx); err != nil {
						fmt.Println("Failed to purge expired TTL entries")
					}
				}
			}
		}()
	}
	return cancel
}

func (s *Server) Leader(ctx context.Context) (string, error) {
	return s.membership.Leader()
}

func (s *Server) DB() *db.DB {
	return s.db
}

func (s *Server) Cert() *transport.Cert {
	return s.cert
}

func (s *Server) Close(ctx context.Context) error {
	if s.cancelUpdater != nil {
		s.cancelUpdater()
	}
	if s.cancelWatcher != nil {
		s.cancelWatcher()
	}
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			return err
		}
	}
	s.membership.Shutdown()
	if err := s.api.Shutdown(ctx); err != nil {
		return errors.Wrap(err, "shutdown API server")
	}
	if s.cancelKine != nil {
		s.cancelKine()
	}
	if err := s.node.Close(); err != nil {
		return errors.Wrap(err, "stop dqlite node")
	}
	return nil
}

func dqliteLogFunc(l client.LogLevel, format string, a ...interface{}) {
	msg := fmt.Sprintf("dqlite: "+format, a...)
	switch l {
	case client.LogDebug:
		logrus.Debug(msg)
	case client.LogInfo:
		logrus.Info(msg)
	case client.LogWarn:
		logrus.Warn(msg)
	case client.LogError:
		logrus.Error(msg)
	}
}
