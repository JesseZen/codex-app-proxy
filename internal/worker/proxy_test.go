package worker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jesse/codex-app-proxy/internal/module"
	"github.com/jesse/codex-app-proxy/internal/upstream"
)

func TestWorkerPassesThroughWithNoModulesAndInjectsAuthorization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.URL.RawQuery != "x=1" {
			t.Fatalf("unexpected server URL %s", r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Fatalf("authorization was not injected: %q", r.Header.Get("Authorization"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"body": string(body),
		})
	}))
	defer server.Close()

	w := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: server.URL, APIKey: "test-secret"},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses?x=1", strings.NewReader(`{"input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"body":"{\"input\":\"hello\"}"`) {
		t.Fatalf("unexpected response body %s", res.Body.String())
	}
}

func TestWorkerUsesOneSnapshotForWholeRequest(t *testing.T) {
	firstReady := make(chan struct{})
	releaseFirst := make(chan struct{})
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(firstReady)
		<-releaseFirst
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("first"))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("second"))
	}))
	defer second.Close()

	w := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: first.URL},
		},
	})

	result := make(chan string, 1)
	go func() {
		res := httptest.NewRecorder()
		w.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://proxy.local/stream", nil))
		result <- res.Body.String()
	}()

	select {
	case <-firstReady:
	case <-time.After(time.Second):
		t.Fatal("first server did not receive request")
	}

	if err := w.UpdateSnapshot(RuntimeConfigSnapshot{
		Generation: 2,
		Upstream:   upstream.RuntimeUpstream{BaseURL: second.URL},
	}); err != nil {
		t.Fatal(err)
	}
	close(releaseFirst)

	select {
	case got := <-result:
		if got != "first" {
			t.Fatalf("in-flight request used changed snapshot: %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not finish")
	}

	res := httptest.NewRecorder()
	w.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://proxy.local/stream", nil))
	if res.Body.String() != "second" {
		t.Fatalf("new request did not use new snapshot: %q", res.Body.String())
	}
}

func TestWorkerRunsModuleChain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()

	w := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: server.URL},
			Modules: []module.Middleware{
				module.NewImageFilter(module.ModuleConfig{Enabled: true}),
			},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"tools":[{"type":"image_generation"},{"type":"function","name":"keep"}]}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if strings.Contains(res.Body.String(), "image_generation") {
		t.Fatalf("module chain did not filter body: %s", res.Body.String())
	}
}

func TestCopyResponseSkipsEmptyReads(t *testing.T) {
	writer := &recordingResponseWriter{header: http.Header{}}
	body := &emptyThenDataReadCloser{data: []byte("ok")}
	resp := &module.ProxyResponse{
		StatusCode: http.StatusAccepted,
		Headers:    http.Header{"X-Test": []string{"1"}},
		Body:       body,
	}

	err := copyProxyResponse(context.Background(), writer, resp)
	if err != nil {
		t.Fatal(err)
	}
	if writer.emptyWriteCount != 0 || writer.flushCount != 1 || string(writer.body) != "ok" {
		t.Fatalf("bad copy behavior: writes=%d flushes=%d body=%q", writer.emptyWriteCount, writer.flushCount, writer.body)
	}
}

type emptyThenDataReadCloser struct {
	data []byte
	read int
}

func (r *emptyThenDataReadCloser) Read(p []byte) (int, error) {
	r.read++
	switch r.read {
	case 1:
		return 0, nil
	case 2:
		return copy(p, r.data), io.EOF
	default:
		return 0, io.EOF
	}
}

func (r *emptyThenDataReadCloser) Close() error {
	return nil
}

type recordingResponseWriter struct {
	header          http.Header
	status          int
	body            []byte
	emptyWriteCount int
	flushCount      int
}

func (w *recordingResponseWriter) Header() http.Header {
	return w.header
}

func (w *recordingResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}

func (w *recordingResponseWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		w.emptyWriteCount++
	}
	w.body = append(w.body, data...)
	return len(data), nil
}

func (w *recordingResponseWriter) Flush() {
	w.flushCount++
}
