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
