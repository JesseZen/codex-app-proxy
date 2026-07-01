package module

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"time"
)

const externalRequestTimeout = 5 * time.Second

type ExternalRequestRuntime struct {
	Command         string
	Args            []string
	ProtocolVersion string
}

type ExternalRequestMiddleware struct {
	baseMiddleware
	runtime ExternalRequestRuntime
}

type externalRequestPayload struct {
	Method      string      `json:"method"`
	Path        string      `json:"path"`
	Headers     http.Header `json:"headers"`
	Body        string      `json:"body"`
	ContentType string      `json:"content_type"`
}

func NewExternalRequestMiddleware(name string, cfg ModuleConfig, runtime ExternalRequestRuntime) *ExternalRequestMiddleware {
	return &ExternalRequestMiddleware{
		baseMiddleware: baseMiddleware{name: name, config: cfg},
		runtime:        runtime,
	}
}

func (m *ExternalRequestMiddleware) ProcessRequest(ctx context.Context, req *ProxyRequest) error {
	if !m.config.Enabled {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, externalRequestTimeout)
	defer cancel()
	payload := externalRequestPayload{
		Method:      req.Method,
		Path:        req.Path,
		Headers:     req.Headers.Clone(),
		Body:        string(req.Body),
		ContentType: req.ContentType,
	}
	input, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, m.runtime.Command, m.runtime.Args...)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("external request middleware %q failed: %s", m.name, stderr.String())
		}
		return fmt.Errorf("external request middleware %q failed: %w", m.name, err)
	}
	var next externalRequestPayload
	if err := json.Unmarshal(output, &next); err != nil {
		return fmt.Errorf("external request middleware %q returned invalid JSON", m.name)
	}
	req.Method = next.Method
	req.Path = next.Path
	req.Headers = next.Headers.Clone()
	req.Body = []byte(next.Body)
	req.ContentType = next.ContentType
	req.Headers.Del("Content-Length")
	return nil
}

func (m *ExternalRequestMiddleware) RequestBodyMode(req ProxyRequestMeta) RequestBodyMode {
	if !m.config.Enabled {
		return RequestBodyStream
	}
	return RequestBodyBuffer
}
