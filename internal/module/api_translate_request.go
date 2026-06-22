package module

import (
	"context"
	"encoding/json"
)

const chatCompletionsFormat = "chat_completions"

type APITranslate struct {
	baseMiddleware
}

func NewAPITranslate(cfg ModuleConfig) *APITranslate {
	return &APITranslate{baseMiddleware: baseMiddleware{name: "api_translate", config: cfg}}
}

func (m *APITranslate) ProcessRequest(ctx context.Context, req *ProxyRequest) error {
	if !m.config.Enabled || m.apiFormat() != chatCompletionsFormat || !isResponsesPath(req.Path) {
		return nil
	}
	if !isJSONContentType(req.ContentType, req.Headers.Get("Content-Type")) {
		return nil
	}

	var body map[string]any
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return err
	}

	translated := translateResponsesBodyToChat(body)
	encoded, err := json.Marshal(translated)
	if err != nil {
		return err
	}

	req.OriginalPath = req.Path
	req.Path = translateResponsesPath(req.Path)
	req.Body = encoded
	req.ContentType = "application/json"
	req.Headers.Set("Content-Type", "application/json")
	req.Headers.Set("Accept", "text/event-stream")
	req.Headers.Set("Accept-Encoding", "identity")
	req.Headers.Set("Cache-Control", "no-cache")
	req.Headers.Del("Content-Length")
	req.Headers.Del("Content-Encoding")
	return nil
}

func (m *APITranslate) RequestBodyMode(req ProxyRequestMeta) RequestBodyMode {
	if !m.config.Enabled || m.apiFormat() != chatCompletionsFormat || !isResponsesPath(req.Path) {
		return RequestBodyStream
	}
	if !isJSONContentType(req.ContentType, req.Headers.Get("Content-Type")) {
		return RequestBodyStream
	}
	return RequestBodyBuffer
}

func (m *APITranslate) apiFormat() string {
	if m.config.Params == nil {
		return ""
	}
	value, _ := m.config.Params["api_format"].(string)
	return value
}

func isResponsesPath(path string) bool {
	return path == "/v1/responses" || path == "/responses"
}

func translateResponsesPath(path string) string {
	if path == "/responses" {
		return "/chat/completions"
	}
	return "/v1/chat/completions"
}

func translateResponsesBodyToChat(body map[string]any) map[string]any {
	out := map[string]any{}

	copyIfPresent(out, body, "model")
	copyIfPresent(out, body, "stream")
	copyIfPresent(out, body, "temperature")
	copyIfPresent(out, body, "top_p")
	if value, ok := body["max_output_tokens"]; ok {
		out["max_tokens"] = value
	}
	if metadata, ok := body["metadata"].(map[string]any); ok {
		if userID, ok := metadata["user_id"]; ok {
			out["user"] = userID
		}
	}

	messages := translateInputToMessages(body)
	if len(messages) > 0 {
		out["messages"] = messages
	}
	if tools := translateTools(body["tools"]); len(tools) > 0 {
		out["tools"] = tools
	}
	if toolChoice, ok := translateToolChoice(body["tool_choice"]); ok {
		out["tool_choice"] = toolChoice
	}
	return out
}

func copyIfPresent(dst map[string]any, src map[string]any, key string) {
	if value, ok := src[key]; ok {
		dst[key] = value
	}
}

func translateInputToMessages(body map[string]any) []map[string]any {
	var messages []map[string]any
	if instructions, ok := body["instructions"].(string); ok && instructions != "" {
		messages = append(messages, map[string]any{"role": "system", "content": instructions})
	}

	switch input := body["input"].(type) {
	case string:
		messages = append(messages, map[string]any{"role": "user", "content": input})
	case []any:
		for _, item := range input {
			if message, ok := translateInputItem(item); ok {
				messages = append(messages, message)
			}
		}
	}
	return messages
}

func translateInputItem(item any) (map[string]any, bool) {
	object, ok := item.(map[string]any)
	if !ok {
		return nil, false
	}

	switch object["type"] {
	case "function_call":
		callID, _ := object["call_id"].(string)
		if callID == "" {
			callID, _ = object["id"].(string)
		}
		return map[string]any{
			"role":    "assistant",
			"content": nil,
			"tool_calls": []map[string]any{{
				"id":   callID,
				"type": "function",
				"function": map[string]any{
					"name":      object["name"],
					"arguments": defaultString(object["arguments"], "{}"),
				},
			}},
		}, true
	case "function_call_output":
		return map[string]any{
			"role":         "tool",
			"tool_call_id": defaultString(object["call_id"], ""),
			"content":      defaultString(object["output"], ""),
		}, true
	case "reasoning":
		text := collectReasoningText(object)
		if text == "" {
			return nil, false
		}
		return map[string]any{"role": "system", "content": "[Previous reasoning] " + text}, true
	}

	role, _ := object["role"].(string)
	if role != "user" && role != "assistant" && role != "system" && role != "developer" {
		return nil, false
	}
	if role == "developer" {
		role = "system"
	}
	return map[string]any{
		"role":    role,
		"content": convertResponsesContentToChat(object["content"], role),
	}, true
}

func defaultString(value any, fallback string) string {
	if text, ok := value.(string); ok {
		return text
	}
	return fallback
}

func collectReasoningText(object map[string]any) string {
	var parts []string
	for _, key := range []string{"content", "summary"} {
		list, ok := object[key].([]any)
		if !ok {
			continue
		}
		for _, part := range list {
			item, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := item["text"].(string); ok && text != "" {
				parts = append(parts, text)
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	joined := parts[0]
	for _, part := range parts[1:] {
		joined += "\n" + part
	}
	return joined
}

func convertResponsesContentToChat(content any, role string) any {
	switch typed := content.(type) {
	case nil:
		return nil
	case string:
		return typed
	case []any:
		parts := make([]map[string]any, 0, len(typed))
		for _, part := range typed {
			converted, ok := convertContentPart(part)
			if ok {
				parts = append(parts, converted)
			}
		}
		if role == "assistant" {
			text := ""
			allText := true
			for _, part := range parts {
				if part["type"] != "text" {
					allText = false
					break
				}
				text += defaultString(part["text"], "")
			}
			if allText {
				return text
			}
		}
		return parts
	default:
		return typed
	}
}

func convertContentPart(part any) (map[string]any, bool) {
	if text, ok := part.(string); ok {
		return map[string]any{"type": "text", "text": text}, true
	}
	object, ok := part.(map[string]any)
	if !ok {
		return nil, false
	}
	switch object["type"] {
	case "output_text", "input_text", "text":
		return map[string]any{"type": "text", "text": defaultString(object["text"], "")}, true
	case "input_image":
		url := defaultString(object["image_url"], "")
		if url == "" {
			url = defaultString(object["url"], "")
		}
		return map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}, true
	case "refusal":
		return nil, false
	default:
		return object, true
	}
}

func translateTools(value any) []map[string]any {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, tool := range list {
		object, ok := tool.(map[string]any)
		if !ok || object["type"] != "function" {
			continue
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        object["name"],
				"description": defaultString(object["description"], ""),
				"parameters":  defaultParameters(object["parameters"]),
			},
		})
	}
	return out
}

func defaultParameters(value any) any {
	if value != nil {
		return value
	}
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func translateToolChoice(value any) (any, bool) {
	if value == nil {
		return nil, false
	}
	object, ok := value.(map[string]any)
	if !ok || object["type"] != "function" {
		return value, true
	}
	name := object["name"]
	if function, ok := object["function"].(map[string]any); ok && function["name"] != nil {
		name = function["name"]
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": name,
		},
	}, true
}
