package manager

// WorkerState 表示 worker 的运行状态，用于 statuses map 与事件 payload。
type WorkerState string

const (
	WorkerStateRunning       WorkerState = "running"
	WorkerStateFailed        WorkerState = "failed"
	WorkerStateStopping      WorkerState = "stopping"
	WorkerStateRestarting    WorkerState = "restarting"
	WorkerStateStopped       WorkerState = "stopped"
	WorkerStateStoppedForced WorkerState = "stopped (forced)"
	WorkerStateConfigured    WorkerState = "configured"
)

// EventType 表示事件总线上的事件类型，用于 publishEvent / Publish 与 As* 解析。
type EventType string

const (
	EventWorkerStarted       EventType = "worker.started"
	EventWorkerStopped       EventType = "worker.stopped"
	EventWorkerRestarted     EventType = "worker.restarted"
	EventWorkerHealthChanged EventType = "worker.health.changed"
	EventWorkerUpdated       EventType = "worker.updated"
	EventModuleUpdated       EventType = "module.updated"
	EventUpstreamUpdated     EventType = "upstream.updated"
	EventConfigStatusChanged EventType = "config.status.changed"
	EventStreamRawRedacted   EventType = "stream.raw_redacted"
)
