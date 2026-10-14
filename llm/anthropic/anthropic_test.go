package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/rizgust/aiassist/llm"
)

func TestComplete_RequestShapeAndResponseMapping(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("bad request json: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5",
			"stop_reason":"tool_use",
			"content":[
				{"type":"thinking","thinking":"","signature":"sig123"},
				{"type":"text","text":"Checking."},
				{"type":"tool_use","id":"tu_1","name":"lookup","input":{"month":"2026-09"}}
			],
			"usage":{"input_tokens":120,"output_tokens":30,"cache_read_input_tokens":1000,"cache_creation_input_tokens":200}
		}`))
	}))
	defer srv.Close()

	p := NewWithAPIKey("test-key", option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	resp, err := p.Complete(context.Background(), llm.Request{
		Model:     "claude-sonnet-5-5",
		MaxTokens: 16000,
		Effort:    "low",
		System:    []llm.SystemBlock{{Text: "stable", Cache: true}, {Text: "today is X"}},
		Tools: []llm.ToolDef{{Name: "lookup", Description: "d", Schema: json.RawMessage(
			`{"type":"object","properties":{"month":{"type":"string"}},"required":["month"],"additionalProperties":false}`)}},
		Messages: []llm.Message{
			llm.UserText("q"),
			{Role: llm.RoleAssistant, Content: []llm.Block{
				{Type: llm.BlockThinking, Signature: "s0"},
				{Type: llm.BlockToolUse, ToolUseID: "tu_0", ToolName: "lookup", Input: json.RawMessage(`{"month":"2026-08"}`)},
			}},
			{Role: llm.RoleUser, Content: []llm.Block{llm.ToolResult("tu_0", "42", false)}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Request shape.
	system := body["system"].([]any)
	if _, ok := system[0].(map[string]any)["cache_control"]; !ok {
		t.Error("first system block should carry cache_control")
	}
	if _, ok := system[1].(map[string]any)["cache_control"]; ok {
		t.Error("volatile system block must not be cached")
	}
	if body["output_config"].(map[string]any)["effort"] != "low" {
		t.Errorf("effort not sent: %v", body["output_config"])
	}
	if _, ok := body["temperature"]; ok {
		t.Error("temperature must never be sent")
	}
	if _, ok := body["thinking"]; ok {
		t.Error("thinking must be left unset")
	}
	schema := body["tools"].([]any)[0].(map[string]any)["input_schema"].(map[string]any)
	if schema["additionalProperties"] != false || schema["required"].([]any)[0] != "month" {
		t.Errorf("schema not round-tripped: %v", schema)
	}
	msgs := body["messages"].([]any)
	asst := msgs[1].(map[string]any)["content"].([]any)
	if asst[0].(map[string]any)["type"] != "thinking" || asst[0].(map[string]any)["signature"] != "s0" {
		t.Errorf("thinking block not passed back: %v", asst[0])
	}
	if res := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any); res["tool_use_id"] != "tu_0" {
		t.Errorf("tool_result = %v", res)
	}

	// Response mapping.
	if resp.StopReason != llm.StopToolUse {
		t.Errorf("stop = %s", resp.StopReason)
	}
	if resp.Usage != (llm.Usage{InputTokens: 120, OutputTokens: 30, CacheReadTokens: 1000, CacheWriteTokens: 200}) {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if len(resp.Content) != 3 || resp.Content[0].Signature != "sig123" || resp.Text() != "Checking." {
		t.Errorf("content = %+v", resp.Content)
	}
	uses := resp.ToolUses()
	if len(uses) != 1 || uses[0].ToolUseID != "tu_1" || string(uses[0].Input) != `{"month":"2026-09"}` {
		t.Errorf("tool uses = %+v", uses)
	}
}

func TestComplete_RefusalAndHTTPError(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusOK {
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"x","stop_reason":"refusal",
			"content":[],"usage":{"input_tokens":5,"output_tokens":0}}`))
	}))
	defer srv.Close()
	p := NewWithAPIKey("k", option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	req := llm.Request{Model: "claude-opus-5-5", MaxTokens: 10, Messages: []llm.Message{llm.UserText("q")}}

	resp, err := p.Complete(context.Background(), req)
	if err != nil || resp.StopReason != llm.StopRefusal {
		t.Fatalf("refusal: %+v %v", resp, err)
	}
	status = 529
	if _, err := p.Complete(context.Background(), req); err == nil {
		t.Fatal("expected error on 529")
	}
}
