package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/0neSe7en/sshfwd/internal/resolver"
)

func TestParseEffectiveListeners(t *testing.T) {
	input := `hostname example.com
clearallforwardings no
dynamicforward [127.0.0.1]:1080
localforward 3000 [service.internal]:3030
localforward /tmp/local.sock [service.internal]:3030
remoteforward 9000 [remote.internal]:9001
`
	got, err := parseEffectiveListeners(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]string{1080: "dynamicforward", 3000: "localforward"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listeners = %#v, want %#v", got, want)
	}
}

func TestParseEffectiveListenersRejectsUnknownSyntax(t *testing.T) {
	_, err := parseEffectiveListeners(strings.NewReader("localforward relative.sock [service]:80\n"))
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("error = %v", err)
	}
}

func TestEffectiveListenersUsesSSH(t *testing.T) {
	tempDir := t.TempDir()
	sshPath := filepath.Join(tempDir, "ssh")
	argsPath := filepath.Join(tempDir, "args")
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + argsPath + `"
printf '%s\n' 'dynamicforward [127.0.0.1]:1080' 'localforward 3000 [service]:3030'
`
	if err := os.WriteFile(sshPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	listeners, err := effectiveListeners(context.Background(), sshPath, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if listeners[1080] != "dynamicforward" || listeners[3000] != "localforward" {
		t.Fatalf("listeners = %#v", listeners)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "ClearAllForwardings=no") {
		t.Fatalf("ssh args = %q", args)
	}
}

func TestCheckConflicts(t *testing.T) {
	tests := []struct {
		name       string
		forwards   []ResolvedForward
		configured map[int]string
		wantErr    string
	}{
		{
			name: "no conflict",
			forwards: []ResolvedForward{
				{ForwardConfig: ForwardConfig{Service: "api", Local: LocalAddress{Port: 8080}}},
				{ForwardConfig: ForwardConfig{Service: "db", Local: LocalAddress{Port: 5432}}},
			},
			configured: map[int]string{1080: "dynamicforward"},
		},
		{
			name:       "SSH conflict",
			forwards:   []ResolvedForward{{ForwardConfig: ForwardConfig{Service: "api", Local: LocalAddress{Port: 8080}}}},
			configured: map[int]string{8080: "localforward"},
			wantErr:    "conflicts with SSH localforward",
		},
		{
			name: "generated conflict",
			forwards: []ResolvedForward{
				{ForwardConfig: ForwardConfig{Service: "api", Local: LocalAddress{Port: 8080}}},
				{ForwardConfig: ForwardConfig{Service: "admin", Local: LocalAddress{Port: 8080}}},
			},
			wantErr: "used by services",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := checkConflicts(test.forwards, test.configured)
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestTunnelArgs(t *testing.T) {
	forwards := []ResolvedForward{
		{
			ForwardConfig: ForwardConfig{Local: LocalAddress{Host: "127.0.0.1", Port: 3000}},
			Endpoint:      resolver.Endpoint{Address: "10.0.0.2", Port: 3030},
		},
		{
			ForwardConfig: ForwardConfig{Local: LocalAddress{Host: "::1", Port: 8080}},
			Endpoint:      resolver.Endpoint{Address: "2001:db8::1", Port: 80},
		},
	}
	got := tunnelArgs("dc1-prod", forwards)
	want := []string{
		"-N", "-T",
		"-o", "ClearAllForwardings=no",
		"-L", "127.0.0.1:3000:10.0.0.2:3030",
		"-L", "[::1]:8080:[2001:db8::1]:80",
		"--", "dc1-prod",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}
