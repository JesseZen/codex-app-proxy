package manager

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/jesse/codex-app-proxy/internal/config"
	"github.com/jesse/codex-app-proxy/internal/upstream"
)

func (m *Manager) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/events", m.handleEvents)
	mux.HandleFunc("/api/workers", m.handleWorkers)
	mux.HandleFunc("/api/workers/", m.handleWorkerByPort)
	mux.HandleFunc("/api/upstreams", m.handleUpstreams)
	mux.HandleFunc("/api/upstreams/", m.handleUpstreamByName)
	mux.HandleFunc("/api/config", m.handleConfig)
}

func (m *Manager) handleWorkers(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(rw, http.StatusOK, map[string]any{"workers": m.workerSummaries()})
		return
	}
	if r.Method == http.MethodPost {
		m.handleCreateWorker(rw, r)
		return
	}
	http.NotFound(rw, r)
}

func (m *Manager) handleCreateWorker(rw http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name     string                         `json:"name"`
		Port     int                            `json:"port"`
		Upstream string                         `json:"upstream"`
		Modules  map[string]config.ModuleConfig `json:"modules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	payload.Name = strings.TrimSpace(payload.Name)
	payload.Upstream = strings.TrimSpace(payload.Upstream)
	if payload.Name == "" || strings.Contains(payload.Name, "/") {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "worker name is required"})
		return
	}
	if payload.Port <= 0 {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "worker port is required"})
		return
	}
	if payload.Upstream == "" {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "worker provider is required"})
		return
	}
	if _, err := m.resolveUpstream(payload.Upstream); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	if _, ok := m.workerConfig(payload.Name); ok {
		writeJSON(rw, http.StatusConflict, map[string]any{"error": "worker already exists"})
		return
	}
	if err := m.CheckPortAvailable(payload.Name, payload.Port); err != nil {
		writeJSON(rw, http.StatusConflict, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	worker := config.WorkerConfig{
		Port:     payload.Port,
		Upstream: payload.Upstream,
		Modules:  payload.Modules,
	}
	if worker.Modules == nil {
		worker.Modules = map[string]config.ModuleConfig{}
	}
	m.updateConfig(func(cfgRoot *config.Config) {
		cfgRoot.Workers[payload.Name] = worker
	})
	if err := m.StartWorker(payload.Name); err != nil {
		m.updateConfig(func(cfgRoot *config.Config) {
			delete(cfgRoot.Workers, payload.Name)
		})
		writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	for _, summary := range m.workerSummaries() {
		if summary.Name == payload.Name {
			writeJSON(rw, http.StatusCreated, summary)
			return
		}
	}
	writeJSON(rw, http.StatusCreated, map[string]any{"name": payload.Name, "port": payload.Port, "status": string(m.workerStatus(payload.Name))})
}

func (m *Manager) handleWorkerByPort(rw http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/workers/")
	parts := strings.Split(rest, "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		port, err := strconv.Atoi(parts[0])
		if err != nil {
			http.NotFound(rw, r)
			return
		}
		workerName, worker, ok := m.workerByPort(port)
		if !ok {
			http.NotFound(rw, r)
			return
		}
		writeJSON(rw, http.StatusOK, m.workerDetail(workerName, worker))
		return
	}
	if len(parts) == 1 && r.Method == http.MethodDelete {
		port, err := strconv.Atoi(parts[0])
		if err != nil {
			http.NotFound(rw, r)
			return
		}
		workerName, _, ok := m.workerByPort(port)
		if !ok {
			http.NotFound(rw, r)
			return
		}
		if err := m.StopWorker(workerName); err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"worker": workerName, "status": string(m.workerStatus(workerName))})
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPatch {
		port, err := strconv.Atoi(parts[0])
		if err != nil {
			http.NotFound(rw, r)
			return
		}
		workerName, current, ok := m.workerByPort(port)
		if !ok {
			http.NotFound(rw, r)
			return
		}
		var next config.WorkerConfig
		if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		if next.Port <= 0 {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "worker port is required"})
			return
		}
		next.Upstream = strings.TrimSpace(next.Upstream)
		if next.Upstream == "" {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "worker provider is required"})
			return
		}
		if next.Modules == nil {
			next.Modules = map[string]config.ModuleConfig{}
		}
		if next.LogLevel == "" {
			next.LogLevel = "simple"
		}
		if !validWorkerLogLevel(next.LogLevel) {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "worker log_level must be simple or detail"})
			return
		}
		if _, err := m.resolveUpstream(next.Upstream); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		if err := m.CheckPortAvailable(workerName, next.Port); err != nil {
			writeJSON(rw, http.StatusConflict, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		if err := m.UpdateWorker(workerName, current, next); err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		for _, summary := range m.workerSummaries() {
			if summary.Name == workerName {
				writeJSON(rw, http.StatusOK, summary)
				return
			}
		}
		writeJSON(rw, http.StatusOK, map[string]any{"worker": workerName, "status": string(m.workerStatus(workerName))})
		return
	}
	if len(parts) == 2 && parts[1] == "restart" && r.Method == http.MethodPost {
		port, err := strconv.Atoi(parts[0])
		if err != nil {
			http.NotFound(rw, r)
			return
		}
		workerName, _, ok := m.workerByPort(port)
		if !ok {
			http.NotFound(rw, r)
			return
		}
		if err := m.RestartWorker(workerName); err != nil {
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"worker": workerName, "status": string(m.workerStatus(workerName))})
		return
	}
	if len(parts) == 2 && parts[1] == "stream" && r.Method == http.MethodGet {
		port, err := strconv.Atoi(parts[0])
		if err != nil {
			http.NotFound(rw, r)
			return
		}
		workerName, _, ok := m.workerByPort(port)
		if !ok {
			http.NotFound(rw, r)
			return
		}
		m.handleWorkerStream(rw, r, workerName)
		return
	}
	if len(parts) == 2 && parts[1] == "logs" && r.Method == http.MethodGet {
		port, err := strconv.Atoi(parts[0])
		if err != nil {
			http.NotFound(rw, r)
			return
		}
		workerName, _, ok := m.workerByPort(port)
		if !ok {
			http.NotFound(rw, r)
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"lines": m.LogSink(workerName).Lines()})
		return
	}
	if len(parts) == 3 && parts[1] == "modules" && r.Method == http.MethodPatch {
		port, err := strconv.Atoi(parts[0])
		if err != nil {
			http.NotFound(rw, r)
			return
		}
		moduleName := parts[2]
		workerName, worker, ok := m.workerByPort(port)
		if !ok {
			http.NotFound(rw, r)
			return
		}
		var cfg config.ModuleConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		if worker.Modules == nil {
			worker.Modules = map[string]config.ModuleConfig{}
		}
		if cfg.Enabled {
			if err := m.validateConfigPatchOwner(workerName, moduleName); err != nil {
				writeJSON(rw, http.StatusConflict, map[string]any{"error": redactedErrorMessage(err)})
				return
			}
		}
		if err := m.patchLiveWorkerModule(workerName, port, moduleName, cfg); err != nil {
			writeJSON(rw, http.StatusBadGateway, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
		m.updateConfig(func(cfgRoot *config.Config) {
			worker.Modules[moduleName] = cfg
			cfgRoot.Workers[workerName] = worker
		})
		if m.workerStatus(workerName) == WorkerStateRunning {
			m.bumpWorkerGeneration(workerName)
		}
		m.publishEvent(EventModuleUpdated, map[string]any{"worker": workerName, "port": port, "module": moduleName, "enabled": cfg.Enabled, "params": cfg.Params})
		writeJSON(rw, http.StatusOK, map[string]any{
			"worker": workerName,
			"port":   port,
			"module": map[string]any{
				"name":    moduleName,
				"enabled": cfg.Enabled,
				"params":  cfg.Params,
			},
		})
		return
	}
	if len(parts) != 4 || parts[1] != "modules" || parts[3] != "toggle" || r.Method != http.MethodPost {
		http.NotFound(rw, r)
		return
	}
	port, err := strconv.Atoi(parts[0])
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	moduleName := parts[2]
	workerName, worker, ok := m.workerByPort(port)
	if !ok {
		http.NotFound(rw, r)
		return
	}
	if worker.Modules == nil {
		worker.Modules = map[string]config.ModuleConfig{}
	}
	cfg := worker.Modules[moduleName]
	cfg.Enabled = !cfg.Enabled
	if cfg.Enabled {
		if err := m.validateConfigPatchOwner(workerName, moduleName); err != nil {
			writeJSON(rw, http.StatusConflict, map[string]any{"error": redactedErrorMessage(err)})
			return
		}
	}
	if err := m.toggleLiveWorkerModule(workerName, port, moduleName); err != nil {
		writeJSON(rw, http.StatusBadGateway, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	m.updateConfig(func(cfgRoot *config.Config) {
		worker.Modules[moduleName] = cfg
		cfgRoot.Workers[workerName] = worker
	})
	if m.workerStatus(workerName) == WorkerStateRunning {
		m.bumpWorkerGeneration(workerName)
	}
	m.publishEvent(EventModuleUpdated, map[string]any{"worker": workerName, "port": port, "module": moduleName, "enabled": cfg.Enabled, "params": cfg.Params})
	writeJSON(rw, http.StatusOK, map[string]any{
		"worker": workerName,
		"port":   port,
		"module": map[string]any{
			"name":    moduleName,
			"enabled": cfg.Enabled,
		},
	})
}

func (m *Manager) validateConfigPatchOwner(workerName string, moduleName string) error {
	if moduleName != "config_patch" {
		return nil
	}
	if err := m.validateConfigPatchRecoveryState(workerName); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for otherName, worker := range m.config.Workers {
		if otherName == workerName || m.workerStatusLocked(otherName) != WorkerStateRunning {
			continue
		}
		if worker.Modules["config_patch"].Enabled {
			return configPatchAlreadyActiveError{}
		}
	}
	return nil
}

func (m *Manager) validateConfigPatchRecoveryState(workerName string) error {
	switch state := m.configPatchState(workerName); state {
	case "unresolved", "failed":
		return configPatchRecoveryStateError{state: state}
	}
	worker, ok := m.workerConfig(workerName)
	if !ok || m.workerStatus(workerName) != WorkerStateRunning {
		return nil
	}
	client := m.workerClient
	if client == nil {
		client = HTTPWorkerClient{Client: http.DefaultClient}
	}
	status, err := client.GetStatus(worker.Port)
	if err != nil {
		return nil
	}
	switch status.ConfigPatchState {
	case "unresolved", "failed":
		return configPatchRecoveryStateError{state: status.ConfigPatchState}
	default:
		return nil
	}
}

type configPatchAlreadyActiveError struct{}

func (configPatchAlreadyActiveError) Error() string {
	return "config_patch already active on another worker"
}

type configPatchRecoveryStateError struct {
	state string
}

func (e configPatchRecoveryStateError) Error() string {
	return "config_patch recovery state " + e.state + " must be resolved before enabling"
}

func validWorkerLogLevel(level string) bool {
	return level == "simple" || level == "detail"
}

func (m *Manager) patchLiveWorkerModule(workerName string, port int, moduleName string, cfg config.ModuleConfig) error {
	if m.workerStatus(workerName) != WorkerStateRunning {
		return nil
	}
	client := m.workerClient
	if client == nil {
		client = HTTPWorkerClient{Client: http.DefaultClient}
	}
	return client.PatchModule(port, moduleName, cfg)
}

func (m *Manager) toggleLiveWorkerModule(workerName string, port int, moduleName string) error {
	if m.workerStatus(workerName) != WorkerStateRunning {
		return nil
	}
	client := m.workerClient
	if client == nil {
		client = HTTPWorkerClient{Client: http.DefaultClient}
	}
	return client.ToggleModule(port, moduleName)
}

func (m *Manager) handleUpstreams(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(rw, r)
		return
	}
	out := map[string]any{}
	for name, profile := range m.upstreamProfileSnapshot() {
		runtime, _ := upstream.Resolve(name, profile)
		out[name] = map[string]any{
			"name":        name,
			"base_url":    profile.BaseURL,
			"has_api_key": runtime.APIKey != "",
			"api_format":  profile.APIFormat,
		}
	}
	writeJSON(rw, http.StatusOK, map[string]any{"providers": out})
}

func (m *Manager) handleUpstreamByName(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.NotFound(rw, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/upstreams/")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(rw, r)
		return
	}
	type upstreamPatch struct {
		BaseURL   *string `json:"base_url,omitempty"`
		APIKey    *string `json:"api_key,omitempty"`
		APIFormat *string `json:"api_format,omitempty"`
	}
	var patch upstreamPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	current, _ := m.upstreamProfileSnapshot()[name]
	profile := current
	if patch.BaseURL != nil {
		profile.BaseURL = *patch.BaseURL
	}
	if patch.APIKey != nil {
		profile.APIKey = *patch.APIKey
	}
	if patch.APIFormat != nil {
		profile.APIFormat = *patch.APIFormat
	}
	runtime, err := upstream.Resolve(name, profile)
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	if err := m.switchLiveWorkersUsingUpstream(name, runtime); err != nil {
		writeJSON(rw, http.StatusBadGateway, map[string]any{"error": redactedErrorMessage(err)})
		return
	}
	m.updateConfig(func(cfgRoot *config.Config) {
		cfgRoot.Upstreams[name] = profile
	})
	m.bumpLiveWorkersUsingUpstream(name)
	m.publishEvent(EventUpstreamUpdated, map[string]any{"upstream": name})
	writeJSON(rw, http.StatusOK, map[string]any{
		"name":        name,
		"base_url":    profile.BaseURL,
		"has_api_key": runtime.APIKey != "",
		"api_format":  profile.APIFormat,
	})
}

func (m *Manager) switchLiveWorkersUsingUpstream(upstreamName string, runtime upstream.RuntimeUpstream) error {
	client := m.workerClient
	if client == nil {
		client = HTTPWorkerClient{Client: http.DefaultClient}
	}
	for _, target := range m.liveWorkersUsingUpstream(upstreamName) {
		if err := client.SwitchUpstream(target.port, runtime); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) bumpLiveWorkersUsingUpstream(upstreamName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for workerName, worker := range m.config.Workers {
		if worker.Upstream == upstreamName && m.workerStatusLocked(workerName) == WorkerStateRunning {
			m.generations[workerName] = m.workerGenerationLocked(workerName) + 1
		}
	}
}

func (m *Manager) handleConfig(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		if m.configuredConfigPath() == "" {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "config path is required"})
			return
		}
		if err := m.store.Save(); err != nil {
			status := m.syncConfigStatusFromStore()
			writeJSON(rw, http.StatusInternalServerError, map[string]any{"error": redactedErrorMessage(err), "status": status})
			return
		}
		_, status := m.syncConfigFromStore()
		writeJSON(rw, http.StatusOK, map[string]any{"status": status})
		return
	}
	if r.Method != http.MethodGet {
		http.NotFound(rw, r)
		return
	}
	cfg, status := m.syncConfigFromStore()
	writeJSON(rw, http.StatusOK, map[string]any{
		"config": sanitizeConfig(cfg),
		"status": map[string]any{
			"generation":      status.Generation,
			"dirty":           status.Dirty,
			"last_save_error": status.LastSaveError,
		},
	})
}

func sanitizeConfig(cfg configLike) any {
	return cfg
}

type configLike interface{}

func writeJSON(rw http.ResponseWriter, status int, value any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	encoder := json.NewEncoder(rw)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func redactedErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return strings.ReplaceAll(err.Error(), "\n", " ")
}
