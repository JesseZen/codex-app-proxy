package worker

import (
	"os"

	"github.com/jesse/codex-app-proxy/internal/module"
	appruntime "github.com/jesse/codex-app-proxy/internal/runtime"
)

var runtimeModuleNames = []string{"image_filter", "api_translate", "model_override", "request_log", "debug_sse"}

func buildRuntimeModules(configs map[string]appruntime.ModuleConfig, apiFormat appruntime.APIFormat) []module.Middleware {
	modules := make([]module.Middleware, 0, len(runtimeModuleNames))
	for _, name := range runtimeModuleNames {
		runtimeCfg := configs[name]
		cfg := module.ModuleConfig{Enabled: runtimeCfg.Enabled}
		if runtimeCfg.Params != nil {
			cfg.Params = make(map[string]any, len(runtimeCfg.Params))
			for key, value := range runtimeCfg.Params {
				cfg.Params[key] = value
			}
		}
		if name == "api_translate" && cfg.Params == nil && apiFormat != "" {
			cfg.Params = map[string]any{"api_format": string(apiFormat)}
		}
		if name == "api_translate" && cfg.Params != nil && cfg.Params["api_format"] == nil && apiFormat != "" {
			cfg.Params["api_format"] = string(apiFormat)
		}
		switch name {
		case "image_filter":
			modules = append(modules, module.NewImageFilter(cfg))
		case "api_translate":
			modules = append(modules, module.NewAPITranslate(cfg))
		case "model_override":
			modules = append(modules, module.NewModelOverride(cfg))
		case "request_log":
			modules = append(modules, module.NewRequestLog(cfg, os.Stderr))
		case "debug_sse":
			modules = append(modules, module.NewDebugSSE(cfg, os.Stderr))
		}
	}
	return modules
}
