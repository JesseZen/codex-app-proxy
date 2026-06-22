package module

import (
	"context"
	"encoding/json"
	"strings"
)

type ImageFilter struct {
	baseMiddleware
}

func NewImageFilter(cfg ModuleConfig) *ImageFilter {
	return &ImageFilter{baseMiddleware: baseMiddleware{name: "image_filter", config: cfg}}
}

func (m *ImageFilter) ProcessRequest(ctx context.Context, req *ProxyRequest) error {
	if !m.config.Enabled || !isJSONContentType(req.ContentType, req.Headers.Get("Content-Type")) {
		return nil
	}

	var body any
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return err
	}

	next, changed := sanitizeJSONBody(body)
	if !changed {
		return nil
	}

	encoded, err := json.Marshal(next)
	if err != nil {
		return err
	}
	req.Body = encoded
	req.Headers.Del("Content-Length")
	req.Headers.Set("Content-Type", "application/json")
	req.Headers.Del("Content-Encoding")
	req.ContentType = "application/json"
	return nil
}

func (m *ImageFilter) RequestBodyMode(req ProxyRequestMeta) RequestBodyMode {
	if !m.config.Enabled || !isJSONContentType(req.ContentType, req.Headers.Get("Content-Type")) {
		return RequestBodyStream
	}
	return RequestBodyBuffer
}

func isJSONContentType(values ...string) bool {
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "application/json" || strings.HasPrefix(value, "application/json;") {
			return true
		}
	}
	return false
}

func sanitizeJSONBody(body any) (any, bool) {
	object, ok := body.(map[string]any)
	if !ok {
		return body, false
	}

	changed := false
	next := make(map[string]any, len(object))
	for key, value := range object {
		next[key] = value
	}

	if tools, ok := next["tools"].([]any); ok {
		filtered := make([]any, 0, len(tools))
		for _, tool := range tools {
			if isImageGenerationTool(tool) {
				changed = true
				continue
			}
			filtered = append(filtered, tool)
		}
		next["tools"] = filtered
	}

	if toolChoice, ok := next["tool_choice"]; ok {
		sanitized := sanitizeToolChoice(toolChoice)
		if sanitized != toolChoice {
			changed = true
		}
		next["tool_choice"] = sanitized
	}

	return next, changed
}

func isImageGenerationTool(tool any) bool {
	switch typed := tool.(type) {
	case string:
		return typed == "image_generation"
	case map[string]any:
		return typed["type"] == "image_generation" || typed["name"] == "image_generation"
	default:
		return false
	}
}

func sanitizeToolChoice(toolChoice any) any {
	if toolChoice == "image_generation" {
		return "auto"
	}

	object, ok := toolChoice.(map[string]any)
	if !ok {
		return toolChoice
	}
	if object["type"] == "image_generation" || object["name"] == "image_generation" {
		return "auto"
	}
	if nested, ok := object["tool"].(map[string]any); ok {
		if nested["type"] == "image_generation" || nested["name"] == "image_generation" {
			return "auto"
		}
	}
	return toolChoice
}
