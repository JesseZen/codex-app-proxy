package worker

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jesse/codex-app-proxy/internal/constants"
	"github.com/jesse/codex-app-proxy/internal/module"
	"github.com/jesse/codex-app-proxy/internal/upstream"
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
		"modules":             moduleStates(snapshot.Modules),
	}
	if snapshot.ConfigPatchState != "" && snapshot.ConfigPatchState != module.ConfigPatchClean {
		status["config_patch_state"] = snapshot.ConfigPatchState
	}
	if len(snapshot.ConfigPatchDetail) > 0 {
		status["config_patch_detail"] = snapshot.ConfigPatchDetail
	}
	writeJSON(rw, http.StatusOK, status)
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
	next := current
	next.Generation = current.Generation + 1
	next.Upstream = payload.Upstream
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
	index := -1
	for i, middleware := range current.Modules {
		if middleware.Name() == name {
			index = i
			break
		}
	}
	if index == -1 {
		http.NotFound(rw, r)
		return
	}

	if action == "" && r.Method == http.MethodGet {
		writeJSON(rw, http.StatusOK, current.Modules[index].Config())
		return
	}
	if action == "" && r.Method == http.MethodPatch {
		var cfg module.ModuleConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		next := cloneSnapshotWithModules(current)
		if err := next.Modules[index].UpdateConfig(cfg); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		next.Generation = current.Generation + 1
		w.snapshots.Store(next)
		writeJSON(rw, http.StatusOK, map[string]any{
			"snapshot_generation": next.Generation,
			"module":              next.Modules[index].Config(),
		})
		return
	}
	if action == "toggle" && r.Method == http.MethodPost {
		next := cloneSnapshotWithModules(current)
		cfg := next.Modules[index].Config()
		cfg.Enabled = !cfg.Enabled
		if err := next.Modules[index].UpdateConfig(cfg); err != nil {
			writeJSON(rw, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		next.Generation = current.Generation + 1
		w.snapshots.Store(next)
		writeJSON(rw, http.StatusOK, map[string]any{
			"snapshot_generation": next.Generation,
			"module":              next.Modules[index].Config(),
		})
		return
	}
	http.NotFound(rw, r)
}

func cloneSnapshotWithModules(snapshot RuntimeConfigSnapshot) RuntimeConfigSnapshot {
	next := snapshot
	if snapshot.Modules != nil {
		next.Modules = make([]module.Middleware, len(snapshot.Modules))
		for i, middleware := range snapshot.Modules {
			next.Modules[i] = module.CloneMiddleware(middleware)
		}
	}
	return next
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
