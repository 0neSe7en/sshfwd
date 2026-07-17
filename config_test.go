package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigWithInlineForwards(t *testing.T) {
	path := writeTestConfig(t, `
[resolvers.consul]
type = "consul"
address = "127.0.0.1:8500"

[hosts.dc1-prod]
resolver = "consul"

[hosts.dc1-prod.forwards]
nomad = "3000"
example-api = "0.0.0.0:18080"
`)

	config, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	forwards := config.Hosts["dc1-prod"].Forwards
	if len(forwards) != 2 {
		t.Fatalf("forwards = %#v", forwards)
	}
	if forwards[0].Service != "example-api" || forwards[0].Local != (LocalAddress{Host: "0.0.0.0", Port: 18080}) {
		t.Fatalf("first forward = %#v", forwards[0])
	}
	if forwards[1].Service != "nomad" || forwards[1].Local != (LocalAddress{Host: defaultLocalHost, Port: 3000}) {
		t.Fatalf("second forward = %#v", forwards[1])
	}
}

func TestInlineForwardsOverrideForwardSets(t *testing.T) {
	path := writeTestConfig(t, `
[resolvers.consul]
type = "consul"
address = "127.0.0.1:8500"

[forward_sets.base]
api = "8080"

[forward_sets.environment]
api = "18080"
database = "5432"

[hosts.demo]
resolver = "consul"
forward_sets = ["base", "environment"]

[hosts.demo.forwards]
api = "28080"
admin = "9000"
`)

	config, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	forwards := config.Hosts["demo"].Forwards
	if len(forwards) != 3 {
		t.Fatalf("forwards = %#v", forwards)
	}
	if forwards[1].Service != "api" || forwards[1].Local.Port != 28080 {
		t.Fatalf("api forward = %#v", forwards[1])
	}
}

func TestLoadConfigRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "unknown key",
			content: `
[hosts.demo]
unknown = true
`,
			want: "unknown configuration keys",
		},
		{
			name: "missing resolver",
			content: `
[hosts.demo]
resolver = "missing"

[hosts.demo.forwards]
api = "8080"
`,
			want: "resolver \"missing\" does not exist",
		},
		{
			name: "invalid local address",
			content: `
[resolvers.consul]
type = "consul"
address = "127.0.0.1:8500"

[hosts.demo]
resolver = "consul"

[hosts.demo.forwards]
api = "localhost"
`,
			want: "local address must be a port or host:port",
		},
		{
			name: "missing forward set",
			content: `
[resolvers.consul]
type = "consul"
address = "127.0.0.1:8500"

[hosts.demo]
resolver = "consul"
forward_sets = ["missing"]
`,
			want: "forward set \"missing\" does not exist",
		},
		{
			name: "no forwards",
			content: `
[resolvers.consul]
type = "consul"
address = "127.0.0.1:8500"

[hosts.demo]
resolver = "consul"
`,
			want: "no forwards configured",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadConfig(writeTestConfig(t, test.content))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sshfwd.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
