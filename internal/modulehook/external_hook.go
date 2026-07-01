package modulehook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/jesse/agent-inn/internal/module"
)

const externalHookTimeout = 5 * time.Second

type ExternalHookRuntime struct {
	Command         string
	Args            []string
	ProtocolVersion string
}

type ExternalHook struct {
	name    string
	config  module.ModuleConfig
	runtime ExternalHookRuntime
	deps    BuildDependencies
	status  Status
}

type externalHookPayload struct {
	Action string              `json:"action"`
	Worker externalHookWorker  `json:"worker"`
	Config module.ModuleConfig `json:"config"`
}

type externalHookWorker struct {
	ID   string `json:"id"`
	Port int    `json:"port"`
}

func NewExternalHook(name string, cfg module.ModuleConfig, runtime ExternalHookRuntime, deps BuildDependencies) *ExternalHook {
	return &ExternalHook{
		name:    name,
		config:  module.CloneModuleConfig(cfg),
		runtime: runtime,
		deps:    deps,
	}
}

func (h *ExternalHook) Name() string {
	return h.name
}

func (h *ExternalHook) Config() module.ModuleConfig {
	return module.CloneModuleConfig(h.config)
}

func (h *ExternalHook) UpdateConfig(cfg module.ModuleConfig) error {
	h.config = module.CloneModuleConfig(cfg)
	return nil
}

func (h *ExternalHook) Start() error {
	status, err := h.run("start")
	if err != nil {
		return err
	}
	if status.State == "" {
		status.State = StateActive
	}
	h.status = status
	return nil
}

func (h *ExternalHook) Stop() error {
	status, err := h.run("stop")
	if err != nil {
		return err
	}
	if status.State == "" {
		status.State = StateClean
	}
	h.status = status
	return nil
}

func (h *ExternalHook) RefreshStatus() error {
	status, err := h.run("status")
	if err != nil {
		return err
	}
	if status.State == "" {
		return fmt.Errorf("external lifecycle hook %q status requires state", h.name)
	}
	h.status = status
	return nil
}

func (h *ExternalHook) State() string {
	return h.status.State
}

func (h *ExternalHook) Detail() map[string]string {
	if len(h.status.Detail) == 0 {
		return nil
	}
	detail := make(map[string]string, len(h.status.Detail))
	for key, value := range h.status.Detail {
		detail[key] = value
	}
	return detail
}

func (h *ExternalHook) Status() Status {
	status := Status{State: h.status.State}
	if len(h.status.Detail) > 0 {
		status.Detail = make(map[string]string, len(h.status.Detail))
		for key, value := range h.status.Detail {
			status.Detail[key] = value
		}
	}
	return status
}

func (h *ExternalHook) run(action string) (Status, error) {
	ctx, cancel := context.WithTimeout(context.Background(), externalHookTimeout)
	defer cancel()
	payload := externalHookPayload{
		Action: action,
		Worker: externalHookWorker{
			ID:   h.deps.WorkerID,
			Port: h.deps.WorkerPort,
		},
		Config: module.CloneModuleConfig(h.config),
	}
	input, err := json.Marshal(payload)
	if err != nil {
		return Status{}, err
	}
	args := append([]string(nil), h.runtime.Args...)
	args = append(args, action)
	cmd := exec.CommandContext(ctx, h.runtime.Command, args...)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		if stderr.Len() > 0 {
			return Status{}, fmt.Errorf("external lifecycle hook %q %s failed: %s", h.name, action, stderr.String())
		}
		return Status{}, fmt.Errorf("external lifecycle hook %q %s failed: %w", h.name, action, err)
	}
	var status Status
	if err := json.Unmarshal(output, &status); err != nil {
		return Status{}, fmt.Errorf("external lifecycle hook %q %s returned invalid JSON", h.name, action)
	}
	return cloneStatus(status), nil
}

func cloneStatus(status Status) Status {
	next := Status{State: status.State}
	if len(status.Detail) > 0 {
		next.Detail = make(map[string]string, len(status.Detail))
		for key, value := range status.Detail {
			next.Detail[key] = value
		}
	}
	return next
}
