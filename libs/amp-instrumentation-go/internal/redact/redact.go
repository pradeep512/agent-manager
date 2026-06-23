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

// Package redact applies the AMP_TRACE_CONTENT redaction rule.
//
// When content tracing is disabled, free-text fields (prompt text, completion
// text, tool arguments/results, system instructions) are replaced with the
// literal string "[redacted]". Message roles, structure, token counts, and
// model names are preserved so the trace view stays readable.
//
// This mirrors the Python helper in
// samples/manual-instrumentation-agent/instrumentation.py.
package redact

import "encoding/json"

// Placeholder replaces content when tracing is disabled.
const Placeholder = "[redacted]"

// Text returns the text unchanged when contentEnabled is true, or the
// Placeholder string when false.
func Text(text string, contentEnabled bool) string {
	if contentEnabled {
		return text
	}
	return Placeholder
}

// Messages serialises a slice of chat messages to JSON. When contentEnabled is
// false the "content" field of each message is replaced with "[redacted]" and
// tool call arguments inside the message are also redacted. The role, id, and
// structural fields are preserved.
//
// msgs must be a slice of map[string]any compatible with the chat message
// schema used by AMP. Each map may contain:
//
//	"role"       string  – preserved
//	"content"    any     – redacted when off
//	"tool_calls" []any   – arguments inside each call redacted when off
func Messages(msgs []map[string]any, contentEnabled bool) string {
	if contentEnabled {
		b, _ := json.Marshal(msgs)
		return string(b)
	}

	redacted := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		redacted[i] = redactMessage(m)
	}
	b, _ := json.Marshal(redacted)
	return string(b)
}

// redactMessage returns a shallow copy of msg with content and tool-call
// arguments replaced by Placeholder.
func redactMessage(msg map[string]any) map[string]any {
	out := make(map[string]any, len(msg))
	for k, v := range msg {
		out[k] = v
	}
	if _, ok := out["content"]; ok {
		out["content"] = Placeholder
	}
	if calls, ok := out["tool_calls"].([]any); ok {
		redactedCalls := make([]any, len(calls))
		for i, c := range calls {
			if callMap, ok := c.(map[string]any); ok {
				callCopy := make(map[string]any, len(callMap))
				for k, v := range callMap {
					callCopy[k] = v
				}
				if fn, ok := callCopy["function"].(map[string]any); ok {
					fnCopy := make(map[string]any, len(fn))
					for k, v := range fn {
						fnCopy[k] = v
					}
					fnCopy["arguments"] = Placeholder
					callCopy["function"] = fnCopy
				}
				redactedCalls[i] = callCopy
			} else {
				redactedCalls[i] = c
			}
		}
		out["tool_calls"] = redactedCalls
	}
	return out
}
