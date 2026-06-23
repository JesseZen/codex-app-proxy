package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jesse/codex-app-proxy/internal/manager"
)

func TestRunLaunchRequiresWorker(t *testing.T) {
	var stderr bytes.Buffer
	code := runLaunch([]string{"--cd", "/tmp/work"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(stderr.String(), "launch requires --worker") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestRunLaunchRunsBuiltCommand(t *testing.T) {
	var got []string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append([]string{}, args...)
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--worker", "11199", "--profile", "cli-openai", "--cd", "/tmp/work", "--add-dir", "/tmp/shared", "--model", "gpt-5.5"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}
	if len(got) == 0 || got[0] != "codex" {
		t.Fatalf("unexpected command: %#v", got)
	}
	if strings.Join(got, " ") != strings.Join([]string{"codex", "--profile", "cli-openai", "--cd", "/tmp/work", "--add-dir", "/tmp/shared", "--model", "gpt-5.5"}, " ") {
		t.Fatalf("unexpected launch args: %#v", got)
	}
}

func TestRunLaunchExplicitExternalWindowMode(t *testing.T) {
	var got []string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append([]string{}, args...)
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--worker", "11199", "--mode", "external-window"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}
	if len(got) == 0 || got[0] != "codex" {
		t.Fatalf("external-window should run codex directly, got %#v", got)
	}
}

func TestRunLaunchExternalWindowUsesDirectExecWithTerminalStreams(t *testing.T) {
	dir := t.TempDir()
	codexPath := filepath.Join(dir, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	called := false
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			called = true
			return launchRunnerFunc(func(args []string) (string, error) {
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	if code := runLaunch([]string{"--worker", "11199", "--mode", "external-window"}, os.Stdout, os.Stderr); code != 0 {
		t.Fatalf("expected success, got %d", code)
	}
	if called {
		t.Fatal("expected direct exec path, not launchRunnerFactory")
	}
}

func TestRunLaunchRejectsInvalidMode(t *testing.T) {
	var stderr bytes.Buffer
	code := runLaunch([]string{"--worker", "11199", "--mode", "bogus"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected failure for invalid mode")
	}
	if !strings.Contains(stderr.String(), "invalid mode") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestRunLaunchHostedTerminalRunsTmuxSequence(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "cap-test", "cap-test-host")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "off\n", nil
				}
				// Simulate fresh tmux host: has-session and select-window fail.
				// tmux subcommand sits at args[3] after `tmux -L cap`.
				if len(args) > 3 && args[3] == "has-session" {
					return "", errors.New("can't find session")
				}
				if len(args) > 3 && args[3] == "select-window" {
					return "", errors.New("can't find window")
				}
				if len(args) > 3 && args[3] == "new-window" {
					return "@12\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--cd", "/tmp/work", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	want := [][]string{
		manager.TmuxDetectCommand(),
		{"tmux", "-L", "cap-test", "has-session", "-t", "cap-test-host"},
		{"tmux", "-L", "cap-test", "new-session", "-d", "-s", "cap-test-host"},
		{"tmux", "-L", "cap-test", "show", "-gv", "mouse"},
		{"tmux", "-L", "cap-test", "set-option", "-g", "mouse", "on"},
		{"tmux", "-L", "cap-test", "select-window", "-t", "cap-test-host:solve problem A"},
		{"tmux", "-L", "cap-test", "new-window", "-t", "cap-test-host", "-n", "solve problem A", "-P", "-F", "#{window_id}", "codex", "--profile", "cli-openai", "--cd", "/tmp/work"},
		{"tmux", "-L", "cap-test", "attach-session", "-t", "cap-test-host"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i, w := range want {
		if strings.Join(got[i], " ") != strings.Join(w, " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], w)
		}
	}

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	records, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one hosted session, got %#v", records)
	}
	if records[0].SessionLabel != "solve problem A" || records[0].TmuxWindowID != "@12" {
		t.Fatalf("expected label and real window id in registry, got %#v", records[0])
	}
}

func TestRunLaunchHostedTerminalSwitchesExistingWindow(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "cap", "cap-host")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				// Simulate existing host and window: has-session and select-window succeed.
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateWindowID(created.SessionID, "@12"); err != nil {
		t.Fatal(err)
	}

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-id", created.SessionID}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		{"tmux", "-L", "cap", "show", "-gv", "mouse"},
		manager.TmuxSelectWindowCommand("@12"),
		manager.TmuxAttachCommand(),
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i, w := range want {
		if strings.Join(got[i], " ") != strings.Join(w, " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], w)
		}
	}
}

func TestRunLaunchHostedTerminalMissingTmux(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	writeLaunchConfig(t, configDir, filepath.Join(dir, "state"), "cap", "cap-host")

	var stderr bytes.Buffer
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				return "", errors.New("exec: \"tmux\": executable file not found")
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--mode", "hosted-terminal", "--session-label", "solve problem A"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected failure when tmux is missing")
	}
	if !strings.Contains(stderr.String(), "tmux is required for hosted-terminal mode") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestRunLaunchHostedTerminalNoAttachSkipsAttach(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "cap", "cap-host")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "off\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateWindowID(created.SessionID, "@12"); err != nil {
		t.Fatal(err)
	}

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-id", created.SessionID, "--no-attach"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		{"tmux", "-L", "cap", "show", "-gv", "mouse"},
		{"tmux", "-L", "cap", "set-option", "-g", "mouse", "on"},
		manager.TmuxSelectWindowCommand("@12"),
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands (no attach), got %d: %#v", len(want), len(got), got)
	}
	for i, w := range want {
		if strings.Join(got[i], " ") != strings.Join(w, " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], w)
		}
	}
}

func TestRunLaunchHostedTerminalKeepsMouseWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	stateDir := filepath.Join(dir, "state")
	writeLaunchConfig(t, configDir, stateDir, "cap", "cap-host")

	var got [][]string
	restore := func() func() {
		previous := launchRunnerFactory
		launchRunnerFactory = func(stdout io.Writer, stderr io.Writer) launchRunner {
			return launchRunnerFunc(func(args []string) (string, error) {
				got = append(got, append([]string{}, args...))
				if len(args) > 3 && args[3] == "show" {
					return "on\n", nil
				}
				return "", nil
			})
		}
		return func() { launchRunnerFactory = previous }
	}()
	defer restore()

	registry := manager.NewHostedSessionRegistry(manager.HostedSessionRegistryPath(stateDir))
	created, err := registry.Create(manager.HostedSessionRecord{
		SessionLabel: "solve problem A",
		WorkerName:   "cli-openai",
		WorkerPort:   11199,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.UpdateWindowID(created.SessionID, "@12"); err != nil {
		t.Fatal(err)
	}

	code := runLaunch([]string{"--config-dir", configDir, "--worker", "11199", "--profile", "cli-openai", "--mode", "hosted-terminal", "--session-id", created.SessionID}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("expected success, got %d", code)
	}

	want := [][]string{
		manager.TmuxDetectCommand(),
		manager.TmuxHasSessionCommand(),
		{"tmux", "-L", "cap", "show", "-gv", "mouse"},
		manager.TmuxSelectWindowCommand("@12"),
		manager.TmuxAttachCommand(),
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d commands, got %d: %#v", len(want), len(got), got)
	}
	for i, w := range want {
		if strings.Join(got[i], " ") != strings.Join(w, " ") {
			t.Fatalf("command %d:\n got %#v\nwant %#v", i, got[i], w)
		}
	}
}

func writeLaunchConfig(t *testing.T, configDir string, stateDir string, socketName string, hostSession string) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`
settings:
  state_dir: ` + stateDir + `
  log_dir: ` + filepath.Join(configDir, "logs") + `
  launch:
    default_mode: hosted-terminal
  terminal:
    host: tmux
    opener: terminal_app
    tmux:
      socket_name: ` + socketName + `
      host_session: ` + hostSession + `
workers:
  cli-openrouter:
    port: 11199
    upstream: openrouter
upstreams:
  openrouter:
    base_url: https://openrouter.ai/api/v1
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRenderCodexLaunchCommand(t *testing.T) {
	got := manager.BuildCodexLaunchCommand(manager.CodexLaunchOptions{Profile: "11199", WorkerPort: 11199})
	if len(got) != 3 {
		t.Fatalf("unexpected launch command: %#v", got)
	}
}

func TestRunLaunchRejectsBadWorker(t *testing.T) {
	var stderr bytes.Buffer
	code := runLaunch([]string{"--worker", "abc"}, &bytes.Buffer{}, &stderr)
	if code == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(stderr.String(), "invalid worker port") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}
