package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

type consulResolver struct {
	sshBinary string
	address   string
}

type consulEntry struct {
	Node           string `json:"Node"`
	Address        string `json:"Address"`
	ServiceAddress string `json:"ServiceAddress"`
	ServicePort    int    `json:"ServicePort"`
}

func (r consulResolver) Resolve(ctx context.Context, host, service string) (Endpoint, error) {
	queryURL := "http://" + r.address + "/v1/catalog/service/" + url.PathEscape(service)
	remoteCommand := "curl -fsS -- " + shellQuote(queryURL)
	cmd := exec.CommandContext(ctx, r.sshBinary,
		"-T",
		"-o", "ClearAllForwardings=yes",
		"--", host,
		remoteCommand,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return Endpoint{}, fmt.Errorf("query Consul through %s: %w: %s", host, err, message)
		}
		return Endpoint{}, fmt.Errorf("query Consul through %s: %w", host, err)
	}

	endpoint, err := parseConsulResponse(output)
	if err != nil {
		return Endpoint{}, fmt.Errorf("resolve service %q: %w", service, err)
	}
	return endpoint, nil
}

func parseConsulResponse(data []byte) (Endpoint, error) {
	var entries []consulEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return Endpoint{}, err
	}
	if len(entries) == 0 {
		return Endpoint{}, fmt.Errorf("no instances registered")
	}

	entry := entries[0]
	address := entry.ServiceAddress
	if address == "" {
		address = entry.Address
	}
	if address == "" {
		return Endpoint{}, fmt.Errorf("address is empty")
	}
	if entry.ServicePort < 1 || entry.ServicePort > 65535 {
		return Endpoint{}, fmt.Errorf("invalid port %d", entry.ServicePort)
	}

	return Endpoint{
		Address: address,
		Port:    entry.ServicePort,
		Node:    entry.Node,
	}, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
