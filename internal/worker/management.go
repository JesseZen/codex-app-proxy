package worker

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jesse/agent-inn/internal/constants"
	"github.com/jesse/agent-inn/internal/module"
	"github.com/jesse/agent-inn/internal/modulehook"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
	"github.com/jesse/agent-inn/internal/upstream"
)

func (w *Worker) serveManagement(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path == constants.ProxyHealthPath && r.Method == http.MethodGet {
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"status": "ok",
			"uptime": time.Since(time.Now()).String(),
		})
		return
	}
	if r.URL.Path == constants.ProxyStatusPath && r.Method == http.MethodGet {
		w.writeStatus(rw)
		return
	}
	if r.URL.Path == constants.ProxyRuntimePath && r.Method == http.MethodPut {
		w.handleRuntime(rw, r)
		return
	}
	if r.URL.Path == constants.ProxySwitchPath && r.Method == http.MethodPost {
		w.handleSwitch(rw, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, constants.ProxyModulesPrefix) {
		w.handleModule(rw, r)
		return
	}
	http.NotFound(rw, r)
}

func (w *Worker) writeStatus(rw http.ResponseWriter) {
	snapshot := w.snapshots.Load()
	status := map[string]any{
		"snapshot_generation": snapshot.Generation,
		"upstream":            snapshot.Upstream.Redacted(),
		"modules":             snapshot.requestModuleStates(),
		"hooks":               snapshot.hookModules(),
	}
	if len(snapshot.HookStatuses) > 0 {
		status["hook_statuses"] = snapshot.HookStatuses
	}
	writeJSON(rw, http.StatusOK, status)
}

func (w *Worker) handleRuntime(rw http.ResponseWriter, r *http.Request) {
	var runtime appruntime.WorkerRuntime
	if err := json.NewDecoder(r.Body).Decode(&runtime); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	applied, err := w.UpdateRuntime(runtime)
	if err != nil {
		current := w.snapshots.Load()
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": err.Error(), "snapshot_generation": current.Generation})
		return
	}
	snapshot := w.snapshots.Load()
	writeJSON(rw, http.StatusOK, map[string]any{
		"applied_generation":  applied,
		"snapshot_generation": snapshot.Generation,
		"upstream":            snapshot.Upstream.Redacted(),
	})
}

func (w *Worker) handleSwitch(rw http.ResponseWriter, r *http.Request) {
	var payload struct {
		Upstream upstream.RuntimeUpstream `json:"upstream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}

	current := w.snapshots.Load()
	next, err := current.withUpstream(payload.Upstream)
	if err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": err.Error(), "snapshot_generation": current.Generation})
		return
	}
	next.Generation = current.Generation + 1
	if err := next.Validate(); err != nil {
		writeJSON(rw, http.StatusBadRequest, map[string]any{"error": err.Error(), "snapshot_generation": current.Generation})
		return
	}
	w.snapshots.Store(next)
	writeJSON(rw, http.StatusOK, map[string]any{
		"snapshot_generation": next.Generation,
		"upstream":            next.Upstream.Redacted(),
	})
}

func (w *Worker) handleModule(rw http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, constants.ProxyModulesPrefix)
	name, action, _ := strings.Cut(rest, "/")
	if name == "" {
		http.NotFound(rw, r)
		return
	}

	current := w.snapshots.Load()
	if modulehook.IsLifecycleHook(name) {
		cfg := current.hookModules()[name]
		if action == "" && r.Method == http.MethodGet {
			writeJSON(rw, http.StatusOK, cfg)
			return
		}
		http.NotFound(rw, r)
		return
	}
	plugin := current.Plugins[name]
	if !module.IsRequestMiddleware(name) && !(plugin.Source == "external" && plugin.Kind == "request_middleware") {
		http.NotFound(rw, r)
		return
	}
	configs := current.requestModules()
	if _, ok := configs[name]; !ok {
		http.NotFound(rw, r)
		return
	}

	if action == "" && r.Method == http.MethodGet {
		writeJSON(rw, http.StatusOK, current.requestModuleStates()[name])
		return
	}
	if action == "" && r.Method == http.MethodPatch {
		var nextCfg module.ModuleConfig
		if err := json.NewDecoder(r.Body).Decode(&nextCfg); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		nextConfigs := current.requestModules()
		nextConfigs[name] = module.CloneModuleConfig(nextCfg)
		next, err := current.withRequestModuleConfigs(nextConfigs)
		if err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		next.Generation = current.Generation + 1
		w.snapshots.Store(next)
		writeJSON(rw, http.StatusOK, map[string]any{
			"snapshot_generation": next.Generation,
			"module":              next.requestModuleStates()[name],
		})
		return
	}
	if action == "toggle" && r.Method == http.MethodPost {
		nextConfigs := current.requestModules()
		cfg := nextConfigs[name]
		cfg.Enabled = !cfg.Enabled
		nextConfigs[name] = cfg
		next, err := current.withRequestModuleConfigs(nextConfigs)
		if err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		next.Generation = current.Generation + 1
		w.snapshots.Store(next)
		writeJSON(rw, http.StatusOK, map[string]any{
			"snapshot_generation": next.Generation,
			"module":              next.requestModuleStates()[name],
		})
		return
	}
	http.NotFound(rw, r)
}

func moduleStates(modules []module.Middleware) map[string]module.ModuleConfig {
	out := map[string]module.ModuleConfig{}
	for _, middleware := range modules {
		out[middleware.Name()] = middleware.Config()
	}
	return out
}

func writeJSON(rw http.ResponseWriter, status int, value any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(value)
}
