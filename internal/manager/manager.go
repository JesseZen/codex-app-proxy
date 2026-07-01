package manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/constants"
	"github.com/jesse/agent-inn/internal/logging"
	"github.com/jesse/agent-inn/internal/module"
	"github.com/jesse/agent-inn/internal/modulehook"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
	"github.com/jesse/agent-inn/internal/upstream"
)

type Config struct {
	Config        config.Config
	ConfigPath    string
	ConfigStatus  config.Status
	Executable    string
	Starter       Starter
	HealthChecker HealthChecker
	WorkerClient  WorkerClient
}

type Manager struct {
	mu               sync.RWMutex
	config           config.Config
	configPath       string
	configStatus     config.Status
	executable       string
	starter          Starter
	healthChecker    HealthChecker
	workerClient     WorkerClient
	clock            func() time.Time
	healthWait       time.Duration
	healthPoll       time.Duration
	store            *config.Store
	stopConfigWriter func()
	events           *eventBus
	portIndex        map[int]string
	supervisors      map[string]*WorkerSupervisor
	processes        map[string]ManagedProcess
	statuses         map[string]WorkerState
	retries          map[string]int
	healthySince     map[string]time.Time
	generations      map[string]int
	logs             map[string]*logging.WorkerLogSink
	hookStatuses     map[string]map[string]modulehook.Status
	hostedSessions   *HostedSessionRegistry
}

type WorkerSummary struct {
	Name               string                         `json:"name"`
	Port               int                            `json:"port"`
	Role               string                         `json:"role"`
	Upstream           upstream.RedactedUpstream      `json:"upstream"`
	Status             string                         `json:"status"`
	SnapshotGeneration int                            `json:"snapshot_generation"`
	LogLevel           string                         `json:"log_level"`
	Modules            map[string]config.ModuleConfig `json:"modules,omitempty"`
	Hooks              map[string]config.ModuleConfig `json:"hooks,omitempty"`
}

type Starter interface {
	Start(spawn WorkerSpawn) (ManagedProcess, error)
}

type ManagedProcess interface {
	Stop() error
}

type forcedStopReporter interface {
	ForcedStop() bool
}

type HealthChecker interface {
	Check(port int) bool
}

type WorkerClient interface {
	ToggleModule(port int, moduleName string) error
	PatchModule(port int, moduleName string, cfg config.ModuleConfig) error
	ApplyRuntime(port int, runtime appruntime.WorkerRuntime) (ApplyRuntimeStatus, error)
	SwitchUpstream(port int, runtime upstream.RuntimeUpstream) error
	GetStatus(port int) (WorkerStatus, error)
}

type ApplyRuntimeStatus struct {
	AppliedGeneration  appruntime.Generation `json:"applied_generation"`
	SnapshotGeneration int                   `json:"snapshot_generation,omitempty"`
}

type WorkerStatus struct {
	SnapshotGeneration int                            `json:"snapshot_generation"`
	Upstream           upstream.RedactedUpstream      `json:"upstream"`
	Modules            map[string]config.ModuleConfig `json:"modules"`
	Hooks              map[string]config.ModuleConfig `json:"hooks,omitempty"`
	HookStatuses       map[string]modulehook.Status   `json:"hook_statuses,omitempty"`
}

type WorkerDetail struct {
	Name               string                         `json:"name"`
	Port               int                            `json:"port"`
	Role               string                         `json:"role"`
	Upstream           upstream.RedactedUpstream      `json:"upstream"`
	Status             string                         `json:"status"`
	SnapshotGeneration int                            `json:"snapshot_generation"`
	LogLevel           string                         `json:"log_level"`
	HookStatuses       map[string]modulehook.Status   `json:"hook_statuses,omitempty"`
	Modules            map[string]config.ModuleConfig `json:"modules,omitempty"`
	Hooks              map[string]config.ModuleConfig `json:"hooks,omitempty"`
}

const healthyRetryResetWindow = 60 * time.Second

func New(cfg Config) *Manager {
	cfg.Config.ApplyDefaults()
	store := config.NewStore(cfg.ConfigPath, cfg.Config)
	m := &Manager{
		config:         cfg.Config,
		configPath:     cfg.ConfigPath,
		configStatus:   cfg.ConfigStatus,
		executable:     cfg.Executable,
		starter:        cfg.Starter,
		healthChecker:  cfg.HealthChecker,
		workerClient:   cfg.WorkerClient,
		clock:          time.Now,
		healthWait:     10 * time.Second,
		healthPoll:     100 * time.Millisecond,
		store:          store,
		events:         newEventBus(defaultEventBusCapacity),
		portIndex:      map[int]string{},
		supervisors:    map[string]*WorkerSupervisor{},
		processes:      map[string]ManagedProcess{},
		statuses:       map[string]WorkerState{},
		retries:        map[string]int{},
		healthySince:   map[string]time.Time{},
		generations:    map[string]int{},
		logs:           map[string]*logging.WorkerLogSink{},
		hookStatuses:   map[string]map[string]modulehook.Status{},
		hostedSessions: NewHostedSessionRegistry(hostedSessionRegistryPath(cfg.Config.Settings.StateDir)),
	}
	if cfg.ConfigPath != "" {
		m.stopConfigWriter = store.StartAsyncWriter()
	}
	m.portIndex = buildPortIndex(m.config.Workers)
	if err := syncCodexProfileFiles(cfg.Config); err != nil {
		m.configStatus.LastSaveError = err.Error()
	}
	return m
}

func hostedSessionRegistryPath(stateDir string) string {
	if stateDir == "" {
		stateDir = "~/.ainn"
	}
	return filepath.Join(expandHomePath(stateDir), hostedSessionsFileName)
}

func (m *Manager) CheckPortAvailable(workerName string, port int) error {
	workers := m.workerConfigSnapshot()
	for name, worker := range workers {
		if name != workerName && worker.Port == port {
			return fmt.Errorf("port :%d is used by worker '%s'", port, name)
		}
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", constants.LocalhostAddr, port))
	if err != nil {
		return fmt.Errorf("port :%d is already in use by another process", port)
	}
	return listener.Close()
}

func (m *Manager) supervisorFor(name string) *WorkerSupervisor {
	if supervisor := m.supervisors[name]; supervisor != nil {
		return supervisor
	}
	supervisor := newWorkerSupervisor(name)
	m.supervisors[name] = supervisor
	return supervisor
}

func (m *Manager) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	m.registerRoutes(mux)
	mux.ServeHTTP(rw, r)
}

func (m *Manager) Close() {
	m.mu.Lock()
	stopConfigWriter := m.stopConfigWriter
	m.stopConfigWriter = nil
	events := m.events
	m.events = nil
	m.mu.Unlock()

	if stopConfigWriter != nil {
		stopConfigWriter()
	}
	if events != nil {
		events.Close()
	}
}

func (m *Manager) workerSummaries() []WorkerSummary {
	type summarySeed struct {
		name          string
		worker        config.WorkerConfig
		profile       config.UpstreamProfile
		providerFound bool
		status        string
		generation    int
	}

	m.mu.RLock()
	names := make([]string, 0, len(m.config.Workers))
	for name := range m.config.Workers {
		names = append(names, name)
	}
	sort.Strings(names)

	seeds := make([]summarySeed, 0, len(names))
	for _, name := range names {
		worker := m.config.Workers[name]
		profile, ok := m.config.Upstreams[worker.Upstream]
		seeds = append(seeds, summarySeed{
			name:          name,
			worker:        cloneWorkerConfig(worker),
			profile:       profile,
			providerFound: ok,
			status:        string(m.workerStatusLocked(name)),
			generation:    m.workerGenerationLocked(name),
		})
	}
	m.mu.RUnlock()

	out := make([]WorkerSummary, 0, len(seeds))
	for _, seed := range seeds {
		runtimeUpstream := upstream.RuntimeUpstream{Name: seed.worker.Upstream}
		if seed.providerFound {
			runtimeUpstream, _ = upstream.Resolve(seed.worker.Upstream, seed.profile)
		}
		out = append(out, WorkerSummary{
			Name:               seed.name,
			Port:               seed.worker.Port,
			Role:               seed.worker.Role,
			Upstream:           runtimeUpstream.Redacted(),
			Status:             seed.status,
			SnapshotGeneration: seed.generation,
			LogLevel:           workerLogLevel(seed.worker),
			Modules:            cloneModules(seed.worker.RequestModules),
			Hooks:              cloneModules(seed.worker.Hooks),
		})
	}
	return out
}

func (m *Manager) workerDetail(name string, worker config.WorkerConfig) WorkerDetail {
	runtimeUpstream := upstream.RuntimeUpstream{Name: worker.Upstream}
	if profile, ok := m.upstreamProfileSnapshot()[worker.Upstream]; ok {
		runtimeUpstream, _ = upstream.Resolve(worker.Upstream, profile)
	}

	detail := WorkerDetail{
		Name:               name,
		Port:               worker.Port,
		Role:               worker.Role,
		Upstream:           runtimeUpstream.Redacted(),
		Status:             string(m.workerStatus(name)),
		SnapshotGeneration: m.workerGeneration(name),
		LogLevel:           workerLogLevel(worker),
		Modules:            cloneModules(worker.RequestModules),
		Hooks:              cloneModules(worker.Hooks),
		HookStatuses:       m.hookStatusesForWorker(name),
	}

	if detail.Status != string(WorkerStateRunning) {
		return detail
	}

	client := m.workerClient
	if client == nil {
		client = HTTPWorkerClient{Client: http.DefaultClient}
	}
	status, err := client.GetStatus(worker.Port)
	if err != nil {
		return detail
	}
	if status.SnapshotGeneration > 0 {
		detail.SnapshotGeneration = status.SnapshotGeneration
	}
	if status.Upstream.Name != "" {
		detail.Upstream = status.Upstream
	}
	if status.Modules != nil {
		detail.Modules = cloneModules(status.Modules)
	}
	if status.Hooks != nil {
		detail.Hooks = cloneModules(status.Hooks)
	}
	if status.HookStatuses != nil {
		detail.HookStatuses = cloneHookStatuses(status.HookStatuses)
	}
	return detail
}

func workerLogLevel(worker config.WorkerConfig) string {
	if worker.LogLevel == "" {
		return "simple"
	}
	return worker.LogLevel
}

func (m *Manager) resolveUpstream(name string) (upstream.RuntimeUpstream, error) {
	m.mu.RLock()
	profile, ok := m.config.Upstreams[name]
	m.mu.RUnlock()
	if !ok {
		return upstream.RuntimeUpstream{Name: name}, fmt.Errorf("upstream %q not found", name)
	}
	return upstream.Resolve(name, profile)
}

func (m *Manager) runtimeForWorker(name string) (appruntime.WorkerRuntime, error) {
	m.mu.RLock()
	cfg := cloneConfig(m.config)
	generation := appruntime.Generation(m.workerGenerationLocked(name))
	m.mu.RUnlock()
	return (RuntimeBuilder{}).Build(cfg, name, generation)
}

func (m *Manager) validateWorkerRuntime(name string, worker config.WorkerConfig) error {
	m.mu.RLock()
	cfg := cloneConfig(m.config)
	generation := appruntime.Generation(m.workerGenerationLocked(name))
	m.mu.RUnlock()
	cfg.Workers[name] = cloneWorkerConfig(worker)
	_, err := (RuntimeBuilder{}).Build(cfg, name, generation)
	return err
}

func (m *Manager) workerByPort(port int) (string, config.WorkerConfig, bool) {
	m.mu.RLock()
	workerName := m.portIndex[port]
	if workerName == "" {
		m.mu.RUnlock()
		return "", config.WorkerConfig{}, false
	}
	worker, ok := m.config.Workers[workerName]
	m.mu.RUnlock()
	if !ok {
		return "", config.WorkerConfig{}, false
	}
	return workerName, cloneWorkerConfig(worker), true
}

func (m *Manager) updateConfig(fn func(*config.Config)) {
	m.store.Update(fn)
	_, status := m.syncConfigFromStore()
	if err := syncCodexProfileFiles(m.config); err != nil {
		m.mu.Lock()
		m.configStatus.LastSaveError = err.Error()
		m.mu.Unlock()
	}
	m.publishEvent(EventConfigStatusChanged, map[string]any{"dirty": status.Dirty, "generation": status.Generation})
}

func (m *Manager) syncConfigFromStore() (config.Config, config.Status) {
	cfg := cloneConfig(m.store.Config())
	status := m.store.Status()
	m.mu.Lock()
	m.config = cfg
	m.configStatus = status
	m.portIndex = buildPortIndex(cfg.Workers)
	m.mu.Unlock()
	return cfg, status
}

func (m *Manager) syncConfigStatusFromStore() config.Status {
	status := m.store.Status()
	m.mu.Lock()
	m.configStatus = status
	m.mu.Unlock()
	return status
}

func (m *Manager) publishEvent(eventType EventType, payload map[string]any) {
	m.mu.RLock()
	events := m.events
	m.mu.RUnlock()
	if events != nil {
		events.Publish(eventType, payload)
	}
}

func (m *Manager) workerStatus(name string) WorkerState {
	m.mu.RLock()
	if supervisor := m.supervisors[name]; supervisor != nil {
		status := supervisor.Status()
		if status != WorkerStateConfigured {
			m.mu.RUnlock()
			return status
		}
		if fallback := m.statuses[name]; fallback != "" {
			m.mu.RUnlock()
			return fallback
		}
		m.mu.RUnlock()
		return status
	}
	defer m.mu.RUnlock()
	return m.workerStatusLocked(name)
}

func (m *Manager) configPatchState(name string) string {
	return m.hookStatus(name, modulehook.ConfigPatchName).State
}

func (m *Manager) hookStatus(workerName string, hookName string) modulehook.Status {
	m.mu.RLock()
	if supervisor := m.supervisors[workerName]; supervisor != nil {
		statuses := supervisor.HookStatuses()
		if status := statuses[hookName]; status.State != "" || len(status.Detail) > 0 {
			m.mu.RUnlock()
			return status
		}
	}
	defer m.mu.RUnlock()
	return cloneHookStatus(m.hookStatuses[workerName][hookName])
}

func (m *Manager) hookStatusesForWorker(workerName string) map[string]modulehook.Status {
	m.mu.RLock()
	statuses := cloneHookStatuses(m.hookStatuses[workerName])
	if supervisor := m.supervisors[workerName]; supervisor != nil {
		for name, status := range supervisor.HookStatuses() {
			if statuses == nil {
				statuses = map[string]modulehook.Status{}
			}
			statuses[name] = cloneHookStatus(status)
		}
	}
	m.mu.RUnlock()
	return statuses
}

func cloneHookStatuses(statuses map[string]modulehook.Status) map[string]modulehook.Status {
	if len(statuses) == 0 {
		return nil
	}
	out := make(map[string]modulehook.Status, len(statuses))
	for name, status := range statuses {
		out[name] = cloneHookStatus(status)
	}
	return out
}

func cloneHookStatus(status modulehook.Status) modulehook.Status {
	next := modulehook.Status{State: status.State}
	if len(status.Detail) > 0 {
		next.Detail = make(map[string]string, len(status.Detail))
		for key, value := range status.Detail {
			next.Detail[key] = value
		}
	}
	return next
}

func (m *Manager) configPatchDetail(name string) map[string]string {
	return m.hookStatus(name, modulehook.ConfigPatchName).Detail
}

func (m *Manager) setConfigPatchStatus(name string, state modulehook.ConfigPatchState, detail map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := modulehook.Status{State: string(state), Detail: detail}
	m.supervisorFor(name).setHookStatus(modulehook.ConfigPatchName, status)
	if state == "" || state == modulehook.ConfigPatchClean || state == modulehook.ConfigPatchRecovered || state == modulehook.ConfigPatchActive {
		delete(m.hookStatuses[name], modulehook.ConfigPatchName)
		if len(m.hookStatuses[name]) == 0 {
			delete(m.hookStatuses, name)
		}
		return
	}
	if m.hookStatuses[name] == nil {
		m.hookStatuses[name] = map[string]modulehook.Status{}
	}
	m.hookStatuses[name][modulehook.ConfigPatchName] = cloneHookStatus(status)
}

func (m *Manager) workerGeneration(name string) int {
	m.mu.RLock()
	if supervisor := m.supervisors[name]; supervisor != nil && supervisor.AppliedGeneration() > 0 {
		generation := supervisor.AppliedGeneration()
		m.mu.RUnlock()
		return generation
	}
	defer m.mu.RUnlock()
	return m.workerGenerationLocked(name)
}

func (m *Manager) workerStatusLocked(name string) WorkerState {
	if status := m.statuses[name]; status != "" {
		return status
	}
	return WorkerStateConfigured
}

func (m *Manager) workerGenerationLocked(name string) int {
	if generation := m.generations[name]; generation > 0 {
		return generation
	}
	if _, ok := m.config.Workers[name]; ok {
		return 1
	}
	return 0
}

func (m *Manager) setWorkerGenerationLocked(name string, generation int) {
	if generation < 1 {
		generation = 1
	}
	m.generations[name] = generation
}

func (m *Manager) bumpWorkerGeneration(name string) {
	m.mu.Lock()
	next := m.workerGenerationLocked(name) + 1
	m.generations[name] = next
	m.supervisorFor(name).setAppliedGeneration(next)
	m.mu.Unlock()
}

func (m *Manager) StartWorker(name string) error {
	return m.startWorker(name, true)
}

func (m *Manager) startWorker(name string, resetRetries bool) error {
	if resetRetries {
		m.mu.Lock()
		m.retries[name] = 0
		delete(m.healthySince, name)
		m.supervisorFor(name).setRetryCount(0)
		m.supervisorFor(name).setHealthySince(time.Time{})
		m.mu.Unlock()
	}
	spawn, err := m.BuildWorkerSpawn(name)
	if err != nil {
		m.mu.Lock()
		m.supervisorFor(name).setStatus(WorkerStateFailed)
		m.statuses[name] = WorkerStateFailed
		m.mu.Unlock()
		m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": redactedErrorMessage(err)})
		return err
	}
	spawn.LogWriter = m.LogSink(name)
	if m.starter == nil {
		m.mu.Lock()
		m.supervisorFor(name).setStatus(WorkerStateRunning)
		m.supervisorFor(name).setAppliedGeneration(1)
		m.statuses[name] = WorkerStateRunning
		m.setWorkerGenerationLocked(name, 1)
		m.mu.Unlock()
		m.publishEvent(EventWorkerStarted, map[string]any{"worker": name, "status": string(WorkerStateRunning)})
		return nil
	}
	process, err := m.starter.Start(spawn)
	if err != nil {
		m.mu.Lock()
		m.supervisorFor(name).setStatus(WorkerStateFailed)
		m.statuses[name] = WorkerStateFailed
		m.mu.Unlock()
		m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": redactedErrorMessage(err)})
		return err
	}
	m.mu.Lock()
	m.processes[name] = process
	m.supervisorFor(name).setProcess(process)
	m.supervisorFor(name).setStatus(WorkerStateRunning)
	m.supervisorFor(name).setAppliedGeneration(1)
	m.statuses[name] = WorkerStateRunning
	m.setWorkerGenerationLocked(name, 1)
	m.mu.Unlock()
	m.publishEvent(EventWorkerStarted, map[string]any{"worker": name, "status": string(WorkerStateRunning)})
	return nil
}

func (m *Manager) LogSink(name string) *logging.WorkerLogSink {
	m.mu.RLock()
	if sink := m.logs[name]; sink != nil {
		sink.SetLevel(workerLogLevel(m.config.Workers[name]))
		m.mu.RUnlock()
		return sink
	}
	worker := m.config.Workers[name]
	logDir := m.config.Settings.LogDir
	m.mu.RUnlock()

	if logDir == "" {
		logDir = "~/.ainn/logs"
	}
	logDir = expandHomePath(logDir)
	sink, err := logging.NewWorkerLogSink(filepath.Join(logDir, fmt.Sprintf("worker-%d.log", worker.Port)), 1000)
	if err != nil {
		sink, _ = logging.NewWorkerLogSink(filepath.Join(os.TempDir(), fmt.Sprintf("ainn-worker-%d.log", worker.Port)), 1000)
	}
	if sink != nil {
		sink.SetLevel(workerLogLevel(worker))
	}

	m.mu.Lock()
	if existing := m.logs[name]; existing != nil {
		m.mu.Unlock()
		if sink != nil {
			_ = sink.Close()
		}
		return existing
	}
	m.logs[name] = sink
	m.mu.Unlock()
	return sink
}

func (m *Manager) StopWorker(name string) error {
	m.mu.Lock()
	process := m.processes[name]
	if process != nil {
		delete(m.processes, name)
		m.statuses[name] = WorkerStateStopping
		m.supervisorFor(name).setStatus(WorkerStateStopping)
		m.supervisorFor(name).clearProcess()
	}
	m.mu.Unlock()

	status, err := stopManagedProcess(process)
	if err != nil {
		m.mu.Lock()
		m.supervisorFor(name).setStatus(WorkerStateFailed)
		m.statuses[name] = WorkerStateFailed
		m.mu.Unlock()
		m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": redactedErrorMessage(err)})
		return err
	}
	m.mu.Lock()
	m.statuses[name] = status
	m.supervisorFor(name).setStatus(status)
	m.mu.Unlock()
	m.publishEvent(EventWorkerStopped, map[string]any{"worker": name, "status": string(status)})
	return nil
}

func (m *Manager) RestartWorker(name string) error {
	return m.restartWorker(name, true)
}

func (m *Manager) UpdateWorker(name string, current config.WorkerConfig, next config.WorkerConfig) error {
	if next.LogLevel == "" {
		next.LogLevel = "simple"
	}
	if sink := m.LogSink(name); sink != nil {
		sink.SetLevel(next.LogLevel)
	}
	wasRunning := m.workerStatus(name) == WorkerStateRunning
	if next.Port == current.Port {
		m.updateConfig(func(cfgRoot *config.Config) {
			cfgRoot.Workers[name] = next
		})
		m.publishWorkerUpdated(name, next)
		if wasRunning {
			return m.RestartWorker(name)
		}
		return nil
	}

	oldProcess := m.processForWorker(name)
	m.updateConfig(func(cfgRoot *config.Config) {
		cfgRoot.Workers[name] = next
	})
	if wasRunning {
		if err := m.startWorker(name, true); err != nil {
			m.updateConfig(func(cfgRoot *config.Config) {
				cfgRoot.Workers[name] = current
			})
			m.mu.Lock()
			if oldProcess != nil {
				m.processes[name] = oldProcess
				m.statuses[name] = WorkerStateRunning
				m.supervisorFor(name).setStatus(WorkerStateRunning)
				m.supervisorFor(name).setProcess(oldProcess)
			}
			m.mu.Unlock()
			return err
		}
		if err := m.waitForWorkerHealth(next.Port); err != nil {
			newProcess := m.processForWorker(name)
			_, _ = stopManagedProcess(newProcess)
			m.updateConfig(func(cfgRoot *config.Config) {
				cfgRoot.Workers[name] = current
			})
			m.mu.Lock()
			if oldProcess != nil {
				m.processes[name] = oldProcess
				m.statuses[name] = WorkerStateRunning
				m.supervisorFor(name).setStatus(WorkerStateRunning)
				m.supervisorFor(name).setProcess(oldProcess)
			} else {
				delete(m.processes, name)
				m.statuses[name] = WorkerStateFailed
				m.supervisorFor(name).setStatus(WorkerStateFailed)
			}
			m.mu.Unlock()
			if oldProcess == nil {
				m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": redactedErrorMessage(err)})
			}
			return err
		}
		m.publishWorkerUpdated(name, next)
		if _, err := stopManagedProcess(oldProcess); err != nil {
			m.mu.Lock()
			m.statuses[name] = WorkerStateFailed
			m.supervisorFor(name).setStatus(WorkerStateFailed)
			m.mu.Unlock()
			m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": redactedErrorMessage(err)})
			return err
		}
		m.mu.Lock()
		m.statuses[name] = WorkerStateRunning
		m.supervisorFor(name).setStatus(WorkerStateRunning)
		m.mu.Unlock()
		return nil
	}
	m.publishWorkerUpdated(name, next)
	return nil
}

func (m *Manager) publishWorkerUpdated(name string, worker config.WorkerConfig) {
	m.publishEvent(EventWorkerUpdated, map[string]any{
		"worker":    name,
		"port":      worker.Port,
		"role":      worker.Role,
		"upstream":  worker.Upstream,
		"log_level": workerLogLevel(worker),
		"modules":   cloneModules(worker.RequestModules),
		"hooks":     cloneModules(worker.Hooks),
	})
}

func (m *Manager) waitForWorkerHealth(port int) error {
	checker := m.healthChecker
	if checker == nil {
		checker = HTTPHealthChecker{Client: http.DefaultClient}
	}
	deadline := m.clock().Add(m.healthWait)
	for {
		if checker.Check(port) {
			return nil
		}
		if !m.clock().Before(deadline) {
			return fmt.Errorf("worker on port :%d did not become healthy", port)
		}
		time.Sleep(m.healthPoll)
	}
}

func (m *Manager) processForWorker(name string) ManagedProcess {
	m.mu.RLock()
	if supervisor := m.supervisors[name]; supervisor != nil {
		process := supervisor.Process()
		m.mu.RUnlock()
		return process
	}
	defer m.mu.RUnlock()
	return m.processes[name]
}

func stopManagedProcess(process ManagedProcess) (WorkerState, error) {
	if process == nil {
		return WorkerStateStopped, nil
	}
	if err := process.Stop(); err != nil {
		return WorkerStateFailed, err
	}
	if reporter, ok := process.(forcedStopReporter); ok && reporter.ForcedStop() {
		return WorkerStateStoppedForced, nil
	}
	return WorkerStateStopped, nil
}

func (m *Manager) restartWorker(name string, resetRetries bool) error {
	if err := m.StopWorker(name); err != nil {
		return err
	}
	if err := m.startWorker(name, resetRetries); err != nil {
		return err
	}
	m.publishEvent(EventWorkerRestarted, map[string]any{"worker": name, "status": string(WorkerStateRunning)})
	return nil
}

func (m *Manager) RecordHealth(name string, healthy bool) {
	if healthy {
		now := m.clock()
		m.mu.Lock()
		since, ok := m.healthySince[name]
		if !ok {
			since = now
			m.healthySince[name] = since
			m.supervisorFor(name).setHealthySince(since)
		}
		if now.Sub(since) >= healthyRetryResetWindow {
			m.retries[name] = 0
			m.supervisorFor(name).setRetryCount(0)
		}
		if m.workerStatusLocked(name) != WorkerStateStopped {
			m.statuses[name] = WorkerStateRunning
			m.supervisorFor(name).setStatus(WorkerStateRunning)
		}
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	delete(m.healthySince, name)
	m.supervisorFor(name).setHealthySince(time.Time{})
	if m.workerStatusLocked(name) == WorkerStateFailed {
		m.mu.Unlock()
		return
	}
	m.retries[name]++
	m.supervisorFor(name).setRetryCount(m.retries[name])
	if m.retries[name] >= 10 {
		m.statuses[name] = WorkerStateFailed
		m.supervisorFor(name).setStatus(WorkerStateFailed)
		m.mu.Unlock()
		m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": "worker health check failed"})
		return
	}
	m.statuses[name] = WorkerStateRestarting
	m.supervisorFor(name).setStatus(WorkerStateRestarting)
	m.mu.Unlock()

	if err := m.restartWorker(name, false); err != nil {
		m.mu.Lock()
		m.statuses[name] = WorkerStateFailed
		m.supervisorFor(name).setStatus(WorkerStateFailed)
		m.mu.Unlock()
		m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": redactedErrorMessage(err)})
	}
}

func (m *Manager) StartHealthMonitor(interval time.Duration) func() {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	checker := m.healthChecker
	if checker == nil {
		checker = HTTPHealthChecker{Client: http.DefaultClient}
	}
	done := make(chan struct{})
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				for _, target := range m.healthTargets() {
					m.RecordHealth(target.name, checker.Check(target.port))
				}
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

const defaultUpstreamProbeInterval = 1 * time.Minute

// StartUpstreamProber 启动后台 ticker 定期 probe 所有 upstream。
// interval <= 0 时使用 defaultUpstreamProbeInterval。启动时立即跑一次。
func (m *Manager) StartUpstreamProber(interval time.Duration) func() {
	if interval <= 0 {
		interval = defaultUpstreamProbeInterval
	}
	done := make(chan struct{})
	go func() {
		m.probeAllUpstreams(context.Background())
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.probeAllUpstreams(context.Background())
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

func (m *Manager) probeAllUpstreams(ctx context.Context) {
	profiles := m.upstreamProfileSnapshot()
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	var wg sync.WaitGroup
	for _, name := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			m.probeUpstreamByName(ctx, n)
		}(name)
	}
	wg.Wait()
}

type HTTPHealthChecker struct {
	Client *http.Client
}

func (c HTTPHealthChecker) Check(port int) bool {
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Get(fmt.Sprintf("http://%s:%d%s", constants.LocalhostAddr, port, constants.ProxyHealthPath))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (m *Manager) StartConfiguredWorkers() error {
	workers := m.workerConfigSnapshot()
	names := make([]string, 0, len(workers))
	for name := range workers {
		names = append(names, name)
	}
	sort.Strings(names)

	var errs []error
	for _, name := range names {
		state, detail, err := recoverConfigPatchBeforeWorkerStart(workers[name], name)
		m.setConfigPatchStatus(name, state, detail)
		if err != nil {
			m.mu.Lock()
			m.supervisorFor(name).setStatus(WorkerStateFailed)
			m.statuses[name] = WorkerStateFailed
			m.mu.Unlock()
			m.publishEvent(EventWorkerHealthChanged, map[string]any{"worker": name, "status": string(WorkerStateFailed), "error": redactedErrorMessage(err)})
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
	}
	for _, name := range names {
		if m.workerStatus(name) == WorkerStateFailed {
			continue
		}
		if err := m.StartWorker(name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func recoverConfigPatchBeforeWorkerStart(worker config.WorkerConfig, workerName string) (modulehook.ConfigPatchState, map[string]string, error) {
	moduleCfg, ok := worker.Hooks[modulehook.ConfigPatchName]
	if !ok || !moduleCfg.Enabled {
		return modulehook.ConfigPatchClean, nil, nil
	}
	patch := modulehook.NewConfigPatch(module.ModuleConfig{
		Enabled: moduleCfg.Enabled,
		Params:  cloneAnyMap(moduleCfg.Params),
	}, modulehook.BuildDependencies{
		WorkerID:   workerName,
		WorkerPort: worker.Port,
	})
	if err := patch.RecoverStaleJournal(); err != nil {
		return patch.State(), patch.Detail(), err
	}
	switch patch.State() {
	case modulehook.ConfigPatchUnresolved, modulehook.ConfigPatchFailed:
		return patch.State(), patch.Detail(), fmt.Errorf("config_patch recovery state %s must be resolved before enabling", patch.State())
	default:
		return patch.State(), patch.Detail(), nil
	}
}

func expandHomePath(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}

type healthTarget struct {
	name string
	port int
}

func (m *Manager) healthTargets() []healthTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()

	targets := make([]healthTarget, 0, len(m.config.Workers))
	for name, worker := range m.config.Workers {
		if m.workerStatusLocked(name) == WorkerStateRunning {
			targets = append(targets, healthTarget{name: name, port: worker.Port})
		}
	}
	return targets
}

func (m *Manager) workerConfigSnapshot() map[string]config.WorkerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	workers := make(map[string]config.WorkerConfig, len(m.config.Workers))
	for name, worker := range m.config.Workers {
		workers[name] = cloneWorkerConfig(worker)
	}
	return workers
}

func (m *Manager) upstreamProfileSnapshot() map[string]config.UpstreamProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()

	providers := make(map[string]config.UpstreamProfile, len(m.config.Upstreams))
	for name, profile := range m.config.Upstreams {
		providers[name] = profile
	}
	return providers
}

func (m *Manager) workerConfig(name string) (config.WorkerConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	worker, ok := m.config.Workers[name]
	if !ok {
		return config.WorkerConfig{}, false
	}
	return cloneWorkerConfig(worker), true
}

func (m *Manager) configuredConfigPath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.configPath
}

type liveWorkerTarget struct {
	name string
	port int
}

func (m *Manager) liveWorkersUsingUpstream(upstreamName string) []liveWorkerTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()

	targets := []liveWorkerTarget{}
	for workerName, worker := range m.config.Workers {
		if worker.Upstream != upstreamName || m.workerStatusLocked(workerName) != WorkerStateRunning {
			continue
		}
		targets = append(targets, liveWorkerTarget{name: workerName, port: worker.Port})
	}
	return targets
}

func cloneConfig(cfg config.Config) config.Config {
	out := config.Config{
		Settings:  cfg.Settings,
		Plugins:   make(map[string]config.PluginDefinition, len(cfg.Plugins)),
		Workers:   make(map[string]config.WorkerConfig, len(cfg.Workers)),
		Upstreams: make(map[string]config.UpstreamProfile, len(cfg.Upstreams)),
	}
	for name, plugin := range cfg.Plugins {
		out.Plugins[name] = plugin
	}
	for name, worker := range cfg.Workers {
		out.Workers[name] = cloneWorkerConfig(worker)
	}
	for name, profile := range cfg.Upstreams {
		out.Upstreams[name] = profile
	}
	return out
}

func buildPortIndex(workers map[string]config.WorkerConfig) map[int]string {
	out := make(map[int]string, len(workers))
	for name, worker := range workers {
		out[worker.Port] = name
	}
	return out
}

func cloneWorkerConfig(worker config.WorkerConfig) config.WorkerConfig {
	return config.WorkerConfig{
		Role:           worker.Role,
		Port:           worker.Port,
		Upstream:       worker.Upstream,
		LogLevel:       workerLogLevel(worker),
		RequestModules: cloneModules(worker.RequestModules),
		Hooks:          cloneModules(worker.Hooks),
	}
}

func cloneModules(modules map[string]config.ModuleConfig) map[string]config.ModuleConfig {
	out := make(map[string]config.ModuleConfig, len(modules))
	for name, module := range modules {
		out[name] = cloneModuleConfig(module)
	}
	return out
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneModuleConfig(module config.ModuleConfig) config.ModuleConfig {
	out := config.ModuleConfig{
		Enabled: module.Enabled,
	}
	if module.Params != nil {
		out.Params = make(map[string]any, len(module.Params))
		for key, value := range module.Params {
			out.Params[key] = value
		}
	}
	return out
}
