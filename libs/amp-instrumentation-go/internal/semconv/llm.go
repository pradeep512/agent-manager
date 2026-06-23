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

// Package semconv builds the per-span-kind OTel GenAI attribute sets that the
// AMP observer reads. This is the Layer-1 gen_ai.* contract.
//
// The canonical key list is in traces-observer-service/cmd/gen-contract/contract.go.
package semconv

import "go.opentelemetry.io/otel/attribute"

// LLMAttrs holds the inputs needed to build a chat-span attribute set for the
// "llm" span kind. Nil/zero optional fields are omitted from the output.
type LLMAttrs struct {
	// System is the AI provider / model vendor (gen_ai.system). Required.
	System string
	// RequestModel is the model requested by the caller (gen_ai.request.model). Required.
	RequestModel string
	// ResponseModel is the model that actually responded (gen_ai.response.model). Optional.
	ResponseModel string
	// Temperature is the sampling temperature (gen_ai.request.temperature). Optional; ≤0 omits.
	Temperature float64
	// SetTemperature must be true for Temperature to be emitted (allows explicit 0.0).
	SetTemperature bool
	// InputMessages is the JSON-encoded input message array. Optional.
	InputMessages string
	// OutputMessages is the JSON-encoded output message array. Optional.
	OutputMessages string
	// InputTokens is gen_ai.usage.input_tokens. Required by observer.
	InputTokens int64
	// OutputTokens is gen_ai.usage.output_tokens. Required by observer.
	OutputTokens int64
	// CacheReadInputTokens is optional; omitted when 0.
	CacheReadInputTokens int64
	// CacheCreationInputTokens is optional; omitted when 0.
	CacheCreationInputTokens int64
}

// LLMAttributes returns the complete attribute set for an LLM chat span. The
// span name must be "chat" and gen_ai.operation.name is always "chat".
//
// The returned slice follows the published contract order:
//  1. gen_ai.operation.name = "chat"          (required)
//  2. gen_ai.system                            (required)
//  3. gen_ai.request.model                     (required)
//  4. gen_ai.response.model                    (optional)
//  5. gen_ai.request.temperature               (optional)
//  6. gen_ai.input.messages                    (optional)
//  7. gen_ai.output.messages                   (optional)
//  8. gen_ai.usage.input_tokens                (required)
//  9. gen_ai.usage.output_tokens               (required)
//  10. gen_ai.usage.cache_read_input_tokens    (optional)
//  11. gen_ai.usage.cache_creation_input_tokens (optional)
func LLMAttributes(a LLMAttrs) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.system", a.System),
		attribute.String("gen_ai.request.model", a.RequestModel),
	}
	if a.ResponseModel != "" {
		attrs = append(attrs, attribute.String("gen_ai.response.model", a.ResponseModel))
	}
	if a.SetTemperature {
		attrs = append(attrs, attribute.Float64("gen_ai.request.temperature", a.Temperature))
	}
	if a.InputMessages != "" {
		attrs = append(attrs, attribute.String("gen_ai.input.messages", a.InputMessages))
	}
	if a.OutputMessages != "" {
		attrs = append(attrs, attribute.String("gen_ai.output.messages", a.OutputMessages))
	}
	attrs = append(attrs,
		attribute.Int64("gen_ai.usage.input_tokens", a.InputTokens),
		attribute.Int64("gen_ai.usage.output_tokens", a.OutputTokens),
	)
	if a.CacheReadInputTokens > 0 {
		attrs = append(attrs, attribute.Int64("gen_ai.usage.cache_read_input_tokens", a.CacheReadInputTokens))
	}
	if a.CacheCreationInputTokens > 0 {
		attrs = append(attrs, attribute.Int64("gen_ai.usage.cache_creation_input_tokens", a.CacheCreationInputTokens))
	}
	return attrs
}
