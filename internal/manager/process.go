package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type WorkerSpawn struct {
	Args        []string
	RuntimeJSON []byte
	LogWriter   io.Writer
}

type ExecStarter struct {
	Executable      string
	StopGracePeriod time.Duration
}

type ExecProcess struct {
	mu              sync.Mutex
	cmd             *exec.Cmd
	stdin           *os.File
	configRead      *os.File
	stopGracePeriod time.Duration
	forcedStop      bool
}

const defaultStopGracePeriod = 3 * time.Second

func (s ExecStarter) Start(spawn WorkerSpawn) (ManagedProcess, error) {
	executable := s.Executable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return nil, err
		}
	}
	configRead, configWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		_ = configRead.Close()
		_ = configWrite.Close()
		return nil, err
	}

	cmd := exec.Command(executable, spawn.Args...)
	cmd.Env = sanitizedWorkerEnv(os.Environ())
	cmd.ExtraFiles = []*os.File{configRead}
	cmd.Stdin = stdinRead
	if spawn.LogWriter != nil {
		cmd.Stdout = spawn.LogWriter
		cmd.Stderr = spawn.LogWriter
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Start(); err != nil {
		_ = configRead.Close()
		_ = configWrite.Close()
		_ = stdinRead.Close()
		_ = stdinWrite.Close()
		return nil, err
	}
	_ = configRead.Close()
	_ = stdinRead.Close()
	if _, err := configWrite.Write(spawn.RuntimeJSON); err != nil {
		_ = configWrite.Close()
		_ = stdinWrite.Close()
		_ = cmd.Process.Kill()
		return nil, err
	}
	if err := configWrite.Close(); err != nil {
		_ = stdinWrite.Close()
		_ = cmd.Process.Kill()
		return nil, err
	}
	stopGracePeriod := s.StopGracePeriod
	if stopGracePeriod <= 0 {
		stopGracePeriod = defaultStopGracePeriod
	}
	return &ExecProcess{cmd: cmd, stdin: stdinWrite, stopGracePeriod: stopGracePeriod}, nil
}

func sanitizedWorkerEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || isSecretEnvName(name) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func isSecretEnvName(name string) bool {
	upper := strings.ToUpper(name)
	secretMarkers := []string{
		"API_KEY",
		"ACCESS_KEY",
		"SECRET_KEY",
		"PRIVATE_KEY",
		"AUTH_TOKEN",
		"ACCESS_TOKEN",
		"REFRESH_TOKEN",
		"BEARER_TOKEN",
		"CLIENT_SECRET",
		"WEBHOOK_SECRET",
		"PASSWORD",
		"PASSWD",
		"TOKEN",
		"SECRET",
	}
	for _, marker := range secretMarkers {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func (p *ExecProcess) Stop() error {
	p.mu.Lock()
	cmd := p.cmd
	stdin := p.stdin
	stopGracePeriod := p.stopGracePeriod
	p.cmd = nil
	p.stdin = nil
	p.mu.Unlock()

	if cmd == nil {
		return nil
	}
	if cmd.Process != nil {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errorsIsProcessDone(err) {
			return err
		}
	}
	if stdin != nil {
		_ = stdin.Close()
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		return ignoreManagedStopExit(err)
	case <-time.After(stopGracePeriod):
		p.markForcedStop()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		err := <-done
		return ignoreManagedStopExit(err)
	}
}

func (p *ExecProcess) ForcedStop() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.forcedStop
}

func (p *ExecProcess) markForcedStop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.forcedStop = true
}

func errorsIsProcessDone(err error) bool {
	return errors.Is(err, os.ErrProcessDone)
}

func ignoreManagedStopExit(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ProcessState != nil {
		if status, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			switch status.Signal() {
			case syscall.SIGTERM, syscall.SIGKILL:
				return nil
			}
		}
	}
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (m *Manager) BuildWorkerSpawn(workerName string) (WorkerSpawn, error) {
	runtime, err := m.runtimeForWorker(workerName)
	if err != nil {
		return WorkerSpawn{}, err
	}
	payload, err := json.Marshal(runtime)
	if err != nil {
		return WorkerSpawn{}, err
	}
	return WorkerSpawn{
		Args:        []string{"worker", "--port", fmt.Sprintf("%d", runtime.ListenPort), "--config-fd", "3"},
		RuntimeJSON: payload,
	}, nil
}
