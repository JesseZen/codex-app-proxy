package manager

// payloadInt 从 any 中提取 int，兼容 JSON 反序列化后的 float64。
func payloadInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

// payloadBoolMap 从 any 中提取 map[string]bool，兼容 map[string]any。
func payloadBoolMap(value any) map[string]bool {
	switch typed := value.(type) {
	case map[string]bool:
		return typed
	case map[string]any:
		out := make(map[string]bool, len(typed))
		for key, raw := range typed {
			if enabled, ok := raw.(bool); ok {
				out[key] = enabled
			}
		}
		return out
	default:
		return nil
	}
}

// AsWorkerStarted 解析 worker.started 事件。ok=false 表示类型不匹配。
func (e Event) AsWorkerStarted() (worker string, status string, ok bool) {
	if e.Type != EventWorkerStarted {
		return "", "", false
	}
	worker, _ = e.Payload["worker"].(string)
	status, _ = e.Payload["status"].(string)
	return worker, status, true
}

// AsWorkerStopped 解析 worker.stopped 事件。ok=false 表示类型不匹配。
func (e Event) AsWorkerStopped() (worker string, status string, ok bool) {
	if e.Type != EventWorkerStopped {
		return "", "", false
	}
	worker, _ = e.Payload["worker"].(string)
	status, _ = e.Payload["status"].(string)
	return worker, status, true
}

// AsWorkerRestarted 解析 worker.restarted 事件。ok=false 表示类型不匹配。
func (e Event) AsWorkerRestarted() (worker string, status string, ok bool) {
	if e.Type != EventWorkerRestarted {
		return "", "", false
	}
	worker, _ = e.Payload["worker"].(string)
	status, _ = e.Payload["status"].(string)
	return worker, status, true
}

// AsWorkerHealthChanged 解析 worker.health.changed 事件。ok=false 表示类型不匹配。
func (e Event) AsWorkerHealthChanged() (worker string, status string, errMsg string, ok bool) {
	if e.Type != EventWorkerHealthChanged {
		return "", "", "", false
	}
	worker, _ = e.Payload["worker"].(string)
	status, _ = e.Payload["status"].(string)
	errMsg, _ = e.Payload["error"].(string)
	return worker, status, errMsg, true
}

// AsWorkerUpdated 解析 worker.updated 事件。ok=false 表示类型不匹配。
func (e Event) AsWorkerUpdated() (worker string, port int, upstream string, logLevel string, modules map[string]bool, ok bool) {
	if e.Type != EventWorkerUpdated {
		return "", 0, "", "", nil, false
	}
	worker, _ = e.Payload["worker"].(string)
	port = payloadInt(e.Payload["port"])
	upstream, _ = e.Payload["upstream"].(string)
	logLevel, _ = e.Payload["log_level"].(string)
	modules = payloadBoolMap(e.Payload["modules"])
	return worker, port, upstream, logLevel, modules, true
}

// AsModuleUpdated 解析 module.updated 事件。ok=false 表示类型不匹配。
func (e Event) AsModuleUpdated() (worker string, port int, module string, enabled bool, params map[string]any, ok bool) {
	if e.Type != EventModuleUpdated {
		return "", 0, "", false, nil, false
	}
	worker, _ = e.Payload["worker"].(string)
	port = payloadInt(e.Payload["port"])
	module, _ = e.Payload["module"].(string)
	enabled, _ = e.Payload["enabled"].(bool)
	params, _ = e.Payload["params"].(map[string]any)
	return worker, port, module, enabled, params, true
}

// AsUpstreamUpdated 解析 upstream.updated 事件。ok=false 表示类型不匹配。
func (e Event) AsUpstreamUpdated() (upstream string, ok bool) {
	if e.Type != EventUpstreamUpdated {
		return "", false
	}
	upstream, _ = e.Payload["upstream"].(string)
	return upstream, true
}

// AsConfigStatusChanged 解析 config.status.changed 事件。ok=false 表示类型不匹配。
func (e Event) AsConfigStatusChanged() (dirty bool, generation int, ok bool) {
	if e.Type != EventConfigStatusChanged {
		return false, 0, false
	}
	dirty, _ = e.Payload["dirty"].(bool)
	generation = payloadInt(e.Payload["generation"])
	return dirty, generation, true
}

// AsStreamRawRedacted 解析 stream.raw_redacted 事件。ok=false 表示类型不匹配。
func (e Event) AsStreamRawRedacted() (worker string, line string, ok bool) {
	if e.Type != EventStreamRawRedacted {
		return "", "", false
	}
	worker, _ = e.Payload["worker"].(string)
	line, _ = e.Payload["line"].(string)
	return worker, line, true
}
