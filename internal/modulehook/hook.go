package modulehook

import "github.com/jesse/agent-inn/internal/module"

type Hook interface {
	Name() string
	Config() module.ModuleConfig
	UpdateConfig(module.ModuleConfig) error
	Start() error
	Stop() error
	State() string
	Detail() map[string]string
}

type Status struct {
	State  string            `json:"state"`
	Detail map[string]string `json:"detail,omitempty"`
}

const (
	StateClean  = "clean"
	StateActive = "active"
)
