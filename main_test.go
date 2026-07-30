package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0neSe7en/sshfwd/internal/resolver"
)

type staticResolver struct {
	endpoints map[string]resolver.Endpoint
	errors    map[string]error
}

func (r staticResolver) Resolve(_ context.Context, _, service string) (resolver.Endpoint, error) {
	if err := r.errors[service]; err != nil {
		return resolver.Endpoint{}, err
	}
	return r.endpoints[service], nil
}

type blockingResolver struct {
	started chan<- string
	release <-chan struct{}
}

func (r blockingResolver) Resolve(ctx context.Context, _, service string) (resolver.Endpoint, error) {
	r.started <- service
	select {
	case <-r.release:
		return resolver.Endpoint{Address: service, Port: 80}, nil
	case <-ctx.Done():
		return resolver.Endpoint{}, ctx.Err()
	}
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		args    []string
		command command
		host    string
		help    bool
		wantErr bool
	}{
		{args: []string{"dc1-prod"}, command: commandTunnel, host: "dc1-prod"},
		{args: []string{"export", "dc1-prod"}, command: commandExport, host: "dc1-prod"},
		{args: []string{"hosts", "ls"}, command: commandHostsList},
		{args: []string{"--help"}, help: true},
		{wantErr: true},
		{args: []string{"resolve", "dc1-prod"}, wantErr: true},
	}

	for _, test := range tests {
		gotCommand, gotHost, gotHelp, err := parseArgs(test.args)
		if test.wantErr {
			if err == nil {
				t.Fatalf("parseArgs(%q) returned no error", test.args)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseArgs(%q): %v", test.args, err)
		}
		if gotCommand != test.command || gotHost != test.host || gotHelp != test.help {
			t.Fatalf("parseArgs(%q) = (%v, %q, %t)", test.args, gotCommand, gotHost, gotHelp)
		}
	}
}

func TestRunListsHosts(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.Mkdir(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `
[resolvers.consul]
type = "consul"
address = "127.0.0.1:8500"

[hosts.zeta]
resolver = "consul"

[hosts.zeta.forwards]
api = "8080"

[hosts.alpha]
resolver = "consul"

[hosts.alpha.forwards]
api = "8081"
`
	if err := os.WriteFile(filepath.Join(sshDir, "sshfwd.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"hosts", "ls"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if want := "alpha\nzeta\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestResolveForwards(t *testing.T) {
	configs := []ForwardConfig{
		{Service: "api", Local: LocalAddress{Host: "127.0.0.1", Port: 8080}},
		{Service: "db", Local: LocalAddress{Host: "127.0.0.1", Port: 5432}},
	}
	serviceResolver := staticResolver{endpoints: map[string]resolver.Endpoint{
		"api": {Address: "10.0.0.1", Port: 80},
		"db":  {Address: "10.0.0.2", Port: 5432},
	}}

	got, errs := resolveForwards(context.Background(), serviceResolver, "demo", configs)
	if len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	if len(got) != 2 || got[0].Endpoint != serviceResolver.endpoints["api"] || got[1].Endpoint != serviceResolver.endpoints["db"] {
		t.Fatalf("forwards = %#v", got)
	}
}

func TestResolveForwardsKeepsSuccessfulResults(t *testing.T) {
	configs := []ForwardConfig{
		{Service: "api", Local: LocalAddress{Host: "127.0.0.1", Port: 8080}},
		{Service: "missing", Local: LocalAddress{Host: "127.0.0.1", Port: 8081}},
		{Service: "db", Local: LocalAddress{Host: "127.0.0.1", Port: 5432}},
	}
	serviceResolver := staticResolver{
		endpoints: map[string]resolver.Endpoint{
			"api": {Address: "10.0.0.1", Port: 80},
			"db":  {Address: "10.0.0.2", Port: 5432},
		},
		errors: map[string]error{
			"missing": errors.New(`resolve service "missing": no instances registered`),
		},
	}

	got, errs := resolveForwards(context.Background(), serviceResolver, "demo", configs)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `service "missing"`) {
		t.Fatalf("errors = %v", errs)
	}
	if len(got) != 2 || got[0].Service != "api" || got[1].Service != "db" {
		t.Fatalf("forwards = %#v", got)
	}
}

func TestResolveForwardsConcurrently(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	serviceResolver := blockingResolver{started: started, release: release}
	configs := []ForwardConfig{
		{Service: "api", Local: LocalAddress{Host: "127.0.0.1", Port: 8080}},
		{Service: "db", Local: LocalAddress{Host: "127.0.0.1", Port: 5432}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan []error, 1)
	go func() {
		_, errs := resolveForwards(ctx, serviceResolver, "demo", configs)
		done <- errs
	}()

	for range configs {
		select {
		case <-started:
		case <-ctx.Done():
			close(release)
			t.Fatal("resolver calls did not start concurrently")
		}
	}
	close(release)
	if errs := <-done; len(errs) != 0 {
		t.Fatal(errs)
	}
}

func TestRunTunnelStartsWhenAllResolutionsFail(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.Mkdir(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `
[resolvers.consul]
type = "consul"
address = "127.0.0.1:8500"

[hosts.demo]
resolver = "consul"

[hosts.demo.forwards]
missing = "8081"
`
	if err := os.WriteFile(filepath.Join(sshDir, "sshfwd.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	finalArgsPath := filepath.Join(t.TempDir(), "final-args")
	fakeSSH := `#!/bin/sh
if [ "$1" = "-G" ]; then
  printf '%s\n' 'hostname example.com' 'clearallforwardings no'
elif printf '%s\n' "$@" | grep -q 'ClearAllForwardings=yes'; then
  printf '%s\n' 'service unavailable' >&2
  exit 22
else
  printf '%s\n' "$@" > "` + finalArgsPath + `"
fi
`
	if err := os.WriteFile(filepath.Join(binDir, "ssh"), []byte(fakeSSH), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"demo"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(finalArgsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "-L") {
		t.Fatalf("final SSH args = %q", args)
	}
	if !strings.Contains(stderr.String(), "sshfwd: resolve service \"missing\" through demo: exit status 22: service unavailable") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestPrintExport(t *testing.T) {
	forwards := []ResolvedForward{{
		ForwardConfig: ForwardConfig{Service: "api", Local: LocalAddress{Host: "127.0.0.1", Port: 8080}},
		Endpoint:      resolver.Endpoint{Address: "10.0.0.1", Port: 80},
	}}
	var output bytes.Buffer
	printExport(&output, "demo", forwards)

	want := `# Generated by sshfwd; Consul endpoints are a point-in-time snapshot.
Host demo
    LocalForward 127.0.0.1:8080 10.0.0.1:80
`
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func TestRunTunnelWithFakeSSH(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.Mkdir(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `
[resolvers.consul]
type = "consul"
address = "127.0.0.1:8500"

[hosts.demo]
resolver = "consul"

[hosts.demo.forwards]
api = "8080"
missing = "8081"
`
	if err := os.WriteFile(filepath.Join(sshDir, "sshfwd.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	finalArgsPath := filepath.Join(t.TempDir(), "final-args")
	fakeSSH := `#!/bin/sh
if [ "$1" = "-G" ]; then
  printf '%s\n' 'hostname example.com' 'clearallforwardings no'
elif printf '%s\n' "$@" | grep -q 'ClearAllForwardings=yes'; then
  case "$*" in
    *service/missing*)
      printf '%s\n' 'service unavailable' >&2
      exit 22
      ;;
    *)
      printf '%s' '[{"Node":"node-1","ServiceAddress":"10.0.0.1","ServicePort":80}]'
      ;;
  esac
else
  printf '%s\n' "$@" > "` + finalArgsPath + `"
fi
`
	if err := os.WriteFile(filepath.Join(binDir, "ssh"), []byte(fakeSSH), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"demo"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(finalArgsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "127.0.0.1:8080:10.0.0.1:80") {
		t.Fatalf("final SSH args = %q", args)
	}
	if !strings.Contains(stderr.String(), "api: 127.0.0.1:8080 -> 10.0.0.1:80") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "sshfwd: resolve service \"missing\" through demo: exit status 22: service unavailable") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if strings.Contains(string(args), "8081") {
		t.Fatalf("final SSH args = %q", args)
	}
}
