package transport

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/pkg/errors"
)

type Cert struct {
	KeyPair tls.Certificate
	Pool    *x509.CertPool
}

// LoadCert loads the cluster TLS certificates from the given directory.
func LoadCert(dir string) (*Cert, error) {
	crt := filepath.Join(dir, "cluster.crt")
	key := filepath.Join(dir, "cluster.key")

	keypair, err := tls.LoadX509KeyPair(crt, key)
	if err != nil {
		return nil, errors.Wrap(err, "load keypair")
	}

	data, err := ioutil.ReadFile(crt)
	if err != nil {
		return nil, errors.Wrap(err, "read certificate")
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("bad certificate")
	}

	return &Cert{KeyPair: keypair, Pool: pool}, nil
}
