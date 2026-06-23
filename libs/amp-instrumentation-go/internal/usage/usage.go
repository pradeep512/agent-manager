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

// Package usage normalises provider token-usage data into the AMP/OTel
// gen_ai.usage.* attribute set.
//
// Scope (issue #2): the three keys recognized by the current observer:
//   - gen_ai.usage.input_tokens   (required; raw uncached input)
//   - gen_ai.usage.output_tokens  (required)
//   - gen_ai.usage.cache_read_input_tokens (optional)
//
// Full Anthropic cache-creation/ephemeral fields are intentionally captured
// here so they can be emitted as extra attributes even before the observer
// extension lands in a follow-up PR.
package usage

import "go.opentelemetry.io/otel/attribute"

// LLMUsage holds normalized token counts for one LLM call.
//
// InputTokens is always the raw (uncached) input count so that per-call cost
// can be computed correctly with the cache discount applied separately via
// CacheReadInputTokens / CacheCreationInputTokens.
type LLMUsage struct {
	// InputTokens is the raw uncached prompt token count.
	InputTokens int64
	// OutputTokens is the number of generated tokens.
	OutputTokens int64
	// CacheReadInputTokens is the number of tokens read from the prompt cache
	// (Anthropic: cache_read_input_tokens). Optional; zero when absent.
	CacheReadInputTokens int64
	// CacheCreationInputTokens is the number of tokens written to the prompt
	// cache (Anthropic: cache_creation_input_tokens). Optional; zero when absent.
	CacheCreationInputTokens int64
}

// Attributes returns the OTel span attributes corresponding to this usage,
// using gen_ai.usage.* keys. Only non-zero optional fields are included.
func (u LLMUsage) Attributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.Int64("gen_ai.usage.input_tokens", u.InputTokens),
		attribute.Int64("gen_ai.usage.output_tokens", u.OutputTokens),
	}
	if u.CacheReadInputTokens > 0 {
		attrs = append(attrs, attribute.Int64("gen_ai.usage.cache_read_input_tokens", u.CacheReadInputTokens))
	}
	if u.CacheCreationInputTokens > 0 {
		attrs = append(attrs, attribute.Int64("gen_ai.usage.cache_creation_input_tokens", u.CacheCreationInputTokens))
	}
	return attrs
}
