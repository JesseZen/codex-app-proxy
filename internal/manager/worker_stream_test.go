package manager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jesse/codex-app-proxy/internal/config"
)

func TestWorkerStreamEndpointReplaysExistingLines(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai", LogLevel: "detail"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	if _, err := m.LogSink("app").Write([]byte("INFO POST /v1/responses\n")); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/6767/stream", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)

	res := httptest.NewRecorder()
	m.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected stream status %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, "event: stream.raw_redacted") {
		t.Fatalf("stream response missing event type: %s", body)
	}
	if !strings.Contains(body, `"worker":"app"`) {
		t.Fatalf("stream response missing worker field: %s", body)
	}
	if !strings.Contains(body, "POST /v1/responses") {
		t.Fatalf("stream response missing replayed line: %s", body)
	}
}

func TestWorkerStreamEndpointStreamsLiveSubscribedLines(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai", LogLevel: "detail"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/6767/stream", nil)
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)

	recorder := newWorkerStreamRecorder()
	done := make(chan struct{})
	go func() {
		m.ServeHTTP(recorder, req)
		close(done)
	}()

	if _, err := m.LogSink("app").Write([]byte("INFO POST /v1/responses\n")); err != nil {
		t.Fatal(err)
	}

	requireBodyContainsEventually(t, recorder, "POST /v1/responses")
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream to stop after cancel")
	}
}

func TestWorkerStreamEndpointStopsWhenSinkCloses(t *testing.T) {
	m := New(Config{
		Config: config.Config{
			Workers: map[string]config.WorkerConfig{
				"app": {Port: 6767, Upstream: "openai", LogLevel: "detail"},
			},
			Upstreams: map[string]config.UpstreamProfile{
				"openai": {BaseURL: "https://api.openai.com/v1"},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "http://manager.local/api/workers/6767/stream", nil)
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)

	recorder := newWorkerStreamRecorder()
	done := make(chan struct{})
	go func() {
		m.ServeHTTP(recorder, req)
		close(done)
	}()

	if err := m.LogSink("app").Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream to stop after sink close")
	}
}

type workerStreamRecorder struct {
	*httptest.ResponseRecorder
	closeCh chan bool
}

func newWorkerStreamRecorder() *workerStreamRecorder {
	return &workerStreamRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		closeCh:          make(chan bool),
	}
}

func (r *workerStreamRecorder) CloseNotify() <-chan bool {
	return r.closeCh
}

func requireBodyContainsEventually(t *testing.T, recorder *workerStreamRecorder, needle string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(recorder.Body.String(), needle) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in body", needle)
}
