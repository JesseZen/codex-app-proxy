package worker

import (
	"fmt"
	"sync/atomic"

	"github.com/jesse/codex-app-proxy/internal/module"
	appruntime "github.com/jesse/codex-app-proxy/internal/runtime"
	"github.com/jesse/codex-app-proxy/internal/upstream"
)

type RuntimeConfigSnapshot struct {
	Generation        int
	Upstream          upstream.RuntimeUpstream
	CompiledUpstream  upstream.Compiled
	Modules           []module.Middleware
	ConfigPatchState  module.ConfigPatchState
	ConfigPatchDetail map[string]string
}

func (s RuntimeConfigSnapshot) Validate() error {
	if s.Upstream.BaseURL == "" {
		return fmt.Errorf("upstream base URL is required")
	}
	return nil
}

func (s RuntimeConfigSnapshot) withCompiledUpstream() RuntimeConfigSnapshot {
	if s.CompiledUpstream.BaseURL != nil || s.Upstream.BaseURL == "" {
		return s
	}
	compiled, err := upstream.Compile(appruntime.UpstreamRuntime{
		ID:        appruntime.UpstreamID(s.Upstream.Name),
		BaseURL:   s.Upstream.BaseURL,
		APIKey:    s.Upstream.APIKey,
		APIFormat: appruntime.APIFormat(s.Upstream.APIFormat),
	})
	if err != nil {
		return s
	}
	s.CompiledUpstream = compiled
	return s
}

func snapshotFromRuntime(runtime appruntime.WorkerRuntime) (RuntimeConfigSnapshot, error) {
	compiled, err := upstream.Compile(appruntime.UpstreamRuntime{
		ID:        runtime.Upstream.ID,
		BaseURL:   runtime.Upstream.BaseURL,
		APIKey:    runtime.Upstream.APIKey,
		APIFormat: runtime.Upstream.APIFormat,
	})
	if err != nil {
		return RuntimeConfigSnapshot{}, err
	}
	snapshot := RuntimeConfigSnapshot{
		Generation: int(runtime.Generation),
		Upstream: upstream.RuntimeUpstream{
			Name:      string(runtime.Upstream.ID),
			BaseURL:   runtime.Upstream.BaseURL,
			APIKey:    runtime.Upstream.APIKey,
			APIFormat: string(runtime.Upstream.APIFormat),
		},
		CompiledUpstream: compiled,
		Modules:          buildRuntimeModules(runtime.Modules, runtime.Upstream.APIFormat),
	}
	if snapshot.Generation == 0 {
		snapshot.Generation = 1
	}
	if err := snapshot.Validate(); err != nil {
		return RuntimeConfigSnapshot{}, err
	}
	return snapshot, nil
}

type snapshotHolder struct {
	value atomic.Value
}

func newSnapshotHolder(snapshot RuntimeConfigSnapshot) *snapshotHolder {
	holder := &snapshotHolder{}
	holder.value.Store(snapshot)
	return holder
}

func (h *snapshotHolder) Load() RuntimeConfigSnapshot {
	return h.value.Load().(RuntimeConfigSnapshot)
}

func (h *snapshotHolder) Store(snapshot RuntimeConfigSnapshot) {
	h.value.Store(snapshot)
}
