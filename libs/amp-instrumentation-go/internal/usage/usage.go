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
// # Issue #10 — Full Anthropic cache-token fidelity
//
// This package captures the COMPLETE Anthropic usage breakdown, including the
// ephemeral cache-creation split and provider metadata fields. The keys emitted
// by Attributes() are:
//
// Standard (observer-recognized) keys:
//   - gen_ai.usage.input_tokens                (required; raw uncached input)
//   - gen_ai.usage.output_tokens               (required)
//   - gen_ai.usage.cache_read_input_tokens     (optional; Anthropic cache hit)
//   - gen_ai.usage.cache_creation_input_tokens (optional; total cache write)
//
// Extended keys (token counts → gen_ai.usage.* prefix; SDK emits now, observer
// extension planned in issue #14):
//   - gen_ai.usage.cache_creation.ephemeral_1h_input_tokens (optional)
//   - gen_ai.usage.cache_creation.ephemeral_5m_input_tokens (optional)
//
// Provider metadata keys (not token counts → gen_ai.anthropic.* prefix):
//   - gen_ai.anthropic.service_tier   (optional; e.g. "standard")
//   - gen_ai.anthropic.inference_geo  (optional; e.g. "global")
//
// Design rules:
//   - InputTokens is always the raw (uncached) value — never add cache hits/writes
//     to it. Cost computation requires the separate keys.
//   - Token-count fields (cache split) sit under gen_ai.usage.* for consistency
//     with OTel GenAI conventions.
//   - Non-token metadata sits under gen_ai.anthropic.* (provider-namespaced,
//     non-standardised by OTel).
//   - All optional fields are omitted (not set to 0) when absent, matching the
//     existing behaviour of cache_read_input_tokens.
package usage

import "go.opentelemetry.io/otel/attribute"

// LLMUsage holds normalized token counts for one LLM call.
//
// InputTokens is always the raw (uncached) input count so that per-call cost
// can be computed correctly with the cache discount applied separately via
// CacheReadInputTokens / CacheCreationInputTokens.
//
// The ephemeral split (CacheCreationEphemeral1hInputTokens /
// CacheCreationEphemeral5mInputTokens) reflects Anthropic's cache_creation
// sub-object. Their sum equals CacheCreationInputTokens (the total cache write).
// They are priced differently (1h storage vs 5m storage) so both are captured.
type LLMUsage struct {
	// InputTokens is the raw uncached prompt token count.
	// Anthropic field: usage.input_tokens.
	InputTokens int64
	// OutputTokens is the number of generated tokens.
	// Anthropic field: usage.output_tokens.
	OutputTokens int64
	// CacheReadInputTokens is the number of tokens read from the prompt cache.
	// Anthropic field: usage.cache_read_input_tokens. Optional; zero when absent.
	// Emitted as: gen_ai.usage.cache_read_input_tokens
	CacheReadInputTokens int64
	// CacheCreationInputTokens is the total number of tokens written to the
	// prompt cache (cache write / warm). Anthropic field:
	// usage.cache_creation_input_tokens. Optional; zero when absent.
	// Emitted as: gen_ai.usage.cache_creation_input_tokens
	CacheCreationInputTokens int64
	// CacheCreationEphemeral1hInputTokens is the subset of cache-creation tokens
	// stored in the 1-hour ephemeral tier.
	// Anthropic field: usage.cache_creation.ephemeral_1h_input_tokens.
	// Optional; zero when absent.
	// Emitted as: gen_ai.usage.cache_creation.ephemeral_1h_input_tokens
	CacheCreationEphemeral1hInputTokens int64
	// CacheCreationEphemeral5mInputTokens is the subset of cache-creation tokens
	// stored in the 5-minute ephemeral tier.
	// Anthropic field: usage.cache_creation.ephemeral_5m_input_tokens.
	// Optional; zero when absent.
	// Emitted as: gen_ai.usage.cache_creation.ephemeral_5m_input_tokens
	CacheCreationEphemeral5mInputTokens int64
	// ServiceTier is the Anthropic pricing/quality tier for this request.
	// Anthropic field: usage.service_tier (e.g. "standard").
	// Optional; empty string omitted.
	// Emitted as: gen_ai.anthropic.service_tier
	ServiceTier string
	// InferenceGeo is the Anthropic inference geography for this request.
	// Anthropic field: usage.inference_geo (e.g. "global").
	// Optional; empty string omitted.
	// Emitted as: gen_ai.anthropic.inference_geo
	InferenceGeo string
}

// Attributes returns the OTel span attributes corresponding to this usage.
//
// Required attributes (always emitted):
//   - gen_ai.usage.input_tokens
//   - gen_ai.usage.output_tokens
//
// Optional attributes (emitted only when non-zero / non-empty):
//   - gen_ai.usage.cache_read_input_tokens
//   - gen_ai.usage.cache_creation_input_tokens
//   - gen_ai.usage.cache_creation.ephemeral_1h_input_tokens
//   - gen_ai.usage.cache_creation.ephemeral_5m_input_tokens
//   - gen_ai.anthropic.service_tier
//   - gen_ai.anthropic.inference_geo
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
	// Ephemeral cache-creation split (Anthropic cache_creation sub-object).
	// Token counts → gen_ai.usage.* prefix.
	if u.CacheCreationEphemeral1hInputTokens > 0 {
		attrs = append(attrs, attribute.Int64("gen_ai.usage.cache_creation.ephemeral_1h_input_tokens", u.CacheCreationEphemeral1hInputTokens))
	}
	if u.CacheCreationEphemeral5mInputTokens > 0 {
		attrs = append(attrs, attribute.Int64("gen_ai.usage.cache_creation.ephemeral_5m_input_tokens", u.CacheCreationEphemeral5mInputTokens))
	}
	// Provider metadata → gen_ai.anthropic.* prefix (not token counts).
	if u.ServiceTier != "" {
		attrs = append(attrs, attribute.String("gen_ai.anthropic.service_tier", u.ServiceTier))
	}
	if u.InferenceGeo != "" {
		attrs = append(attrs, attribute.String("gen_ai.anthropic.inference_geo", u.InferenceGeo))
	}
	return attrs
}
