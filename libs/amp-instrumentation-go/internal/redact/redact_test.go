// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package redact_test

import (
	"encoding/json"
	"testing"

	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/redact"
)

func TestText_ContentEnabled(t *testing.T) {
	got := redact.Text("hello world", true)
	if got != "hello world" {
		t.Errorf("expected original text, got %q", got)
	}
}

func TestText_ContentDisabled(t *testing.T) {
	got := redact.Text("sensitive data", false)
	if got != redact.Placeholder {
		t.Errorf("expected %q, got %q", redact.Placeholder, got)
	}
}

func TestMessages_ContentEnabled(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "What is 2+2?"},
	}
	got := redact.Messages(msgs, true)
	var out []map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if out[0]["content"] != "What is 2+2?" {
		t.Errorf("content should be preserved, got %q", out[0]["content"])
	}
	if out[0]["role"] != "user" {
		t.Errorf("role should be preserved, got %q", out[0]["role"])
	}
}

func TestMessages_ContentDisabled_TextRedacted(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "Sensitive prompt"},
		{"role": "assistant", "content": "Sensitive response"},
	}
	got := redact.Messages(msgs, false)
	var out []map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for i, m := range out {
		if m["content"] != redact.Placeholder {
			t.Errorf("msg[%d].content should be %q, got %q", i, redact.Placeholder, m["content"])
		}
		// role must be preserved
		if m["role"] == "" {
			t.Errorf("msg[%d].role should be preserved", i)
		}
	}
}

func TestMessages_ContentDisabled_RolePreserved(t *testing.T) {
	msgs := []map[string]any{
		{"role": "system", "content": "You are a helpful assistant."},
	}
	got := redact.Messages(msgs, false)
	var out []map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if out[0]["role"] != "system" {
		t.Errorf("role should be preserved, got %q", out[0]["role"])
	}
	if out[0]["content"] != redact.Placeholder {
		t.Errorf("content should be redacted, got %q", out[0]["content"])
	}
}

func TestMessages_ContentDisabled_ToolCallArgsRedacted(t *testing.T) {
	msgs := []map[string]any{
		{
			"role":    "assistant",
			"content": nil,
			"tool_calls": []any{
				map[string]any{
					"id":   "call_123",
					"type": "function",
					"function": map[string]any{
						"name":      "get_weather",
						"arguments": `{"location":"London"}`,
					},
				},
			},
		},
	}
	got := redact.Messages(msgs, false)
	var out []map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	calls, ok := out[0]["tool_calls"].([]any)
	if !ok || len(calls) == 0 {
		t.Fatalf("tool_calls missing or empty")
	}
	call := calls[0].(map[string]any)
	fn := call["function"].(map[string]any)
	if fn["arguments"] != redact.Placeholder {
		t.Errorf("tool call arguments should be redacted, got %q", fn["arguments"])
	}
	// name should be preserved
	if fn["name"] != "get_weather" {
		t.Errorf("tool call name should be preserved, got %q", fn["name"])
	}
}

func TestMessages_ContentDisabled_OriginalUnmodified(t *testing.T) {
	// Ensure the original slice is not mutated.
	msgs := []map[string]any{
		{"role": "user", "content": "original"},
	}
	redact.Messages(msgs, false)
	if msgs[0]["content"] != "original" {
		t.Error("original msgs slice must not be mutated")
	}
}

// TestValue_ContentEnabled asserts that Value returns the input unchanged when
// content tracing is on.
func TestValue_ContentEnabled(t *testing.T) {
	got := redact.Value(map[string]any{"key": "secret"}, true)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", got)
	}
	if m["key"] != "secret" {
		t.Errorf("expected 'secret', got %v", m["key"])
	}
}

// TestValue_ContentDisabled_StringRedacted asserts that a string value is
// replaced with the placeholder when content tracing is off.
func TestValue_ContentDisabled_StringRedacted(t *testing.T) {
	got := redact.Value("sensitive", false)
	if got != redact.Placeholder {
		t.Errorf("expected %q, got %v", redact.Placeholder, got)
	}
}

// TestValue_ContentDisabled_MapValuesRedacted asserts that string values inside
// a map are redacted while the map structure (keys) is preserved.
func TestValue_ContentDisabled_MapValuesRedacted(t *testing.T) {
	input := map[string]any{
		"location": "London",
		"units":    "celsius",
	}
	got := redact.Value(input, false)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", got)
	}
	if m["location"] != redact.Placeholder {
		t.Errorf("map string value should be redacted, got %v", m["location"])
	}
	if m["units"] != redact.Placeholder {
		t.Errorf("map string value should be redacted, got %v", m["units"])
	}
	// Keys must exist (structure preserved).
	if _, ok := m["location"]; !ok {
		t.Error("key 'location' should still exist in redacted map")
	}
}

// TestValue_ContentDisabled_ListItemsRedacted asserts that string elements inside
// a slice are redacted.
func TestValue_ContentDisabled_ListItemsRedacted(t *testing.T) {
	input := []any{"item1", "item2"}
	got := redact.Value(input, false)
	s, ok := got.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", got)
	}
	for i, v := range s {
		if v != redact.Placeholder {
			t.Errorf("slice[%d] should be %q, got %v", i, redact.Placeholder, v)
		}
	}
}

// TestValue_ContentDisabled_NonStringPreserved asserts that non-string scalars
// (numbers, booleans) are not redacted.
func TestValue_ContentDisabled_NonStringPreserved(t *testing.T) {
	input := map[string]any{
		"count": 42,
		"flag":  true,
	}
	got := redact.Value(input, false)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", got)
	}
	if m["count"] != 42 {
		t.Errorf("integer should be preserved, got %v", m["count"])
	}
	if m["flag"] != true {
		t.Errorf("bool should be preserved, got %v", m["flag"])
	}
}
