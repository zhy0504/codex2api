package proxy

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestTranslateRequest_ConvertsMessagesToolsAndFlags(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5.4",
		"messages":[
			{"role":"system","content":"You are a helpful assistant."},
			{"role":"user","content":"What is the weather in Shanghai?"}
		],
		"tools":[
			{
				"type":"function",
				"function":{
					"name":"get_weather",
					"description":"Get weather by city",
					"parameters":{
						"type":"object",
						"properties":{
							"city":{"type":"string"}
						},
						"required":["city"]
					},
					"strict":true
				}
			}
		],
		"tool_choice":"auto"
	}`)

	translated, err := TranslateRequest(raw)
	if err != nil {
		t.Fatalf("TranslateRequest returned error: %v", err)
	}

	if gjson.GetBytes(translated, "messages").Exists() {
		t.Fatal("messages should be removed after translation")
	}

	input := gjson.GetBytes(translated, "input")
	if !input.Exists() || !input.IsArray() {
		t.Fatal("input should exist as an array after translation")
	}
	if count := int(input.Get("#").Int()); count != 2 {
		t.Fatalf("input length mismatch: got %d want %d", count, 2)
	}
	if role := gjson.GetBytes(translated, "input.0.role").String(); role != "developer" {
		t.Fatalf("system role should become developer: got %q", role)
	}
	if role := gjson.GetBytes(translated, "input.1.role").String(); role != "user" {
		t.Fatalf("input user role mismatch: got %q want %q", role, "user")
	}

	if name := gjson.GetBytes(translated, "tools.0.name").String(); name != "get_weather" {
		t.Fatalf("tool name mismatch: got %q want %q", name, "get_weather")
	}
	if desc := gjson.GetBytes(translated, "tools.0.description").String(); desc != "Get weather by city" {
		t.Fatalf("tool description mismatch: got %q want %q", desc, "Get weather by city")
	}
	if paramType := gjson.GetBytes(translated, "tools.0.parameters.type").String(); paramType != "object" {
		t.Fatalf("tool parameters.type mismatch: got %q want %q", paramType, "object")
	}
	if strict := gjson.GetBytes(translated, "tools.0.strict"); !strict.Exists() || !strict.Bool() {
		t.Fatalf("tool strict mismatch: got %v want %v", strict.Value(), true)
	}
	if gjson.GetBytes(translated, "tools.0.function").Exists() {
		t.Fatal("tools[0].function should be removed after flattening")
	}

	if gjson.GetBytes(translated, "tool_choice").Exists() {
		t.Fatal("tool_choice should be removed after translation")
	}

	if stream := gjson.GetBytes(translated, "stream"); !stream.Exists() || !stream.Bool() {
		t.Fatalf("stream mismatch: got %v want %v", stream.Value(), true)
	}
	if store := gjson.GetBytes(translated, "store"); !store.Exists() || store.Bool() {
		t.Fatalf("store mismatch: got %v want %v", store.Value(), false)
	}

	include := gjson.GetBytes(translated, "include")
	if !include.Exists() || !include.IsArray() {
		t.Fatal("include should exist as an array")
	}

	foundEncryptedContent := false
	include.ForEach(func(_, item gjson.Result) bool {
		if item.String() == "reasoning.encrypted_content" {
			foundEncryptedContent = true
		}
		return true
	})
	if !foundEncryptedContent {
		t.Fatal("include should contain reasoning.encrypted_content")
	}
}

func TestTranslateStreamChunk_ResponseFailedReturnsTerminalError(t *testing.T) {
	eventData := []byte(`{
		"type":"response.failed",
		"response":{"error":{"message":"upstream request failed"}}
	}`)

	chunk, terminal := TranslateStreamChunk(eventData, "gpt-5.4", "chunk_123")
	if !terminal {
		t.Fatal("response.failed should produce terminal=true")
	}

	if msg := gjson.GetBytes(chunk, "error.message").String(); msg != "upstream request failed" {
		t.Fatalf("error.message mismatch: got %q want %q", msg, "upstream request failed")
	}
}
