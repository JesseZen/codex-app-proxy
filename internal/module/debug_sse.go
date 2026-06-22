package module

import (
	"context"
	"fmt"
	"io"
)

type DebugSSE struct {
	baseMiddleware
	writer io.Writer
}

func NewDebugSSE(cfg ModuleConfig, writer io.Writer) *DebugSSE {
	return &DebugSSE{
		baseMiddleware: baseMiddleware{name: "debug_sse", config: cfg},
		writer:         writer,
	}
}

func (m *DebugSSE) ProcessRequest(ctx context.Context, req *ProxyRequest) error {
	return nil
}

func (m *DebugSSE) WrapResponse(ctx context.Context, req *ProxyRequest, upstream *ProxyResponse) (*ProxyResponse, error) {
	if !m.config.Enabled || m.writer == nil || !isEventStream(upstream.ContentType, upstream.Headers.Get("Content-Type")) {
		return upstream, nil
	}
	next := *upstream
	next.Body = &debugSSEReadCloser{source: upstream.Body, writer: m.writer}
	return &next, nil
}

type debugSSEReadCloser struct {
	source            io.ReadCloser
	writer            io.Writer
	parser            SSEParser
	chunks            int
	totalBytes        int
	responseCompleted bool
	done              bool
}

func (r *debugSSEReadCloser) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	if n > 0 {
		r.totalBytes += n
		events, parseErr := r.parser.Push(p[:n], false)
		if parseErr == nil {
			r.chunks += len(events)
			for _, event := range events {
				if event.Event == "response.completed" {
					r.responseCompleted = true
				}
				if event.Done {
					r.done = true
				}
			}
		}
		_, _ = fmt.Fprintf(
			r.writer,
			"DEBUG sse chunk bytes=%d chunks=%d total_bytes=%d response_completed=%t done=%t\n",
			n,
			r.chunks,
			r.totalBytes,
			r.responseCompleted,
			r.done,
		)
	}
	if err == io.EOF {
		events, parseErr := r.parser.Push(nil, true)
		if parseErr == nil && len(events) > 0 {
			r.chunks += len(events)
			for _, event := range events {
				if event.Event == "response.completed" {
					r.responseCompleted = true
				}
				if event.Done {
					r.done = true
				}
			}
		}
	}
	return n, err
}

func (r *debugSSEReadCloser) Close() error {
	return r.source.Close()
}
