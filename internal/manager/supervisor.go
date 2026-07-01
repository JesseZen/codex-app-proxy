package manager

import (
	"time"

	"github.com/jesse/agent-inn/internal/logging"
	"github.com/jesse/agent-inn/internal/modulehook"
)

type WorkerSupervisor struct {
	name              string
	process           ManagedProcess
	state             WorkerState
	appliedGeneration int
	retries           int
	healthySince      time.Time
	logSink           *logging.WorkerLogSink
	hookStatuses      map[string]modulehook.Status
	lastError         string
}

func newWorkerSupervisor(name string) *WorkerSupervisor {
	return &WorkerSupervisor{name: name, state: WorkerStateConfigured}
}

func (s *WorkerSupervisor) Status() WorkerState {
	if s == nil || s.state == "" {
		return WorkerStateConfigured
	}
	return s.state
}

func (s *WorkerSupervisor) AppliedGeneration() int {
	if s == nil {
		return 0
	}
	return s.appliedGeneration
}

func (s *WorkerSupervisor) setStatus(state WorkerState) {
	s.state = state
}

func (s *WorkerSupervisor) Process() ManagedProcess {
	if s == nil {
		return nil
	}
	return s.process
}

func (s *WorkerSupervisor) setAppliedGeneration(generation int) {
	if generation < 1 {
		generation = 1
	}
	s.appliedGeneration = generation
}

func (s *WorkerSupervisor) setProcess(process ManagedProcess) {
	s.process = process
}

func (s *WorkerSupervisor) clearProcess() {
	s.process = nil
}

func (s *WorkerSupervisor) setRetryCount(retries int) {
	if retries < 0 {
		retries = 0
	}
	s.retries = retries
}

func (s *WorkerSupervisor) RetryCount() int {
	if s == nil {
		return 0
	}
	return s.retries
}

func (s *WorkerSupervisor) setHealthySince(t time.Time) {
	s.healthySince = t
}

func (s *WorkerSupervisor) HealthySince() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.healthySince
}

func (s *WorkerSupervisor) setHookStatus(name string, status modulehook.Status) {
	if s.hookStatuses == nil {
		s.hookStatuses = map[string]modulehook.Status{}
	}
	s.hookStatuses[name] = cloneHookStatus(status)
}

func (s *WorkerSupervisor) HookStatuses() map[string]modulehook.Status {
	if s == nil || len(s.hookStatuses) == 0 {
		return nil
	}
	return cloneHookStatuses(s.hookStatuses)
}

func (s *WorkerSupervisor) setLastError(err string) {
	s.lastError = err
}

func (s *WorkerSupervisor) LastError() string {
	if s == nil {
		return ""
	}
	return s.lastError
}
