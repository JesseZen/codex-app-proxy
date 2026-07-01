package manager

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jesse/agent-inn/internal/config"
	"github.com/jesse/agent-inn/internal/module"
	"github.com/jesse/agent-inn/internal/modulehook"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
	"github.com/jesse/agent-inn/internal/upstream"
	"gopkg.in/yaml.v3"
)

type RuntimeBuilder struct{}

type externalPluginManifest struct {
	Name            string   `yaml:"name" json:"name"`
	Kind            string   `yaml:"kind" json:"kind"`
	Version         string   `yaml:"version" json:"version"`
	ProtocolVersion string   `yaml:"protocol_version" json:"protocol_version"`
	Command         string   `yaml:"command,omitempty" json:"command,omitempty"`
	Args            []string `yaml:"args,omitempty" json:"args,omitempty"`
	Path            string   `yaml:"path,omitempty" json:"path,omitempty"`
}

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
	hooks := map[string]appruntime.ModuleConfig{}
	plugins := map[string]appruntime.PluginRuntime{}
	for name, moduleCfg := range worker.RequestModules {
		definition, ok := cfg.Plugins[name]
		if !ok {
			return appruntime.WorkerRuntime{}, fmt.Errorf("worker %q references undefined plugin %q", workerName, name)
		}
		pluginRuntime, err := validateWorkerPluginBinding(workerName, name, definition, config.PluginKindRequestMiddleware)
		if err != nil {
			return appruntime.WorkerRuntime{}, err
		}
		if definition.Source == config.PluginSourceBuiltin && !module.IsRequestMiddleware(name) {
			return appruntime.WorkerRuntime{}, fmt.Errorf("builtin request middleware plugin %q is not registered", name)
		}
		if definition.Source == config.PluginSourceExternal {
			plugins[name] = pluginRuntime
		}
		modules[name] = runtimeModuleConfig(moduleCfg)
	}
	for name, moduleCfg := range worker.Hooks {
		definition, ok := cfg.Plugins[name]
		if !ok {
			return appruntime.WorkerRuntime{}, fmt.Errorf("worker %q references undefined plugin %q", workerName, name)
		}
		pluginRuntime, err := validateWorkerPluginBinding(workerName, name, definition, config.PluginKindLifecycleHook)
		if err != nil {
			return appruntime.WorkerRuntime{}, err
		}
		if definition.Source == config.PluginSourceBuiltin && !modulehook.IsLifecycleHook(name) {
			return appruntime.WorkerRuntime{}, fmt.Errorf("builtin lifecycle hook plugin %q is not registered", name)
		}
		if definition.Source == config.PluginSourceExternal {
			plugins[name] = pluginRuntime
		}
		hooks[name] = runtimeModuleConfig(moduleCfg)
	}
	runtime := appruntime.WorkerRuntime{
		ID:         appruntime.WorkerID(workerName),
		Generation: generation,
		ListenPort: worker.Port,
		Role:       appruntime.WorkerRole(worker.Role),
		LogLevel:   appruntime.LogLevel(workerLogLevel(worker)),
		Upstream:   resolved,
		Modules:    modules,
		Hooks:      hooks,
	}
	if len(plugins) > 0 {
		runtime.Plugins = plugins
	}
	return runtime, nil
}

func validateWorkerPluginBinding(workerName string, pluginName string, definition config.PluginDefinition, bindingKind string) (appruntime.PluginRuntime, error) {
	if definition.Kind != config.PluginKindRequestMiddleware && definition.Kind != config.PluginKindLifecycleHook {
		return appruntime.PluginRuntime{}, fmt.Errorf("plugin %q has invalid kind %q", pluginName, definition.Kind)
	}
	if definition.Source != config.PluginSourceBuiltin && definition.Source != config.PluginSourceExternal {
		return appruntime.PluginRuntime{}, fmt.Errorf("plugin %q has invalid source %q", pluginName, definition.Source)
	}
	if definition.Kind != bindingKind {
		return appruntime.PluginRuntime{}, fmt.Errorf("worker %q binds plugin %q as %s but plugin kind is %s", workerName, pluginName, bindingKind, definition.Kind)
	}
	if definition.Source == config.PluginSourceExternal && definition.Path == "" {
		return appruntime.PluginRuntime{}, fmt.Errorf("external plugin %q requires path", pluginName)
	}
	pluginRuntime := appruntime.PluginRuntime{
		Kind:   definition.Kind,
		Source: definition.Source,
		Path:   definition.Path,
	}
	if definition.Source == config.PluginSourceExternal {
		manifestPath := expandHomePath(definition.Path)
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return appruntime.PluginRuntime{}, fmt.Errorf("external plugin %q manifest is not readable", pluginName)
		}
		var manifest externalPluginManifest
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			return appruntime.PluginRuntime{}, fmt.Errorf("external plugin %q manifest is invalid", pluginName)
		}
		if manifest.Name != pluginName {
			return appruntime.PluginRuntime{}, fmt.Errorf("external plugin %q manifest name is %q", pluginName, manifest.Name)
		}
		if manifest.Kind != config.PluginKindRequestMiddleware && manifest.Kind != config.PluginKindLifecycleHook {
			return appruntime.PluginRuntime{}, fmt.Errorf("external plugin %q manifest has invalid kind %q", pluginName, manifest.Kind)
		}
		if manifest.Kind != definition.Kind {
			return appruntime.PluginRuntime{}, fmt.Errorf("external plugin %q manifest kind is %s", pluginName, manifest.Kind)
		}
		if manifest.Version == "" {
			return appruntime.PluginRuntime{}, fmt.Errorf("external plugin %q manifest requires version", pluginName)
		}
		if manifest.ProtocolVersion == "" {
			return appruntime.PluginRuntime{}, fmt.Errorf("external plugin %q manifest requires protocol_version", pluginName)
		}
		if manifest.ProtocolVersion != "1" {
			return appruntime.PluginRuntime{}, fmt.Errorf("external plugin %q manifest has unsupported protocol_version %q", pluginName, manifest.ProtocolVersion)
		}
		executable := manifest.Command
		if executable == "" {
			executable = manifest.Path
		}
		if executable == "" {
			return appruntime.PluginRuntime{}, fmt.Errorf("external plugin %q manifest requires command or path", pluginName)
		}
		if filepath.IsAbs(executable) || strings.ContainsRune(executable, os.PathSeparator) {
			executablePath := executable
			if !filepath.IsAbs(executablePath) {
				executablePath = filepath.Join(filepath.Dir(manifestPath), executablePath)
			}
			info, err := os.Stat(executablePath)
			if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
				return appruntime.PluginRuntime{}, fmt.Errorf("external plugin %q executable is not available", pluginName)
			}
			executable = executablePath
		} else if _, err := exec.LookPath(executable); err != nil {
			return appruntime.PluginRuntime{}, fmt.Errorf("external plugin %q executable is not available", pluginName)
		}
		pluginRuntime.Command = executable
		pluginRuntime.Args = append([]string(nil), manifest.Args...)
		pluginRuntime.ProtocolVersion = manifest.ProtocolVersion
	}
	return pluginRuntime, nil
}

func runtimeModuleConfig(cfg config.ModuleConfig) appruntime.ModuleConfig {
	next := appruntime.ModuleConfig{Enabled: cfg.Enabled}
	if cfg.Params != nil {
		next.Params = make(map[string]any, len(cfg.Params))
		for key, value := range cfg.Params {
			next.Params[key] = value
		}
	}
	return next
}
