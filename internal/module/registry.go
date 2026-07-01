package module

import (
	"fmt"
	"io"
	"sort"
)

type BuildDependencies struct {
	APIFormat       string
	Stderr          io.Writer
	ExternalRequest map[string]ExternalRequestRuntime
}

type requestMiddlewareDefinition struct {
	name      string
	normalize func(ModuleConfig, BuildDependencies) ModuleConfig
	build     func(ModuleConfig, BuildDependencies) Middleware
}

var requestMiddlewareDefinitions = []requestMiddlewareDefinition{
	{
		name: "image_filter",
		build: func(cfg ModuleConfig, _ BuildDependencies) Middleware {
			return NewImageFilter(cfg)
		},
	},
	{
		name: "debug_sse",
		build: func(cfg ModuleConfig, deps BuildDependencies) Middleware {
			return NewDebugSSE(cfg, deps.Stderr)
		},
	},
	{
		name: "api_translate",
		normalize: func(cfg ModuleConfig, deps BuildDependencies) ModuleConfig {
			if cfg.Params == nil {
				cfg.Params = map[string]any{}
			}
			if cfg.Params["api_format"] == nil && deps.APIFormat != "" {
				cfg.Params["api_format"] = deps.APIFormat
			}
			return cfg
		},
		build: func(cfg ModuleConfig, _ BuildDependencies) Middleware {
			return NewAPITranslate(cfg)
		},
	},
	{
		name: "model_override",
		build: func(cfg ModuleConfig, _ BuildDependencies) Middleware {
			return NewModelOverride(cfg)
		},
	},
	{
		name: "request_log",
		build: func(cfg ModuleConfig, deps BuildDependencies) Middleware {
			return NewRequestLog(cfg, deps.Stderr)
		},
	},
}

func RequestMiddlewareNames() []string {
	names := make([]string, len(requestMiddlewareDefinitions))
	for i, definition := range requestMiddlewareDefinitions {
		names[i] = definition.name
	}
	return names
}

func IsRequestMiddleware(name string) bool {
	for _, definition := range requestMiddlewareDefinitions {
		if definition.name == name {
			return true
		}
	}
	return false
}

func BuildRequestMiddlewares(configs map[string]ModuleConfig, deps BuildDependencies) ([]Middleware, map[string]ModuleConfig, error) {
	if deps.Stderr == nil {
		deps.Stderr = io.Discard
	}
	for name := range configs {
		if !IsRequestMiddleware(name) && deps.ExternalRequest[name].Command == "" {
			return nil, nil, fmt.Errorf("unknown request middleware %q", name)
		}
	}
	modules := make([]Middleware, 0, len(requestMiddlewareDefinitions))
	normalized := make(map[string]ModuleConfig, len(requestMiddlewareDefinitions)+len(deps.ExternalRequest))
	for _, definition := range requestMiddlewareDefinitions {
		cfg := CloneModuleConfig(configs[definition.name])
		if definition.normalize != nil {
			cfg = definition.normalize(cfg, deps)
		}
		normalized[definition.name] = cfg
		modules = append(modules, definition.build(cfg, deps))
	}
	externalNames := make([]string, 0, len(deps.ExternalRequest))
	for name := range deps.ExternalRequest {
		if _, ok := configs[name]; ok {
			externalNames = append(externalNames, name)
		}
	}
	sort.Strings(externalNames)
	for _, name := range externalNames {
		cfg := CloneModuleConfig(configs[name])
		normalized[name] = cfg
		modules = append(modules, NewExternalRequestMiddleware(name, cfg, deps.ExternalRequest[name]))
	}
	return modules, normalized, nil
}

func CloneModuleConfig(cfg ModuleConfig) ModuleConfig {
	cloned := ModuleConfig{Enabled: cfg.Enabled}
	if cfg.Params != nil {
		cloned.Params = make(map[string]any, len(cfg.Params))
		for key, value := range cfg.Params {
			cloned.Params[key] = value
		}
	}
	return cloned
}

func CloneModuleConfigs(configs map[string]ModuleConfig) map[string]ModuleConfig {
	out := make(map[string]ModuleConfig, len(configs))
	for name, cfg := range configs {
		out[name] = CloneModuleConfig(cfg)
	}
	return out
}
