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

// Package amp_test contains rollup integration tests for the token accumulator
// (ADR 0001, issue #9).
//
// Tests in this file verify that:
//   - AgentSpan carries the sum of all child LLM/Embedding spans' token usage.
//   - Parallel goroutines fan out correctly without races (run with -race).
//   - gen_ai.usage.input_tokens remains raw uncached; cache totals are separate.
//   - LLMSpan/EmbeddingSpan used without an enclosing AgentSpan remain safe.
package amp_test

import (
	"context"
	"sync"
	"testing"

	amp "github.com/wso2/agent-manager/libs/amp-instrumentation-go"
	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/usage"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// spanByName returns the first ended span with the given name, or nil.
func spanByName(rec *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	for _, s := range rec.Ended() {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// TestRollup_AgentTotalsEqualSumOfLeafs verifies that the agent span's
// gen_ai.usage.* totals equal the arithmetic sum of all child LLM spans.
func TestRollup_AgentTotalsEqualSumOfLeafs(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx, agentSpan, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "rollup-agent"})

	// Child 1 — LLM call with cache read.
	ctx1, llm1, res1 := amp.LLMSpan(ctx, amp.LLMInput{System: "anthropic", RequestModel: "claude-3-5-sonnet"})
	_ = ctx1
	res1.Usage = usage.LLMUsage{
		InputTokens:          100,
		OutputTokens:         50,
		CacheReadInputTokens: 20,
	}
	llm1.End()

	// Child 2 — LLM call with cache creation.
	ctx2, llm2, res2 := amp.LLMSpan(ctx, amp.LLMInput{System: "anthropic", RequestModel: "claude-3-5-sonnet"})
	_ = ctx2
	res2.Usage = usage.LLMUsage{
		InputTokens:              200,
		OutputTokens:             80,
		CacheCreationInputTokens: 40,
	}
	llm2.End()

	// Child 3 — Embedding call (input_tokens only for embeddings).
	ctx3, emb, embRes := amp.EmbeddingSpan(ctx, amp.EmbeddingInput{System: "voyage", RequestModel: "voyage-3"})
	_ = ctx3
	embRes.InputTokens = 30
	emb.End()

	// End the agent span — should stamp accumulated totals.
	agentSpan.End()

	agentS := spanByName(rec, "invoke_agent")
	if agentS == nil {
		t.Fatal("invoke_agent span not found")
	}
	attrs := attrMap(agentS)

	// input_tokens = 100 + 200 + 30 (embedding) = 330
	if attrs["gen_ai.usage.input_tokens"] != int64(330) {
		t.Errorf("agent gen_ai.usage.input_tokens = %v, want 330", attrs["gen_ai.usage.input_tokens"])
	}
	// output_tokens = 50 + 80 = 130
	if attrs["gen_ai.usage.output_tokens"] != int64(130) {
		t.Errorf("agent gen_ai.usage.output_tokens = %v, want 130", attrs["gen_ai.usage.output_tokens"])
	}
	// cache_read_input_tokens = 20
	if attrs["gen_ai.usage.cache_read_input_tokens"] != int64(20) {
		t.Errorf("agent gen_ai.usage.cache_read_input_tokens = %v, want 20", attrs["gen_ai.usage.cache_read_input_tokens"])
	}
	// cache_creation_input_tokens = 40
	if attrs["gen_ai.usage.cache_creation_input_tokens"] != int64(40) {
		t.Errorf("agent gen_ai.usage.cache_creation_input_tokens = %v, want 40", attrs["gen_ai.usage.cache_creation_input_tokens"])
	}
}

// TestRollup_InputTokensIsRawUncached verifies that gen_ai.usage.input_tokens
// on the agent span is the raw (uncached) sum — not the cache-inclusive total.
//
// This is the ADR 0001 invariant: cache_read_input_tokens is a separate key so
// callers can apply the cache discount when computing cost.
func TestRollup_InputTokensIsRawUncached(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx, agentSpan, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "cache-agent"})

	_, llm, res := amp.LLMSpan(ctx, amp.LLMInput{System: "anthropic", RequestModel: "claude-3-5-sonnet"})
	res.Usage = usage.LLMUsage{
		InputTokens:              50,  // raw uncached
		OutputTokens:             25,
		CacheReadInputTokens:     200, // large cache hit — must NOT be added to input_tokens
		CacheCreationInputTokens: 10,
	}
	llm.End()
	agentSpan.End()

	agentS := spanByName(rec, "invoke_agent")
	if agentS == nil {
		t.Fatal("invoke_agent span not found")
	}
	attrs := attrMap(agentS)

	// input_tokens must be raw (50), not 50+200 or 50+200+10.
	if attrs["gen_ai.usage.input_tokens"] != int64(50) {
		t.Errorf("agent gen_ai.usage.input_tokens = %v, want 50 (raw uncached)", attrs["gen_ai.usage.input_tokens"])
	}
	if attrs["gen_ai.usage.cache_read_input_tokens"] != int64(200) {
		t.Errorf("agent gen_ai.usage.cache_read_input_tokens = %v, want 200", attrs["gen_ai.usage.cache_read_input_tokens"])
	}
	if attrs["gen_ai.usage.cache_creation_input_tokens"] != int64(10) {
		t.Errorf("agent gen_ai.usage.cache_creation_input_tokens = %v, want 10", attrs["gen_ai.usage.cache_creation_input_tokens"])
	}
}

// TestRollup_ZeroTotalsWhenNoChildren verifies that an agent span with no child
// spans carries zero token totals (present but zero, satisfying the observer's
// required-key contract).
func TestRollup_ZeroTotalsWhenNoChildren(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, agentSpan, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "empty-agent"})
	agentSpan.End()

	agentS := spanByName(rec, "invoke_agent")
	if agentS == nil {
		t.Fatal("invoke_agent span not found")
	}
	attrs := attrMap(agentS)

	if attrs["gen_ai.usage.input_tokens"] != int64(0) {
		t.Errorf("agent gen_ai.usage.input_tokens = %v, want 0", attrs["gen_ai.usage.input_tokens"])
	}
	if attrs["gen_ai.usage.output_tokens"] != int64(0) {
		t.Errorf("agent gen_ai.usage.output_tokens = %v, want 0", attrs["gen_ai.usage.output_tokens"])
	}
	// Optional cache keys must NOT be present when zero.
	if _, ok := attrs["gen_ai.usage.cache_read_input_tokens"]; ok {
		t.Error("cache_read_input_tokens should be absent when zero")
	}
	if _, ok := attrs["gen_ai.usage.cache_creation_input_tokens"]; ok {
		t.Error("cache_creation_input_tokens should be absent when zero")
	}
}

// TestRollup_ConcurrentLLMSpans fans out N LLM spans across goroutines under a
// single agent span, then asserts the agent span's totals are exactly N × per-call
// values. This test MUST be run with -race to exercise the race detector.
//
// go test -race -run TestRollup_ConcurrentLLMSpans ./...
func TestRollup_ConcurrentLLMSpans(t *testing.T) {
	const concurrency = 50

	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx, agentSpan, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "concurrent-agent"})

	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			// Each goroutine starts and ends its own LLM span under the same ctx.
			// concurrent Add calls to the accumulator are the race condition to detect.
			_, s, res := amp.LLMSpan(ctx, amp.LLMInput{
				System:       "anthropic",
				RequestModel: "claude-3-5-sonnet",
			})
			res.Usage = usage.LLMUsage{
				InputTokens:              10,
				OutputTokens:             5,
				CacheReadInputTokens:     3,
				CacheCreationInputTokens: 2,
			}
			s.End()
		}()
	}

	wg.Wait()
	agentSpan.End()

	agentS := spanByName(rec, "invoke_agent")
	if agentS == nil {
		t.Fatal("invoke_agent span not found")
	}
	attrs := attrMap(agentS)

	wantInput := int64(concurrency * 10)
	wantOutput := int64(concurrency * 5)
	wantCacheRead := int64(concurrency * 3)
	wantCacheCreation := int64(concurrency * 2)

	if attrs["gen_ai.usage.input_tokens"] != wantInput {
		t.Errorf("input_tokens = %v, want %d", attrs["gen_ai.usage.input_tokens"], wantInput)
	}
	if attrs["gen_ai.usage.output_tokens"] != wantOutput {
		t.Errorf("output_tokens = %v, want %d", attrs["gen_ai.usage.output_tokens"], wantOutput)
	}
	if attrs["gen_ai.usage.cache_read_input_tokens"] != wantCacheRead {
		t.Errorf("cache_read_input_tokens = %v, want %d", attrs["gen_ai.usage.cache_read_input_tokens"], wantCacheRead)
	}
	if attrs["gen_ai.usage.cache_creation_input_tokens"] != wantCacheCreation {
		t.Errorf("cache_creation_input_tokens = %v, want %d", attrs["gen_ai.usage.cache_creation_input_tokens"], wantCacheCreation)
	}
}

// TestRollup_LLMSpanWithoutAgent verifies that LLMSpan used without an enclosing
// AgentSpan is safe — no panic, correct per-call attributes on the leaf span.
func TestRollup_LLMSpanWithoutAgent(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	// No AgentSpan in ctx — no accumulator present.
	_, llm, res := amp.LLMSpan(ctx, amp.LLMInput{System: "openai", RequestModel: "gpt-4o"})
	res.Usage = usage.LLMUsage{InputTokens: 15, OutputTokens: 8}
	llm.End() // must not panic

	s := spanByName(rec, "chat")
	if s == nil {
		t.Fatal("chat span not found")
	}
	attrs := attrMap(s)
	if attrs["gen_ai.usage.input_tokens"] != int64(15) {
		t.Errorf("leaf input_tokens = %v, want 15", attrs["gen_ai.usage.input_tokens"])
	}
}

// TestRollup_EmbeddingSpanWithoutAgent verifies that EmbeddingSpan used without
// an enclosing AgentSpan is safe — no panic, correct attributes on the leaf span.
func TestRollup_EmbeddingSpanWithoutAgent(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, emb, res := amp.EmbeddingSpan(ctx, amp.EmbeddingInput{System: "voyage", RequestModel: "voyage-3"})
	res.InputTokens = 12
	emb.End() // must not panic

	s := spanByName(rec, "embeddings")
	if s == nil {
		t.Fatal("embeddings span not found")
	}
	attrs := attrMap(s)
	if attrs["gen_ai.usage.input_tokens"] != int64(12) {
		t.Errorf("leaf input_tokens = %v, want 12", attrs["gen_ai.usage.input_tokens"])
	}
}

// TestRollup_AccumulatorIncludesCacheCreation verifies that the accumulator's
// trace totals include both cache read AND cache creation from the full
// Anthropic usage shape (issue #10 acceptance criterion).
//
// The ephemeral split and metadata are leaf-span-only (no accumulator fields for
// them); only CacheCreationInputTokens is rolled up (the total cache-write count).
func TestRollup_AccumulatorIncludesCacheCreation(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx, agentSpan, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "cache-creation-agent"})

	// Simulate a real Anthropic usage response with cache creation + ephemeral split.
	_, llm, res := amp.LLMSpan(ctx, amp.LLMInput{System: "anthropic", RequestModel: "claude-3-5-sonnet-20241022"})
	res.Usage = usage.LLMUsage{
		InputTokens:                         1,
		OutputTokens:                        38,
		CacheReadInputTokens:                6270,
		CacheCreationInputTokens:            222,
		CacheCreationEphemeral1hInputTokens: 0,
		CacheCreationEphemeral5mInputTokens: 222,
		ServiceTier:                         "standard",
		InferenceGeo:                        "global",
	}
	llm.End()
	agentSpan.End()

	agentS := spanByName(rec, "invoke_agent")
	if agentS == nil {
		t.Fatal("invoke_agent span not found")
	}
	attrs := attrMap(agentS)

	// Accumulator totals must include cache read and cache creation.
	if attrs["gen_ai.usage.input_tokens"] != int64(1) {
		t.Errorf("agent input_tokens = %v, want 1 (raw uncached)", attrs["gen_ai.usage.input_tokens"])
	}
	if attrs["gen_ai.usage.output_tokens"] != int64(38) {
		t.Errorf("agent output_tokens = %v, want 38", attrs["gen_ai.usage.output_tokens"])
	}
	if attrs["gen_ai.usage.cache_read_input_tokens"] != int64(6270) {
		t.Errorf("agent cache_read_input_tokens = %v, want 6270", attrs["gen_ai.usage.cache_read_input_tokens"])
	}
	if attrs["gen_ai.usage.cache_creation_input_tokens"] != int64(222) {
		t.Errorf("agent cache_creation_input_tokens = %v, want 222", attrs["gen_ai.usage.cache_creation_input_tokens"])
	}
	// Ephemeral split and metadata are leaf-span-only — must NOT appear on agent span.
	if _, ok := attrs["gen_ai.usage.cache_creation.ephemeral_5m_input_tokens"]; ok {
		t.Error("ephemeral_5m_input_tokens should not be rolled up to agent span")
	}
	if _, ok := attrs["gen_ai.anthropic.service_tier"]; ok {
		t.Error("service_tier should not be rolled up to agent span")
	}
	if _, ok := attrs["gen_ai.anthropic.inference_geo"]; ok {
		t.Error("inference_geo should not be rolled up to agent span")
	}
}

// TestRollup_LeafUsageStaysOnLeafSpan verifies that the roll-up is ADDITIVE —
// per-call token attributes remain on each leaf span (OTel-correct). The agent
// span carries the total AND each leaf span carries its own usage.
func TestRollup_LeafUsageStaysOnLeafSpan(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx, agentSpan, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "additive-agent"})

	_, llm, res := amp.LLMSpan(ctx, amp.LLMInput{System: "anthropic", RequestModel: "claude-3-5-sonnet"})
	res.Usage = usage.LLMUsage{InputTokens: 42, OutputTokens: 17}
	llm.End()

	agentSpan.End()

	// Leaf span must still carry its own token attributes.
	leafS := spanByName(rec, "chat")
	if leafS == nil {
		t.Fatal("chat span not found")
	}
	leafAttrs := attrMap(leafS)
	if leafAttrs["gen_ai.usage.input_tokens"] != int64(42) {
		t.Errorf("leaf span input_tokens = %v, want 42", leafAttrs["gen_ai.usage.input_tokens"])
	}
	if leafAttrs["gen_ai.usage.output_tokens"] != int64(17) {
		t.Errorf("leaf span output_tokens = %v, want 17", leafAttrs["gen_ai.usage.output_tokens"])
	}

	// Agent span must also carry the totals.
	agentS := spanByName(rec, "invoke_agent")
	if agentS == nil {
		t.Fatal("invoke_agent span not found")
	}
	agentAttrs := attrMap(agentS)
	if agentAttrs["gen_ai.usage.input_tokens"] != int64(42) {
		t.Errorf("agent span input_tokens = %v, want 42", agentAttrs["gen_ai.usage.input_tokens"])
	}
	if agentAttrs["gen_ai.usage.output_tokens"] != int64(17) {
		t.Errorf("agent span output_tokens = %v, want 17", agentAttrs["gen_ai.usage.output_tokens"])
	}
}
