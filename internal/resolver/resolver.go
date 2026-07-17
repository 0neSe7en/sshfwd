package resolver

import (
	"context"
	"fmt"
)

type Config struct {
	Type    string `toml:"type"`
	Address string `toml:"address"`
}

type Endpoint struct {
	Address string
	Port    int
	Node    string
}

type Resolver interface {
	Resolve(context.Context, string, string) (Endpoint, error)
}

func New(config Config, sshBinary string) (Resolver, error) {
	switch config.Type {
	case "consul":
		if config.Address == "" {
			return nil, fmt.Errorf("consul address is empty")
		}
		return consulResolver{sshBinary: sshBinary, address: config.Address}, nil
	default:
		return nil, fmt.Errorf("unsupported resolver %q", config.Type)
	}
}
