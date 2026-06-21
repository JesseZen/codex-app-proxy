package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jesse/codex-app-proxy/internal/config"
)

func TestRunVersionPrintsVersion(t *testing.T) {
	var stdout bytes.Buffer
	code := Run([]string{"version"}, &stdout, &bytes.Buffer{})

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "codex-proxy") {
		t.Fatalf("expected version output to name codex-proxy, got %q", stdout.String())
	}
}

func TestRunUnknownCommandReturnsUsageError(t *testing.T) {
	var stderr bytes.Buffer
	code := Run([]string{"unknown"}, &bytes.Buffer{}, &stderr)

	if code == 0 {
		t.Fatal("expected non-zero exit code")
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("expected unknown command error, got %q", stderr.String())
	}
}

func TestRunDefaultStartsRootRunnerWithConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
workers:
  app:
    port: 6767
    provider: openai
providers:
  openai:
    base_url: https://api.openai.com/v1
`), 0600); err != nil {
		t.Fatal(err)
	}

	var called bool
	restore := SetRootRunnerForTest(func(opts RootOptions) error {
		called = true
		if opts.ConfigPath != configPath {
			t.Fatalf("unexpected config path %s", opts.ConfigPath)
		}
		if len(opts.Config.Workers) != 1 {
			t.Fatalf("config was not loaded: %#v", opts.Config)
		}
		return nil
	})
	defer restore()

	var stderr bytes.Buffer
	code := Run([]string{"--config", configPath, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d: %s", code, stderr.String())
	}
	if !called {
		t.Fatal("root runner was not called")
	}
}

func TestRunDefaultContinuesWhenConfiguredWorkerWillFailToStart(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
workers:
  bad:
    port: 6767
    provider: missing
providers:
  openai:
    base_url: https://api.openai.com/v1
`), 0600); err != nil {
		t.Fatal(err)
	}

	var called bool
	restore := SetRootRunnerForTest(func(opts RootOptions) error {
		called = true
		if len(opts.Config.Workers) != 1 {
			t.Fatalf("config was not loaded: %#v", opts.Config)
		}
		return nil
	})
	defer restore()

	var stderr bytes.Buffer
	code := Run([]string{"--config", configPath, "--manager-port", "19090"}, &bytes.Buffer{}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0 despite failed worker config, got %d: %s", code, stderr.String())
	}
	if !called {
		t.Fatal("root runner was not called")
	}
}

func TestRootRunnerContinuesAfterConfiguredWorkerStartupFailure(t *testing.T) {
	startErr := errors.New("app: config_patch recovery state unresolved must be resolved before enabling")
	mgr := &fakeRootManager{startErr: startErr}
	server := &fakeRootServer{listenStarted: make(chan struct{})}
	program := &fakeRootProgram{waitForListen: server.listenStarted}

	restoreManager := setRootManagerFactoryForTest(func(opts RootOptions) rootManager {
		return mgr
	})
	defer restoreManager()
	restoreServer := setRootServerFactoryForTest(func(addr string, handler http.Handler) rootServer {
		server.addr = addr
		server.handler = handler
		return server
	})
	defer restoreServer()
	restoreProgram := func() func() {
		previous := rootProgramFactory
		rootProgramFactory = func(addr string, startupStatus string) rootProgram {
			program.addr = addr
			program.startupStatus = startupStatus
			return program
		}
		return func() { rootProgramFactory = previous }
	}()
	defer restoreProgram()

	err := rootRunner(RootOptions{
		ManagerPort: 19090,
		Config:      config.Config{},
	})
	if err != nil {
		t.Fatalf("expected root runner to keep manager running despite worker startup failure, got %v", err)
	}
	if !mgr.startConfiguredWorkersCalled {
		t.Fatal("expected root runner to attempt configured worker startup")
	}
	if !mgr.startHealthMonitorCalled {
		t.Fatal("expected health monitor to start after worker startup failure")
	}
	if !server.listenCalled {
		t.Fatal("expected manager API server to start after worker startup failure")
	}
	if !program.runCalled {
		t.Fatal("expected TUI program to run after worker startup failure")
	}
	if !server.closeCalled {
		t.Fatal("expected server to be closed when program exits")
	}
	if program.startupStatus != startErr.Error() {
		t.Fatalf("expected startup status %q, got %q", startErr.Error(), program.startupStatus)
	}
}

func TestRootProgramFactoryBuildsTypeScriptTUICommand(t *testing.T) {
	program := rootProgramFactory("127.0.0.1:8787", "")
	cmd := program.CommandLine()
	if cmd[len(cmd)-2] != "run" || cmd[len(cmd)-1] != "src/cli.ts" {
		t.Fatalf("expected bun run src/cli.ts command, got %#v", cmd)
	}
	if program.WorkingDir() != "tui" {
		t.Fatalf("expected tui working dir, got %q", program.WorkingDir())
	}
	if program.Env()["CODEX_PROXY_URL"] != "http://127.0.0.1:8787" {
		t.Fatalf("expected CODEX_PROXY_URL for manager API, got %#v", program.Env())
	}
	if program.Env()["CODEX_PROXY_PROJECT_DIR"] == "" {
		t.Fatalf("expected CODEX_PROXY_PROJECT_DIR to be set, got %#v", program.Env())
	}
}

func TestRootRunnerDoesNotWriteConfiguredWorkerStartupFailureToTerminal(t *testing.T) {
	startErr := errors.New("cli-groq: missing API key")
	mgr := &fakeRootManager{startErr: startErr}
	server := &fakeRootServer{listenStarted: make(chan struct{})}
	program := &fakeRootProgram{waitForListen: server.listenStarted}

	var logOutput bytes.Buffer
	restoreLogWriter := setRootLogWriterForTest(&logOutput)
	defer restoreLogWriter()

	restoreManager := setRootManagerFactoryForTest(func(opts RootOptions) rootManager {
		return mgr
	})
	defer restoreManager()
	restoreServer := setRootServerFactoryForTest(func(addr string, handler http.Handler) rootServer {
		server.addr = addr
		server.handler = handler
		return server
	})
	defer restoreServer()
	restoreProgram := func() func() {
		previous := rootProgramFactory
		rootProgramFactory = func(addr string, startupStatus string) rootProgram {
			program.addr = addr
			program.startupStatus = startupStatus
			return program
		}
		return func() { rootProgramFactory = previous }
	}()
	defer restoreProgram()

	err := rootRunner(RootOptions{ManagerPort: 19090, Config: config.Config{}})
	if err != nil {
		t.Fatalf("expected root runner to keep running, got %v", err)
	}
	if strings.Contains(logOutput.String(), startErr.Error()) {
		t.Fatalf("startup error should not be written to terminal log output: %q", logOutput.String())
	}
}

type fakeRootManager struct {
	startErr                    error
	startConfiguredWorkersCalled bool
	startHealthMonitorCalled     bool
	closeCalled                  bool
}

func (m *fakeRootManager) ServeHTTP(http.ResponseWriter, *http.Request) {}

func (m *fakeRootManager) Close() {
	m.closeCalled = true
}

func (m *fakeRootManager) StartConfiguredWorkers() error {
	m.startConfiguredWorkersCalled = true
	return m.startErr
}

func (m *fakeRootManager) StartHealthMonitor(_ time.Duration) func() {
	m.startHealthMonitorCalled = true
	return func() {}
}

type fakeRootServer struct {
	addr         string
	handler      http.Handler
	listenStarted chan struct{}
	listenCalled bool
	closeCalled  bool
}

func (s *fakeRootServer) ensureListenStarted() {
	if s.listenStarted == nil {
		s.listenStarted = make(chan struct{})
	}
}

func (s *fakeRootServer) ListenAndServe() error {
	s.ensureListenStarted()
	s.listenCalled = true
	close(s.listenStarted)
	return http.ErrServerClosed
}

func (s *fakeRootServer) Close() error {
	s.closeCalled = true
	return nil
}

type fakeRootProgram struct {
	addr          string
	waitForListen <-chan struct{}
	runCalled     bool
	startupStatus string
}

func (p *fakeRootProgram) Run() error {
	p.runCalled = true
	if p.waitForListen != nil {
		select {
		case <-p.waitForListen:
		case <-time.After(time.Second):
			return errors.New("timed out waiting for server startup")
		}
	}
	return nil
}

func (p *fakeRootProgram) CommandLine() []string {
	return []string{"fake"}
}

func (p *fakeRootProgram) WorkingDir() string {
	return ""
}

func (p *fakeRootProgram) Env() map[string]string {
	return map[string]string{}
}
