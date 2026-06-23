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
