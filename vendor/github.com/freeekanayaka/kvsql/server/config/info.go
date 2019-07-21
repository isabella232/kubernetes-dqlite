package config

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/canonical/go-dqlite/client"
	"github.com/ghodss/yaml"
	"github.com/pkg/errors"
)

func saveInfo(id uint64, address string, dir string) error {
	info := client.NodeInfo{
		ID:      id,
		Address: address,
	}
	data, err := yaml.Marshal(info)
	if err != nil {
		return errors.Wrap(err, "encode server info")
	}

	path := filepath.Join(dir, "info.yaml")
	if err := ioutil.WriteFile(path, data, 0644); err != nil {
		return errors.Wrap(err, "write server info")
	}

	return nil
}

func loadInfo(dir string) (uint64, string, error) {
	info := client.NodeInfo{}
	path := filepath.Join(dir, "info.yaml")

	data, err := ioutil.ReadFile(path)
	if err != nil {
		return 0, "", errors.Wrap(err, "read info.yaml")
	}

	if err := yaml.Unmarshal(data, &info); err != nil {
		return 0, "", errors.Wrap(err, "parse info.yaml")
	}

	if info.ID == 0 {
		return 0, "", fmt.Errorf("server ID is zero")
	}
	if info.Address == "" {
		return 0, "", fmt.Errorf("server address is empty")
	}

	return info.ID, info.Address, nil
}
