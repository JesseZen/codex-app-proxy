package module

import (
	"context"
	"encoding/json"
)

type ModelOverride struct {
	baseMiddleware
}

func NewModelOverride(cfg ModuleConfig) *ModelOverride {
	return &ModelOverride{baseMiddleware: baseMiddleware{name: "model_override", config: cfg}}
}

func (m *ModelOverride) ProcessRequest(ctx context.Context, req *ProxyRequest) error {
	if !m.config.Enabled || !isJSONContentType(req.ContentType, req.Headers.Get("Content-Type")) {
		return nil
	}
	model, _ := m.config.Params["model"].(string)
	if model == "" {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return err
	}
	body["model"] = model
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req.Body = encoded
	req.Headers.Del("Content-Length")
	return nil
}

func (m *ModelOverride) RequestBodyMode(req ProxyRequestMeta) RequestBodyMode {
	if !m.config.Enabled || !isJSONContentType(req.ContentType, req.Headers.Get("Content-Type")) {
		return RequestBodyStream
	}
	if model, _ := m.config.Params["model"].(string); model == "" {
		return RequestBodyStream
	}
	return RequestBodyBuffer
}
