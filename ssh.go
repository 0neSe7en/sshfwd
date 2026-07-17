package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/0neSe7en/sshfwd/internal/resolver"
)

type ResolvedForward struct {
	ForwardConfig
	Endpoint resolver.Endpoint
}

type tunnelSignalError struct {
	signal os.Signal
}

func (e *tunnelSignalError) Error() string {
	return "tunnel terminated by " + e.signal.String()
}

func (e *tunnelSignalError) ExitCode() int {
	if sig, ok := e.signal.(syscall.Signal); ok {
		return 128 + int(sig)
	}
	return 1
}

func (f ResolvedForward) remoteAddress() string {
	return net.JoinHostPort(f.Endpoint.Address, strconv.Itoa(f.Endpoint.Port))
}

func effectiveListeners(ctx context.Context, sshBinary, host string) (map[int]string, error) {
	cmd := exec.CommandContext(ctx, sshBinary, "-G", "-o", "ClearAllForwardings=no", "--", host)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return nil, fmt.Errorf("read SSH configuration for %s: %w: %s", host, err, message)
		}
		return nil, fmt.Errorf("read SSH configuration for %s: %w", host, err)
	}
	return parseEffectiveListeners(bytes.NewReader(output))
}

func parseEffectiveListeners(reader io.Reader) (map[int]string, error) {
	listeners := make(map[int]string)
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || (fields[0] != "localforward" && fields[0] != "dynamicforward") {
			continue
		}
		if len(fields) < 2 {
			return nil, fmt.Errorf("unsupported ssh -G forwarding line %q", scanner.Text())
		}

		local := fields[1]
		if strings.HasPrefix(local, "/") {
			continue
		}
		port, err := localPort(local)
		if err != nil {
			return nil, fmt.Errorf("unsupported ssh -G forwarding line %q", scanner.Text())
		}
		listeners[port] = fields[0]
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return listeners, nil
}

func localPort(value string) (int, error) {
	portText := value
	if index := strings.LastIndex(value, ":"); index >= 0 {
		portText = value[index+1:]
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port")
	}
	return port, nil
}

func checkConflicts(forwards []ForwardConfig, configured map[int]string) error {
	seen := make(map[int]string)
	for _, forward := range forwards {
		if kind, ok := configured[forward.Local.Port]; ok {
			return fmt.Errorf("local port %d for service %q conflicts with SSH %s", forward.Local.Port, forward.Service, kind)
		}
		if service, ok := seen[forward.Local.Port]; ok {
			return fmt.Errorf("local port %d is used by services %q and %q", forward.Local.Port, service, forward.Service)
		}
		seen[forward.Local.Port] = forward.Service
	}
	return nil
}

func tunnelArgs(host string, forwards []ResolvedForward) []string {
	args := []string{
		"-N",
		"-T",
		"-o", "ClearAllForwardings=no",
	}
	for _, forward := range forwards {
		args = append(args, "-L", forward.Local.String()+":"+forward.remoteAddress())
	}
	return append(args, "--", host)
}

func runTunnel(ctx context.Context, sshBinary, host string, forwards []ResolvedForward) error {
	cmd := exec.CommandContext(ctx, sshBinary, tunnelArgs(host, forwards)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	signals := make(chan os.Signal, 4)
	signal.Notify(signals, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGQUIT)
	defer signal.Stop(signals)

	if err := cmd.Start(); err != nil {
		return err
	}

	wait := make(chan error, 1)
	go func() {
		wait <- cmd.Wait()
	}()

	select {
	case sig := <-signals:
		if err := cmd.Process.Signal(sig); err != nil {
			_ = cmd.Process.Kill()
		}
		<-wait
		return &tunnelSignalError{signal: sig}
	case err := <-wait:
		signal.Stop(signals)
		select {
		case sig := <-signals:
			return &tunnelSignalError{signal: sig}
		default:
			return err
		}
	}
}
