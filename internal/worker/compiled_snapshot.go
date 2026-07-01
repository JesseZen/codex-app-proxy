package worker

import (
	"os"

	"github.com/jesse/agent-inn/internal/module"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
)

func buildRuntimeModules(configs map[string]module.ModuleConfig, plugins map[string]appruntime.PluginRuntime, apiFormat appruntime.APIFormat) ([]module.Middleware, map[string]module.ModuleConfig, error) {
	externalRequest := map[string]module.ExternalRequestRuntime{}
	for name, plugin := range plugins {
		if plugin.Source == "external" && plugin.Kind == "request_middleware" {
			externalRequest[name] = module.ExternalRequestRuntime{
				Command:         plugin.Command,
				Args:            append([]string(nil), plugin.Args...),
				ProtocolVersion: plugin.ProtocolVersion,
			}
		}
	}
	return module.BuildRequestMiddlewares(configs, module.BuildDependencies{
		APIFormat:       string(apiFormat),
		Stderr:          os.Stderr,
		ExternalRequest: externalRequest,
	})
}

func cloneRuntimeParams(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	cloned := make(map[string]any, len(params))
	for key, value := range params {
		cloned[key] = value
	}
	return cloned
}
