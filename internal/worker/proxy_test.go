package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/jesse/agent-inn/internal/module"
	appruntime "github.com/jesse/agent-inn/internal/runtime"
	"github.com/jesse/agent-inn/internal/upstream"
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

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: server.URL, APIKey: "test-secret"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

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

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: first.URL},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

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

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: server.URL},
			Modules: []module.Middleware{
				module.NewImageFilter(module.ModuleConfig{Enabled: true}),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader(`{"tools":[{"type":"image_generation"},{"type":"function","name":"keep"}]}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if strings.Contains(res.Body.String(), "image_generation") {
		t.Fatalf("module chain did not filter body: %s", res.Body.String())
	}
}

func TestWorkerRunsExternalRequestMiddleware(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "external-filter")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
python3 -c 'import json,sys
payload=json.load(sys.stdin)
payload["headers"]["X-External"]=["yes"]
payload["body"]="external:"+payload.get("body","")
json.dump(payload, sys.stdout)'
`), 0700); err != nil {
		t.Fatal(err)
	}

	var receivedBody string
	var receivedHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		receivedBody = string(body)
		receivedHeader = r.Header.Get("X-External")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w, err := New(Options{
		Runtime: appruntime.WorkerRuntime{
			ID:         "cli-openai",
			Generation: 1,
			ListenPort: 11199,
			Upstream: appruntime.UpstreamRuntime{
				ID:      "openai",
				BaseURL: server.URL,
			},
			Plugins: map[string]appruntime.PluginRuntime{
				"external_filter": {
					Kind:            "request_middleware",
					Source:          "external",
					Command:         script,
					ProtocolVersion: "1",
				},
			},
			Modules: map[string]appruntime.ModuleConfig{
				"external_filter": {Enabled: true},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader("hello"))
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if receivedHeader != "yes" || receivedBody != "external:hello" {
		t.Fatalf("external middleware did not mutate request: header=%q body=%q", receivedHeader, receivedBody)
	}
}

func TestWorkerRunsExternalRequestMiddlewareWithArgs(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "external-filter.py")
	if err := os.WriteFile(script, []byte(`import json,sys
payload=json.load(sys.stdin)
payload["headers"]["X-External-Args"]=["yes"]
json.dump(payload, sys.stdout)
`), 0600); err != nil {
		t.Fatal(err)
	}

	var receivedHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-External-Args")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w, err := New(Options{
		Runtime: appruntime.WorkerRuntime{
			ID:         "cli-openai",
			Generation: 1,
			ListenPort: 11199,
			Upstream: appruntime.UpstreamRuntime{
				ID:      "openai",
				BaseURL: server.URL,
			},
			Plugins: map[string]appruntime.PluginRuntime{
				"external_filter": {
					Kind:            "request_middleware",
					Source:          "external",
					Command:         "python3",
					Args:            []string{script},
					ProtocolVersion: "1",
				},
			},
			Modules: map[string]appruntime.ModuleConfig{
				"external_filter": {Enabled: true},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	w.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", strings.NewReader("hello")))

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if receivedHeader != "yes" {
		t.Fatalf("external middleware args were not used: header=%q", receivedHeader)
	}
}

func TestWorkerClearsContentEncodingAfterBufferingCompressedRequest(t *testing.T) {
	type upstreamRequest struct {
		Body            string
		ContentEncoding string
	}
	received := upstreamRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		received = upstreamRequest{
			Body:            string(body),
			ContentEncoding: r.Header.Get("Content-Encoding"),
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	w, err := New(Options{
		Snapshot: RuntimeConfigSnapshot{
			Generation: 1,
			Upstream:   upstream.RuntimeUpstream{BaseURL: server.URL},
			Modules: []module.Middleware{
				module.NewImageFilter(module.ModuleConfig{Enabled: true}),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var compressed bytes.Buffer
	zw, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write([]byte(`{"input":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses", &compressed)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "zstd")
	res := httptest.NewRecorder()
	w.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", res.Code, res.Body.String())
	}
	if received != (upstreamRequest{Body: `{"input":"hello"}`}) {
		t.Fatalf("unexpected upstream request %#v", received)
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

func TestNewRejectsInvalidRuntimeInsteadOfPanicking(t *testing.T) {
	worker, err := New(Options{
		Runtime: appruntime.WorkerRuntime{
			Upstream: appruntime.UpstreamRuntime{
				ID:      "openai",
				BaseURL: "https://api.openai.com/v1",
			},
			Hooks: map[string]appruntime.ModuleConfig{
				"unknown": {Enabled: true},
			},
		},
	})
	if err == nil {
		t.Fatal("expected invalid runtime error")
	}
	if worker != nil {
		t.Fatalf("expected nil worker on error, got %#v", worker)
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
