package manager

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jesse/codex-app-proxy/internal/config"
)

func TestEventBusPublishesAndReplaysEvents(t *testing.T) {
	bus := newEventBus(2)
	first := bus.Publish("worker.started", map[string]any{"worker": "app"})
	second := bus.Publish("worker.stopped", map[string]any{"worker": "app"})

	events := bus.Replay(first.ID - 1)
	if len(events) != 2 || events[0].ID != first.ID || events[1].ID != second.ID {
		t.Fatalf("bad replay: %#v", events)
	}
}

func TestEventBusSubscribeReceivesPublishedEvent(t *testing.T) {
	bus := newEventBus(2)
	sub := bus.Subscribe(0)
	defer sub.Close()

	bus.Publish("config.status.changed", map[string]any{"dirty": true})

	select {
	case event := <-sub.C:
		if event.Type != "config.status.changed" {
			t.Fatalf("bad event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventBusReplayClonesPayloadAndRespectsCapacity(t *testing.T) {
	bus := newEventBus(2)
	payload := map[string]any{"worker": "app"}
	bus.Publish("worker.started", payload)
	second := bus.Publish("worker.updated", map[string]any{"worker": "cli"})
	third := bus.Publish("worker.stopped", map[string]any{"worker": "app"})

	payload["worker"] = "mutated"

	events := bus.Replay(0)
	if len(events) != 2 || events[0].ID != second.ID || events[1].ID != third.ID {
		t.Fatalf("bad replay window: %#v", events)
	}
	if events[1].Payload["worker"] != "app" {
		t.Fatalf("payload should be cloned, got %#v", events[1].Payload)
	}
	events[1].Payload["worker"] = "changed"
	replayedAgain := bus.Replay(third.ID - 1)
	if replayedAgain[0].Payload["worker"] != "app" {
		t.Fatalf("historical payload should be immutable, got %#v", replayedAgain[0].Payload)
	}
}

func TestEventBusSubscribeReplaysBacklogBeforeLiveEvents(t *testing.T) {
	bus := newEventBus(4)
	first := bus.Publish("worker.started", map[string]any{"worker": "app"})
	bus.Publish("worker.updated", map[string]any{"worker": "app"})

	sub := bus.Subscribe(first.ID)
	defer sub.Close()

	backlog := <-sub.C
	if backlog.Type != "worker.updated" {
		t.Fatalf("expected backlog event first, got %#v", backlog)
	}

	bus.Publish("worker.stopped", map[string]any{"worker": "app"})
	select {
	case live := <-sub.C:
		if live.Type != "worker.stopped" {
			t.Fatalf("expected live event second, got %#v", live)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live event")
	}
}

func TestEventBusSubscribeAfterCloseReturnsAlreadyClosedSubscription(t *testing.T) {
	bus := newEventBus(2)
	bus.Close()

	sub := bus.Subscribe(0)
	select {
	case _, ok := <-sub.C:
		if ok {
			t.Fatal("expected closed subscription channel")
		}
	default:
		t.Fatal("expected closed subscription channel to be readable immediately")
	}
	sub.Close()
}

func TestEventBusDeepClonesNestedPayload(t *testing.T) {
	bus := newEventBus(2)
	payload := map[string]any{
		"nested": map[string]any{"worker": "app"},
		"items":  []any{map[string]any{"state": "running"}},
	}
	bus.Publish("worker.started", payload)

	payload["nested"].(map[string]any)["worker"] = "mutated"
	payload["items"].([]any)[0].(map[string]any)["state"] = "stopped"

	event := bus.Replay(0)[0]
	if event.Payload["nested"].(map[string]any)["worker"] != "app" {
		t.Fatalf("expected nested map clone, got %#v", event.Payload)
	}
	if event.Payload["items"].([]any)[0].(map[string]any)["state"] != "running" {
		t.Fatalf("expected nested slice clone, got %#v", event.Payload)
	}
}

func TestEventBusLiveSubscriberCannotMutateHistoricalReplay(t *testing.T) {
	bus := newEventBus(2)
	sub := bus.Subscribe(0)
	defer sub.Close()

	bus.Publish("worker.started", map[string]any{"nested": map[string]any{"worker": "app"}})
	live := <-sub.C
	live.Payload["nested"].(map[string]any)["worker"] = "mutated"

	replayed := bus.Replay(0)
	if replayed[0].Payload["nested"].(map[string]any)["worker"] != "app" {
		t.Fatalf("live subscriber should not mutate history, got %#v", replayed[0].Payload)
	}
}

func TestManagerPublishesWorkerLifecycleEvents(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter: fakeStarter{},
	})
	defer m.Close()

	sub := m.events.Subscribe(0)
	defer sub.Close()

	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-sub.C:
		if event.Type != "worker.started" {
			t.Fatalf("expected worker.started event, got %#v", event)
		}
		if event.Payload["worker"] != "app" {
			t.Fatalf("expected app worker payload, got %#v", event.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker lifecycle event")
	}
}

func TestManagerPublishesWorkerUpdateEvents(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {
					Port:     6767,
					Upstream: "openai",
					Modules: map[string]config.ModuleConfig{
						"api_translate": {Enabled: true, Params: map[string]any{"api_format": "responses"}},
					},
				},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})
	defer m.Close()

	sub := m.events.Subscribe(0)
	defer sub.Close()

	current, ok := m.workerConfig("app")
	if !ok {
		t.Fatal("missing app worker")
	}
	next := current
	next.LogLevel = "detail"
	if err := m.UpdateWorker("app", current, next); err != nil {
		t.Fatal(err)
	}

	event := nextEventOfType(t, sub, "worker.updated")
	if event.Payload["worker"] != "app" || event.Payload["log_level"] != "detail" {
		t.Fatalf("bad worker update event: %#v", event)
	}
	modules, ok := event.Payload["modules"].(map[string]config.ModuleConfig)
	if !ok {
		t.Fatalf("worker update event missing modules: %#v", event.Payload)
	}
	if module := modules["api_translate"]; !module.Enabled || module.Params["api_format"] != "responses" {
		t.Fatalf("worker update event has bad modules payload: %#v", modules)
	}
}

func TestManagerDoesNotPublishWorkerUpdatedForRolledBackPortChange(t *testing.T) {
	starter := &recordingStarter{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		Starter:       starter,
		HealthChecker: &sequenceHealthChecker{results: []bool{false}},
	})
	defer m.Close()
	m.healthWait = 0
	m.healthPoll = 0

	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	sub := m.events.Subscribe(0)
	defer sub.Close()

	current, ok := m.workerConfig("app")
	if !ok {
		t.Fatal("missing app worker")
	}
	next := current
	next.Port = 6868

	err := m.UpdateWorker("app", current, next)
	if err == nil {
		t.Fatal("expected port change health failure")
	}
	if !strings.Contains(err.Error(), "did not become healthy") {
		t.Fatalf("unexpected update error: %v", err)
	}

	restored, ok := m.workerConfig("app")
	if !ok {
		t.Fatal("missing app worker after rollback")
	}
	if restored.Port != current.Port {
		t.Fatalf("expected worker port rollback to %d, got %#v", current.Port, restored)
	}

	assertNoEventOfType(t, sub, "worker.updated", 150*time.Millisecond)
}

func TestManagerPublishesModuleUpdatedAfterGenerationBump(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai", Modules: map[string]config.ModuleConfig{"api_translate": {Enabled: false}}},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	defer m.Close()
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	startGeneration := m.workerGeneration("app")

	sub := m.events.Subscribe(0)
	defer sub.Close()

	body := bytes.NewBufferString(`{"enabled":true,"params":{"api_format":"responses"}}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/workers/6767/modules/api_translate", body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected module patch status %d: %s", res.Code, res.Body.String())
	}

	event := nextEventOfType(t, sub, "module.updated")
	if m.workerGeneration("app") <= startGeneration {
		t.Fatalf("expected generation to be bumped before event, start=%d current=%d", startGeneration, m.workerGeneration("app"))
	}
	if event.Payload["worker"] != "app" || event.Payload["module"] != "api_translate" || event.Payload["enabled"] != true {
		t.Fatalf("bad module event: %#v", event)
	}
	params, ok := event.Payload["params"].(map[string]any)
	if !ok || params["api_format"] != "responses" {
		t.Fatalf("module event missing params: %#v", event.Payload)
	}
}

func TestManagerPublishesProviderUpdatedAfterGenerationBump(t *testing.T) {
	client := &recordingWorkerClient{}
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
		WorkerClient: client,
	})
	defer m.Close()
	if err := m.StartWorker("app"); err != nil {
		t.Fatal(err)
	}
	startGeneration := m.workerGeneration("app")

	sub := m.events.Subscribe(0)
	defer sub.Close()

	body := strings.NewReader(`{"base_url":"https://relay.example/v1","api_format":"chat_completions"}`)
	res := httptest.NewRecorder()
	m.ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "http://manager.local/api/upstreams/openai", body))
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected provider update status %d: %s", res.Code, res.Body.String())
	}

	event := nextEventOfType(t, sub, "upstream.updated")
	if m.workerGeneration("app") <= startGeneration {
		t.Fatalf("expected generation to be bumped before event, start=%d current=%d", startGeneration, m.workerGeneration("app"))
	}
	if event.Payload["upstream"] != "openai" {
		t.Fatalf("bad upstream event: %#v", event)
	}
}

func nextEventOfType(t *testing.T, sub *eventSubscription, eventType EventType) Event {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-sub.C:
			if event.Type == eventType {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", eventType)
		}
	}
}

func assertNoEventOfType(t *testing.T, sub *eventSubscription, eventType EventType, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event := <-sub.C:
			if event.Type == eventType {
				t.Fatalf("unexpected %s event: %#v", eventType, event)
			}
		case <-deadline:
			return
		}
	}
}
