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

package usage_test

import (
	"testing"

	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/usage"
)

func TestLLMUsage_Attributes_RequiredKeys(t *testing.T) {
	u := usage.LLMUsage{InputTokens: 100, OutputTokens: 50}
	attrs := u.Attributes()

	found := map[string]int64{}
	for _, a := range attrs {
		found[string(a.Key)] = a.Value.AsInt64()
	}

	if v, ok := found["gen_ai.usage.input_tokens"]; !ok || v != 100 {
		t.Errorf("gen_ai.usage.input_tokens: got %v (present=%v)", v, ok)
	}
	if v, ok := found["gen_ai.usage.output_tokens"]; !ok || v != 50 {
		t.Errorf("gen_ai.usage.output_tokens: got %v (present=%v)", v, ok)
	}
}

func TestLLMUsage_Attributes_OptionalCacheReadIncluded(t *testing.T) {
	u := usage.LLMUsage{InputTokens: 100, OutputTokens: 50, CacheReadInputTokens: 30}
	attrs := u.Attributes()

	found := map[string]int64{}
	for _, a := range attrs {
		found[string(a.Key)] = a.Value.AsInt64()
	}

	if v, ok := found["gen_ai.usage.cache_read_input_tokens"]; !ok || v != 30 {
		t.Errorf("cache_read_input_tokens: got %v (present=%v)", v, ok)
	}
}

func TestLLMUsage_Attributes_OptionalCacheReadOmittedWhenZero(t *testing.T) {
	u := usage.LLMUsage{InputTokens: 100, OutputTokens: 50}
	attrs := u.Attributes()

	for _, a := range attrs {
		if string(a.Key) == "gen_ai.usage.cache_read_input_tokens" {
			t.Error("cache_read_input_tokens should be omitted when zero")
		}
	}
}

func TestLLMUsage_Attributes_CacheCreationIncluded(t *testing.T) {
	u := usage.LLMUsage{
		InputTokens:              100,
		OutputTokens:             50,
		CacheCreationInputTokens: 80,
	}
	attrs := u.Attributes()

	found := map[string]int64{}
	for _, a := range attrs {
		found[string(a.Key)] = a.Value.AsInt64()
	}

	if v, ok := found["gen_ai.usage.cache_creation_input_tokens"]; !ok || v != 80 {
		t.Errorf("cache_creation_input_tokens: got %v (present=%v)", v, ok)
	}
}

func TestLLMUsage_Attributes_InputTokensRawUncached(t *testing.T) {
	// Confirm that InputTokens is the raw prompt count, not cache-adjusted.
	// We set both input and cache_read to ensure they are separate keys.
	u := usage.LLMUsage{
		InputTokens:          200,
		OutputTokens:         40,
		CacheReadInputTokens: 150,
	}
	attrs := u.Attributes()
	found := map[string]int64{}
	for _, a := range attrs {
		found[string(a.Key)] = a.Value.AsInt64()
	}

	if found["gen_ai.usage.input_tokens"] != 200 {
		t.Errorf("input_tokens should be raw 200, got %d", found["gen_ai.usage.input_tokens"])
	}
	if found["gen_ai.usage.cache_read_input_tokens"] != 150 {
		t.Errorf("cache_read_input_tokens should be 150, got %d", found["gen_ai.usage.cache_read_input_tokens"])
	}
}

// TestLLMUsage_Attributes_EphemeralSplit verifies that the Anthropic ephemeral
// cache-creation split fields are emitted as separate gen_ai.usage.* keys when
// non-zero, and omitted when zero.
//
// Chosen key names (not yet standardised by OTel):
//   gen_ai.usage.cache_creation.ephemeral_1h_input_tokens
//   gen_ai.usage.cache_creation.ephemeral_5m_input_tokens
//
// These sit under gen_ai.usage.* because they are token counts used for cost
// computation (1h storage tier vs 5m storage tier pricing differs).
func TestLLMUsage_Attributes_EphemeralSplit(t *testing.T) {
	u := usage.LLMUsage{
		InputTokens:                          1,
		OutputTokens:                         38,
		CacheReadInputTokens:                 6270,
		CacheCreationInputTokens:             222,
		CacheCreationEphemeral1hInputTokens:  0,
		CacheCreationEphemeral5mInputTokens:  222,
	}
	attrs := u.Attributes()

	found := map[string]int64{}
	foundStr := map[string]string{}
	for _, a := range attrs {
		switch a.Value.Type().String() {
		case "STRING":
			foundStr[string(a.Key)] = a.Value.AsString()
		default:
			found[string(a.Key)] = a.Value.AsInt64()
		}
	}

	// cache_creation.ephemeral_5m_input_tokens should be present (222)
	if v, ok := found["gen_ai.usage.cache_creation.ephemeral_5m_input_tokens"]; !ok || v != 222 {
		t.Errorf("cache_creation.ephemeral_5m_input_tokens: got %v (present=%v), want 222", v, ok)
	}
	// cache_creation.ephemeral_1h_input_tokens should be OMITTED (0)
	if _, ok := found["gen_ai.usage.cache_creation.ephemeral_1h_input_tokens"]; ok {
		t.Error("cache_creation.ephemeral_1h_input_tokens should be absent when zero")
	}
}

// TestLLMUsage_Attributes_ServiceTierAndGeo verifies that Anthropic metadata
// fields are emitted under gen_ai.* keys (not gen_ai.usage.* since they are not
// token counts).
//
// Chosen key names:
//   gen_ai.anthropic.service_tier    — the pricing/quality tier (e.g. "standard")
//   gen_ai.anthropic.inference_geo   — the inference geography (e.g. "global")
//
// These are provider-specific metadata, not token counts, so they sit under
// gen_ai.anthropic.* rather than gen_ai.usage.*.
func TestLLMUsage_Attributes_ServiceTierAndGeo(t *testing.T) {
	u := usage.LLMUsage{
		InputTokens:  1,
		OutputTokens: 38,
		ServiceTier:  "standard",
		InferenceGeo: "global",
	}
	attrs := u.Attributes()

	foundStr := map[string]string{}
	for _, a := range attrs {
		if a.Value.Type().String() == "STRING" {
			foundStr[string(a.Key)] = a.Value.AsString()
		}
	}

	if v, ok := foundStr["gen_ai.anthropic.service_tier"]; !ok || v != "standard" {
		t.Errorf("gen_ai.anthropic.service_tier: got %q (present=%v), want 'standard'", v, ok)
	}
	if v, ok := foundStr["gen_ai.anthropic.inference_geo"]; !ok || v != "global" {
		t.Errorf("gen_ai.anthropic.inference_geo: got %q (present=%v), want 'global'", v, ok)
	}
}

// TestLLMUsage_Attributes_FullAnthropicShape verifies the complete real-world
// Anthropic usage response is captured with correct key names and values, and
// that gen_ai.usage.input_tokens remains the raw uncached value.
//
// Based on the real Anthropic usage shape from issue #10:
//
//	"usage": {
//	  "input_tokens": 1,
//	  "output_tokens": 38,
//	  "cache_read_input_tokens": 6270,
//	  "cache_creation_input_tokens": 222,
//	  "cache_creation": { "ephemeral_1h_input_tokens": 0, "ephemeral_5m_input_tokens": 222 },
//	  "service_tier": "standard",
//	  "inference_geo": "global"
//	}
func TestLLMUsage_Attributes_FullAnthropicShape(t *testing.T) {
	u := usage.LLMUsage{
		InputTokens:                         1,
		OutputTokens:                        38,
		CacheReadInputTokens:                6270,
		CacheCreationInputTokens:            222,
		CacheCreationEphemeral1hInputTokens: 0,
		CacheCreationEphemeral5mInputTokens: 222,
		ServiceTier:                         "standard",
		InferenceGeo:                        "global",
	}
	attrs := u.Attributes()

	foundInt := map[string]int64{}
	foundStr := map[string]string{}
	for _, a := range attrs {
		switch a.Value.Type().String() {
		case "STRING":
			foundStr[string(a.Key)] = a.Value.AsString()
		default:
			foundInt[string(a.Key)] = a.Value.AsInt64()
		}
	}

	// Raw input must be 1 (not cache-adjusted).
	if v := foundInt["gen_ai.usage.input_tokens"]; v != 1 {
		t.Errorf("input_tokens = %d, want 1 (raw uncached)", v)
	}
	if v := foundInt["gen_ai.usage.output_tokens"]; v != 38 {
		t.Errorf("output_tokens = %d, want 38", v)
	}
	if v := foundInt["gen_ai.usage.cache_read_input_tokens"]; v != 6270 {
		t.Errorf("cache_read_input_tokens = %d, want 6270", v)
	}
	if v := foundInt["gen_ai.usage.cache_creation_input_tokens"]; v != 222 {
		t.Errorf("cache_creation_input_tokens = %d, want 222", v)
	}
	// Ephemeral 5m present (non-zero), 1h absent (zero).
	if v := foundInt["gen_ai.usage.cache_creation.ephemeral_5m_input_tokens"]; v != 222 {
		t.Errorf("cache_creation.ephemeral_5m_input_tokens = %d, want 222", v)
	}
	if _, ok := foundInt["gen_ai.usage.cache_creation.ephemeral_1h_input_tokens"]; ok {
		t.Error("cache_creation.ephemeral_1h_input_tokens should be absent when zero")
	}
	// Metadata.
	if v := foundStr["gen_ai.anthropic.service_tier"]; v != "standard" {
		t.Errorf("service_tier = %q, want 'standard'", v)
	}
	if v := foundStr["gen_ai.anthropic.inference_geo"]; v != "global" {
		t.Errorf("inference_geo = %q, want 'global'", v)
	}

	// input_tokens must NOT equal cache_read + cache_creation (raw-only rule).
	if foundInt["gen_ai.usage.input_tokens"] == foundInt["gen_ai.usage.cache_read_input_tokens"] {
		t.Error("input_tokens must be raw uncached, not equal to cache_read_input_tokens")
	}
}
