package module

import (
	"context"
	"io"
	"net/http"
)

type RequestBodyMode int

const (
	RequestBodyStream RequestBodyMode = iota
	RequestBodyBuffer
)

type ProxyRequestMeta struct {
	Method      string
	Path        string
	Headers     http.Header
	ContentType string
}

type RequestBodyPlanner interface {
	RequestBodyMode(req ProxyRequestMeta) RequestBodyMode
}

type ModuleConfig struct {
	Enabled bool           `json:"enabled"`
	Params  map[string]any `json:"params,omitempty"`
}

type Middleware interface {
	Name() string
	ProcessRequest(ctx context.Context, req *ProxyRequest) error
	WrapResponse(ctx context.Context, req *ProxyRequest, upstream *ProxyResponse) (*ProxyResponse, error)
	Config() ModuleConfig
	UpdateConfig(cfg ModuleConfig) error
	RequestBodyPlanner
}

type ProxyRequest struct {
	Method       string
	Path         string
	Headers      http.Header
	Body         []byte
	ContentType  string
	OriginalPath string
}

type ProxyResponse struct {
	StatusCode  int
	Headers     http.Header
	Body        io.ReadCloser
	ContentType string
}

type baseMiddleware struct {
	name   string
	config ModuleConfig
}

func (m *baseMiddleware) RequestBodyMode(req ProxyRequestMeta) RequestBodyMode {
	return RequestBodyStream
}

func (m *baseMiddleware) Name() string {
	return m.name
}

func (m *baseMiddleware) Config() ModuleConfig {
	return m.config
}

func (m *baseMiddleware) UpdateConfig(cfg ModuleConfig) error {
	m.config = cfg
	return nil
}

func (m *baseMiddleware) WrapResponse(ctx context.Context, req *ProxyRequest, upstream *ProxyResponse) (*ProxyResponse, error) {
	return upstream, nil
}
