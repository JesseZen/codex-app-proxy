package module

import (
	"context"
	"fmt"
	"io"
)

type RequestLog struct {
	baseMiddleware
	writer io.Writer
}

func NewRequestLog(cfg ModuleConfig, writer io.Writer) *RequestLog {
	return &RequestLog{
		baseMiddleware: baseMiddleware{name: "request_log", config: cfg},
		writer:         writer,
	}
}

func (m *RequestLog) ProcessRequest(ctx context.Context, req *ProxyRequest) error {
	if !m.config.Enabled || m.writer == nil {
		return nil
	}
	if req.OriginalPath != "" && req.OriginalPath != req.Path {
		_, _ = fmt.Fprintf(m.writer, "INFO %s %s -> %s\n", req.Method, req.OriginalPath, req.Path)
		return nil
	}
	_, _ = fmt.Fprintf(m.writer, "INFO %s %s\n", req.Method, req.Path)
	return nil
}
