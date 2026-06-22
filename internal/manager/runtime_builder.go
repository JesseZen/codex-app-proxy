package manager

import (
	"fmt"

	"github.com/jesse/codex-app-proxy/internal/config"
	appruntime "github.com/jesse/codex-app-proxy/internal/runtime"
	"github.com/jesse/codex-app-proxy/internal/upstream"
)

type RuntimeBuilder struct{}

func (RuntimeBuilder) Build(cfg config.Config, workerName string, generation appruntime.Generation) (appruntime.WorkerRuntime, error) {
	cfg.ApplyDefaults()
	worker, ok := cfg.Workers[workerName]
	if !ok {
		return appruntime.WorkerRuntime{}, fmt.Errorf("worker %q not found", workerName)
	}
	profile, ok := cfg.Upstreams[worker.Upstream]
	if !ok {
		return appruntime.WorkerRuntime{}, fmt.Errorf("upstream %q not found", worker.Upstream)
	}
	resolved, err := upstream.ResolveRuntime(worker.Upstream, profile)
	if err != nil {
		return appruntime.WorkerRuntime{}, err
	}

	modules := map[string]appruntime.ModuleConfig{}
	for name, module := range worker.Modules {
		next := appruntime.ModuleConfig{Enabled: module.Enabled}
		if module.Params != nil {
			next.Params = make(map[string]any, len(module.Params))
			for key, value := range module.Params {
				next.Params[key] = value
			}
		}
		modules[name] = next
	}
	if module, ok := modules["api_translate"]; ok && module.Enabled {
		if module.Params == nil {
			module.Params = map[string]any{}
		}
		if module.Params["api_format"] == nil && resolved.APIFormat != "" {
			module.Params["api_format"] = string(resolved.APIFormat)
		}
		modules["api_translate"] = module
	}

	return appruntime.WorkerRuntime{
		ID:         appruntime.WorkerID(workerName),
		Generation: generation,
		ListenPort: worker.Port,
		Role:       appruntime.WorkerRole(worker.Role),
		LogLevel:   appruntime.LogLevel(workerLogLevel(worker)),
		Upstream:   resolved,
		Modules:    modules,
	}, nil
}
