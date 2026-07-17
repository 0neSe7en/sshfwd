package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/0neSe7en/sshfwd/internal/resolver"
)

const defaultLocalHost = "127.0.0.1"

type Config struct {
	Resolvers   map[string]resolver.Config         `toml:"resolvers"`
	ForwardSets map[string]map[string]LocalAddress `toml:"forward_sets"`
	Hosts       map[string]HostConfig              `toml:"hosts"`
}

type HostConfig struct {
	Resolver    string                  `toml:"resolver"`
	ForwardSets []string                `toml:"forward_sets"`
	ForwardMap  map[string]LocalAddress `toml:"forwards"`
	Forwards    []ForwardConfig         `toml:"-"`
}

type ForwardConfig struct {
	Service string
	Local   LocalAddress
}

type LocalAddress struct {
	Host string
	Port int
}

func (a *LocalAddress) UnmarshalText(text []byte) error {
	value := string(text)
	if port, err := strconv.Atoi(value); err == nil {
		return a.set(defaultLocalHost, port)
	}

	host, portText, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return fmt.Errorf("local address must be a port or host:port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return fmt.Errorf("invalid local port %q", portText)
	}
	return a.set(host, port)
}

func (a *LocalAddress) set(host string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid local port %d", port)
	}
	a.Host = host
	a.Port = port
	return nil
}

func (a LocalAddress) String() string {
	return net.JoinHostPort(a.Host, strconv.Itoa(a.Port))
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "sshfwd.toml"), nil
}

func loadConfig(path string) (Config, error) {
	var config Config
	metadata, err := toml.DecodeFile(path, &config)
	if err != nil {
		return Config{}, err
	}

	if keys := metadata.Undecoded(); len(keys) > 0 {
		names := make([]string, len(keys))
		for i, key := range keys {
			names[i] = key.String()
		}
		return Config{}, fmt.Errorf("unknown configuration keys: %s", strings.Join(names, ", "))
	}

	if err := validateConfig(&config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func validateConfig(config *Config) error {
	if len(config.Hosts) == 0 {
		return fmt.Errorf("no hosts configured")
	}

	for name, host := range config.Hosts {
		if name == "" {
			return fmt.Errorf("host name is empty")
		}
		if host.Resolver == "" {
			return fmt.Errorf("host %q: resolver is empty", name)
		}
		if _, ok := config.Resolvers[host.Resolver]; !ok {
			return fmt.Errorf("host %q: resolver %q does not exist", name, host.Resolver)
		}
		forwards, err := hostForwards(config, name, host)
		if err != nil {
			return err
		}
		host.Forwards = forwards
		config.Hosts[name] = host
	}
	return nil
}

func hostForwards(config *Config, hostName string, host HostConfig) ([]ForwardConfig, error) {
	forwardMap := make(map[string]LocalAddress)
	for _, setName := range host.ForwardSets {
		set, ok := config.ForwardSets[setName]
		if !ok {
			return nil, fmt.Errorf("host %q: forward set %q does not exist", hostName, setName)
		}
		for service, local := range set {
			forwardMap[service] = local
		}
	}
	for service, local := range host.ForwardMap {
		forwardMap[service] = local
	}
	if len(forwardMap) == 0 {
		return nil, fmt.Errorf("host %q: no forwards configured", hostName)
	}

	services := make([]string, 0, len(forwardMap))
	for service := range forwardMap {
		if service == "" {
			return nil, fmt.Errorf("host %q: service name is empty", hostName)
		}
		services = append(services, service)
	}
	sort.Strings(services)

	forwards := make([]ForwardConfig, 0, len(services))
	for _, service := range services {
		forwards = append(forwards, ForwardConfig{Service: service, Local: forwardMap[service]})
	}
	return forwards, nil
}
