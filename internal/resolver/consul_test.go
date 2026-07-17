package resolver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConsulResponse(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    Endpoint
		wantErr string
	}{
		{
			name: "service address",
			data: `[{
				"Node": "node-1",
				"Address": "10.0.0.1",
				"ServiceAddress": "10.0.0.2",
				"ServicePort": 3030
			}]`,
			want: Endpoint{Address: "10.0.0.2", Port: 3030, Node: "node-1"},
		},
		{
			name: "node address",
			data: `[{"Address":"10.0.0.1","ServicePort":3030}]`,
			want: Endpoint{Address: "10.0.0.1", Port: 3030},
		},
		{
			name:    "empty result",
			data:    `[]`,
			wantErr: "no instances registered",
		},
		{
			name:    "missing address",
			data:    `[{"ServicePort":3030}]`,
			wantErr: "address is empty",
		},
		{
			name:    "missing port",
			data:    `[{"ServiceAddress":"10.0.0.2"}]`,
			wantErr: "invalid port 0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseConsulResponse([]byte(test.data))
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("endpoint = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestConsulUsesSSH(t *testing.T) {
	argsPath := filepath.Join(t.TempDir(), "args")
	sshPath := filepath.Join(t.TempDir(), "ssh")
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + argsPath + `"
printf '%s' '[{"Node":"node-1","ServiceAddress":"10.0.0.2","ServicePort":3030}]'
`
	if err := os.WriteFile(sshPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	resolver := consulResolver{sshBinary: sshPath, address: "127.0.0.1:8500"}
	endpoint, err := resolver.Resolve(context.Background(), "dc1-prod", "nomad")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Address != "10.0.0.2" || endpoint.Port != 3030 {
		t.Fatalf("endpoint = %#v", endpoint)
	}

	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(args)
	for _, want := range []string{
		"ClearAllForwardings=yes",
		"dc1-prod",
		"curl -fsS -- 'http://127.0.0.1:8500/v1/catalog/service/nomad'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ssh args %q do not contain %q", got, want)
		}
	}
}
