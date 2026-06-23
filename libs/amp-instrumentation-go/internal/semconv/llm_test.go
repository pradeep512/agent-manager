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

package semconv_test

import (
	"testing"

	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/semconv"
)

func TestLLMAttributes_RequiredKeys(t *testing.T) {
	a := semconv.LLMAttrs{
		System:       "anthropic",
		RequestModel: "claude-3-5-sonnet-20241022",
		InputTokens:  10,
		OutputTokens: 5,
	}
	attrs := semconv.LLMAttributes(a)
	found := map[string]any{}
	for _, kv := range attrs {
		found[string(kv.Key)] = kv.Value.AsInterface()
	}

	requiredKeys := []string{
		"gen_ai.operation.name",
		"gen_ai.system",
		"gen_ai.request.model",
		"gen_ai.usage.input_tokens",
		"gen_ai.usage.output_tokens",
	}
	for _, k := range requiredKeys {
		if _, ok := found[k]; !ok {
			t.Errorf("required key %q missing", k)
		}
	}
	if found["gen_ai.operation.name"] != "chat" {
		t.Errorf("gen_ai.operation.name = %v, want 'chat'", found["gen_ai.operation.name"])
	}
}

func TestLLMAttributes_TemperatureEmitted_WhenSet(t *testing.T) {
	a := semconv.LLMAttrs{
		System:         "openai",
		RequestModel:   "gpt-4o",
		Temperature:    0.5,
		SetTemperature: true,
	}
	attrs := semconv.LLMAttributes(a)
	found := false
	for _, kv := range attrs {
		if string(kv.Key) == "gen_ai.request.temperature" {
			found = true
			if kv.Value.AsFloat64() != 0.5 {
				t.Errorf("temperature = %v, want 0.5", kv.Value.AsFloat64())
			}
		}
	}
	if !found {
		t.Error("gen_ai.request.temperature should be present when SetTemperature=true")
	}
}

func TestLLMAttributes_TemperatureOmitted_WhenNotSet(t *testing.T) {
	a := semconv.LLMAttrs{
		System:       "openai",
		RequestModel: "gpt-4o",
	}
	attrs := semconv.LLMAttributes(a)
	for _, kv := range attrs {
		if string(kv.Key) == "gen_ai.request.temperature" {
			t.Error("gen_ai.request.temperature should be absent when SetTemperature=false")
		}
	}
}

func TestLLMAttributes_CacheReadIncluded_WhenNonZero(t *testing.T) {
	a := semconv.LLMAttrs{
		System:               "anthropic",
		RequestModel:         "claude-3-5-sonnet-20241022",
		CacheReadInputTokens: 30,
	}
	attrs := semconv.LLMAttributes(a)
	found := false
	for _, kv := range attrs {
		if string(kv.Key) == "gen_ai.usage.cache_read_input_tokens" {
			found = true
			if kv.Value.AsInt64() != 30 {
				t.Errorf("cache_read = %d, want 30", kv.Value.AsInt64())
			}
		}
	}
	if !found {
		t.Error("gen_ai.usage.cache_read_input_tokens should be present")
	}
}

func TestLLMAttributes_CacheReadOmitted_WhenZero(t *testing.T) {
	a := semconv.LLMAttrs{
		System:       "anthropic",
		RequestModel: "claude-3-5-sonnet-20241022",
	}
	attrs := semconv.LLMAttributes(a)
	for _, kv := range attrs {
		if string(kv.Key) == "gen_ai.usage.cache_read_input_tokens" {
			t.Error("cache_read_input_tokens should be absent when zero")
		}
	}
}

func TestLLMAttributes_ResponseModelOptional(t *testing.T) {
	a := semconv.LLMAttrs{
		System:        "anthropic",
		RequestModel:  "claude-3-5-sonnet-20241022",
		ResponseModel: "claude-3-5-sonnet-20241022",
	}
	attrs := semconv.LLMAttributes(a)
	found := false
	for _, kv := range attrs {
		if string(kv.Key) == "gen_ai.response.model" {
			found = true
		}
	}
	if !found {
		t.Error("gen_ai.response.model should be present when ResponseModel is set")
	}

	// When empty, must be absent.
	a.ResponseModel = ""
	attrs = semconv.LLMAttributes(a)
	for _, kv := range attrs {
		if string(kv.Key) == "gen_ai.response.model" {
			t.Error("gen_ai.response.model should be absent when ResponseModel is empty")
		}
	}
}
