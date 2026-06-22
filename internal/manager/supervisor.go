package manager

import (
	"time"

	"github.com/jesse/codex-app-proxy/internal/logging"
)

type WorkerSupervisor struct {
	name              string
	process           ManagedProcess
	state             WorkerState
	appliedGeneration int
	retries           int
	healthySince      time.Time
	logSink           *logging.WorkerLogSink
	configPatchState  string
	configPatchDetail map[string]string
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

func (s *WorkerSupervisor) setConfigPatchStatus(state string, detail map[string]string) {
	s.configPatchState = state
	if len(detail) == 0 {
		s.configPatchDetail = nil
		return
	}
	cloned := make(map[string]string, len(detail))
	for key, value := range detail {
		cloned[key] = value
	}
	s.configPatchDetail = cloned
}

func (s *WorkerSupervisor) ConfigPatchState() string {
	if s == nil {
		return ""
	}
	return s.configPatchState
}

func (s *WorkerSupervisor) ConfigPatchDetail() map[string]string {
	if s == nil || len(s.configPatchDetail) == 0 {
		return nil
	}
	out := make(map[string]string, len(s.configPatchDetail))
	for key, value := range s.configPatchDetail {
		out[key] = value
	}
	return out
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
