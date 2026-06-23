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

// Package conformance_test exercises a full round-trip from SDK span helpers
// through the real observer logic (DetermineSpanType / ProcessSpan) without
// any live infrastructure.
//
// Design decisions:
//  1. Span capture: we install an OTel in-memory SpanRecorder (tracetest) as the
//     global TracerProvider before each test. The amp helpers resolve the tracer
//     via otel.GetTracerProvider() at call-time, so they pick up the recorder.
//     No network, no OTLP exporter.
//  2. Attribute conversion: OTel ReadOnlySpan.Attributes() returns []attribute.KeyValue
//     with typed Value objects. The observer expects map[string]interface{} whose
//     values are native Go types (string, int64, float64, bool). We flatten using
//     attribute.Value.AsInterface() — the same approach used by the SDK golden tests.
//  3. Observer functions under test: DetermineSpanType and ProcessSpan from
//     traces-observer-service/opensearch. ProcessSpan calls DetermineSpanType
//     internally, so testing ProcessSpan covers both.
//  4. Module isolation: this module (test/go-conformance) depends on BOTH the SDK
//     and the observer via local replace directives. Neither the SDK nor the observer
//     depends on the other — the SDK module stays dependency-clean.
package conformance_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	amp "github.com/wso2/agent-manager/libs/amp-instrumentation-go"
	"github.com/wso2/agent-manager/traces-observer-service/opensearch"
)

// --------------------------------------------------------------------------
// Test helpers
// --------------------------------------------------------------------------

// setupRecorder installs an in-memory SpanRecorder as the global OTel provider.
// Returns the recorder and a cleanup function that shuts down the provider.
func setupRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		tp.Shutdown(context.Background()) //nolint:errcheck
	})
	amp.ResetConfigCache()
	t.Cleanup(amp.ResetConfigCache)
	return rec
}

// endedSpans returns all recorded spans after the given function runs.
// It is a convenience wrapper for tests that emit a known number of spans.
func endedSpans(rec *tracetest.SpanRecorder) []sdktrace.ReadOnlySpan {
	return rec.Ended()
}

// spanByName returns the first ended span with the given name.
func spanByName(t *testing.T, rec *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range rec.Ended() {
		if s.Name() == name {
			return s
		}
	}
	t.Fatalf("no span named %q in recorder", name)
	return nil
}

// toObserverSpan converts an OTel ReadOnlySpan to the opensearch.Span shape
// expected by DetermineSpanType / ProcessSpan.
//
// The observer's map[string]interface{} attribute values must be native Go types
// (string, int64, float64, bool). attribute.Value.AsInterface() returns exactly
// that, so we use it directly — no special-casing needed.
//
// The service field comes from the "openchoreo.dev/component-uid" resource
// attribute (observer/convert.go), which we set to a test sentinel value.
func toObserverSpan(s sdktrace.ReadOnlySpan) opensearch.Span {
	attrs := make(map[string]interface{}, len(s.Attributes()))
	for _, kv := range s.Attributes() {
		attrs[string(kv.Key)] = kv.Value.AsInterface()
	}

	// Build a minimal resource map mirroring what convert.go expects.
	resource := map[string]interface{}{
		"openchoreo.dev/component-uid": "test-component",
	}

	return opensearch.Span{
		TraceID:    s.SpanContext().TraceID().String(),
		SpanID:     s.SpanContext().SpanID().String(),
		Name:       s.Name(),
		Service:    "test-component",
		Attributes: attrs,
		Resource:   resource,
	}
}

// --------------------------------------------------------------------------
// 1. LLM span round-trip
// --------------------------------------------------------------------------

// TestRoundTrip_LLMSpan proves that a span emitted by amp.LLMSpan is resolved
// to SpanTypeLLM by the observer, and that AmpAttributes.Data carries the
// expected model, vendor, and token usage.
func TestRoundTrip_LLMSpan(t *testing.T) {
	rec := setupRecorder(t)

	ctx := context.Background()
	_, span, result := amp.LLMSpan(ctx, amp.LLMInput{
		System:       "anthropic",
		RequestModel: "claude-3-5-sonnet-20241022",
		InputMessages: []map[string]any{
			{"role": "user", "content": "What is 2+2?"},
		},
	})
	result.ResponseModel = "claude-3-5-sonnet-20241022"
	result.OutputMessages = []map[string]any{
		{"role": "assistant", "content": "4"},
	}
	result.Usage = amp.LLMUsage{
		InputTokens:  20,
		OutputTokens: 5,
	}
	span.End()

	spans := endedSpans(rec)
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	obs := toObserverSpan(spans[len(spans)-1])

	// DetermineSpanType
	spanType := opensearch.DetermineSpanType(obs)
	if spanType != opensearch.SpanTypeLLM {
		t.Errorf("DetermineSpanType = %q, want %q", spanType, opensearch.SpanTypeLLM)
	}

	// ProcessSpan
	processed := opensearch.ProcessSpan(obs)
	if processed.AmpAttributes == nil {
		t.Fatal("ProcessSpan returned nil AmpAttributes")
	}
	if processed.AmpAttributes.Kind != string(opensearch.SpanTypeLLM) {
		t.Errorf("AmpAttributes.Kind = %q, want %q", processed.AmpAttributes.Kind, opensearch.SpanTypeLLM)
	}

	llmData, ok := processed.AmpAttributes.Data.(opensearch.LLMData)
	if !ok {
		t.Fatalf("AmpAttributes.Data type = %T, want opensearch.LLMData", processed.AmpAttributes.Data)
	}
	if llmData.Vendor != "anthropic" {
		t.Errorf("LLMData.Vendor = %q, want %q", llmData.Vendor, "anthropic")
	}
	if llmData.Model != "claude-3-5-sonnet-20241022" {
		t.Errorf("LLMData.Model = %q, want %q", llmData.Model, "claude-3-5-sonnet-20241022")
	}
	if llmData.TokenUsage == nil {
		t.Fatal("LLMData.TokenUsage is nil")
	}
	if llmData.TokenUsage.InputTokens != 20 {
		t.Errorf("TokenUsage.InputTokens = %d, want 20", llmData.TokenUsage.InputTokens)
	}
	if llmData.TokenUsage.OutputTokens != 5 {
		t.Errorf("TokenUsage.OutputTokens = %d, want 5", llmData.TokenUsage.OutputTokens)
	}
	if llmData.TokenUsage.TotalTokens != 25 {
		t.Errorf("TokenUsage.TotalTokens = %d, want 25", llmData.TokenUsage.TotalTokens)
	}
}

// --------------------------------------------------------------------------
// 2. Agent span round-trip
// --------------------------------------------------------------------------

// TestRoundTrip_AgentSpan proves that a span emitted by amp.AgentSpan is
// resolved to SpanTypeAgent by the observer, with the correct agent name and
// framework populated in AmpAttributes.Data.
func TestRoundTrip_AgentSpan(t *testing.T) {
	rec := setupRecorder(t)

	ctx := context.Background()
	_, span, result := amp.AgentSpan(ctx, amp.AgentInput{
		Name:      "my-research-agent",
		Framework: "strands-agents",
	})
	result.OutputMessages = []map[string]any{
		{"role": "assistant", "content": "Research complete"},
	}
	span.End()

	spans := endedSpans(rec)
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	obs := toObserverSpan(spans[len(spans)-1])

	spanType := opensearch.DetermineSpanType(obs)
	if spanType != opensearch.SpanTypeAgent {
		t.Errorf("DetermineSpanType = %q, want %q", spanType, opensearch.SpanTypeAgent)
	}

	processed := opensearch.ProcessSpan(obs)
	if processed.AmpAttributes == nil {
		t.Fatal("ProcessSpan returned nil AmpAttributes")
	}
	if processed.AmpAttributes.Kind != string(opensearch.SpanTypeAgent) {
		t.Errorf("AmpAttributes.Kind = %q, want %q", processed.AmpAttributes.Kind, opensearch.SpanTypeAgent)
	}

	agentData, ok := processed.AmpAttributes.Data.(opensearch.AgentData)
	if !ok {
		t.Fatalf("AmpAttributes.Data type = %T, want opensearch.AgentData", processed.AmpAttributes.Data)
	}
	if agentData.Name != "my-research-agent" {
		t.Errorf("AgentData.Name = %q, want %q", agentData.Name, "my-research-agent")
	}
	if agentData.Framework != "strands-agents" {
		t.Errorf("AgentData.Framework = %q, want %q", agentData.Framework, "strands-agents")
	}
}

// --------------------------------------------------------------------------
// 3. Tool span round-trip
// --------------------------------------------------------------------------

// TestRoundTrip_ToolSpan proves that a span emitted by amp.ToolSpan is
// resolved to SpanTypeTool by the observer.
func TestRoundTrip_ToolSpan(t *testing.T) {
	rec := setupRecorder(t)

	ctx := context.Background()
	_, span, result := amp.ToolSpan(ctx, amp.ToolInput{
		Name: "web_search",
		Arguments: map[string]any{
			"query": "Go language benchmarks",
		},
	})
	result.Output = map[string]any{"results": []string{"result1", "result2"}}
	span.End()

	spans := endedSpans(rec)
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	obs := toObserverSpan(spans[len(spans)-1])

	spanType := opensearch.DetermineSpanType(obs)
	if spanType != opensearch.SpanTypeTool {
		t.Errorf("DetermineSpanType = %q, want %q", spanType, opensearch.SpanTypeTool)
	}

	processed := opensearch.ProcessSpan(obs)
	if processed.AmpAttributes == nil {
		t.Fatal("ProcessSpan returned nil AmpAttributes")
	}
	if processed.AmpAttributes.Kind != string(opensearch.SpanTypeTool) {
		t.Errorf("AmpAttributes.Kind = %q, want %q", processed.AmpAttributes.Kind, opensearch.SpanTypeTool)
	}

	toolData, ok := processed.AmpAttributes.Data.(opensearch.ToolData)
	if !ok {
		t.Fatalf("AmpAttributes.Data type = %T, want opensearch.ToolData", processed.AmpAttributes.Data)
	}
	if toolData.Name != "web_search" {
		t.Errorf("ToolData.Name = %q, want %q", toolData.Name, "web_search")
	}
}

// --------------------------------------------------------------------------
// 4. Embedding span round-trip
// --------------------------------------------------------------------------

// TestRoundTrip_EmbeddingSpan proves that a span emitted by amp.EmbeddingSpan
// is resolved to SpanTypeEmbedding by the observer, with token usage preserved.
func TestRoundTrip_EmbeddingSpan(t *testing.T) {
	rec := setupRecorder(t)

	ctx := context.Background()
	_, span, result := amp.EmbeddingSpan(ctx, amp.EmbeddingInput{
		System:       "voyage",
		RequestModel: "voyage-3-lite",
		Texts:        []string{"Hello world", "Embedding test"},
	})
	result.ResponseModel = "voyage-3-lite"
	result.InputTokens = 12
	span.End()

	spans := endedSpans(rec)
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	obs := toObserverSpan(spans[len(spans)-1])

	spanType := opensearch.DetermineSpanType(obs)
	if spanType != opensearch.SpanTypeEmbedding {
		t.Errorf("DetermineSpanType = %q, want %q", spanType, opensearch.SpanTypeEmbedding)
	}

	processed := opensearch.ProcessSpan(obs)
	if processed.AmpAttributes == nil {
		t.Fatal("ProcessSpan returned nil AmpAttributes")
	}
	if processed.AmpAttributes.Kind != string(opensearch.SpanTypeEmbedding) {
		t.Errorf("AmpAttributes.Kind = %q, want %q", processed.AmpAttributes.Kind, opensearch.SpanTypeEmbedding)
	}

	embData, ok := processed.AmpAttributes.Data.(opensearch.EmbeddingData)
	if !ok {
		t.Fatalf("AmpAttributes.Data type = %T, want opensearch.EmbeddingData", processed.AmpAttributes.Data)
	}
	if embData.Model != "voyage-3-lite" {
		t.Errorf("EmbeddingData.Model = %q, want %q", embData.Model, "voyage-3-lite")
	}
	if embData.Vendor != "voyage" {
		t.Errorf("EmbeddingData.Vendor = %q, want %q", embData.Vendor, "voyage")
	}
	if embData.TokenUsage == nil {
		t.Fatal("EmbeddingData.TokenUsage is nil")
	}
	if embData.TokenUsage.InputTokens != 12 {
		t.Errorf("TokenUsage.InputTokens = %d, want 12", embData.TokenUsage.InputTokens)
	}
}

// --------------------------------------------------------------------------
// 5. Retriever span round-trip
// --------------------------------------------------------------------------

// TestRoundTrip_RetrieverSpan proves that a span emitted by amp.RetrieverSpan
// is resolved to SpanTypeRetriever by the observer, with VectorDB and collection
// populated in AmpAttributes.Data.
func TestRoundTrip_RetrieverSpan(t *testing.T) {
	rec := setupRecorder(t)

	ctx := context.Background()
	_, span := amp.RetrieverSpan(ctx, amp.RetrieverInput{
		VectorDB:   "pinecone",
		Collection: "embeddings-index",
		TopK:       5,
	})
	span.End()

	spans := endedSpans(rec)
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	obs := toObserverSpan(spans[len(spans)-1])

	spanType := opensearch.DetermineSpanType(obs)
	if spanType != opensearch.SpanTypeRetriever {
		t.Errorf("DetermineSpanType = %q, want %q", spanType, opensearch.SpanTypeRetriever)
	}

	processed := opensearch.ProcessSpan(obs)
	if processed.AmpAttributes == nil {
		t.Fatal("ProcessSpan returned nil AmpAttributes")
	}
	if processed.AmpAttributes.Kind != string(opensearch.SpanTypeRetriever) {
		t.Errorf("AmpAttributes.Kind = %q, want %q", processed.AmpAttributes.Kind, opensearch.SpanTypeRetriever)
	}

	retData, ok := processed.AmpAttributes.Data.(opensearch.RetrieverData)
	if !ok {
		t.Fatalf("AmpAttributes.Data type = %T, want opensearch.RetrieverData", processed.AmpAttributes.Data)
	}
	if retData.VectorDB != "pinecone" {
		t.Errorf("RetrieverData.VectorDB = %q, want %q", retData.VectorDB, "pinecone")
	}
	if retData.Collection != "embeddings-index" {
		t.Errorf("RetrieverData.Collection = %q, want %q", retData.Collection, "embeddings-index")
	}
	if retData.TopK != 5 {
		t.Errorf("RetrieverData.TopK = %d, want 5", retData.TopK)
	}
}

// --------------------------------------------------------------------------
// 6. Rerank span round-trip
// --------------------------------------------------------------------------

// TestRoundTrip_RerankSpan proves that a span emitted by amp.RerankSpan is
// resolved to SpanTypeRerank by the observer.
func TestRoundTrip_RerankSpan(t *testing.T) {
	rec := setupRecorder(t)

	ctx := context.Background()
	_, span := amp.RerankSpan(ctx, amp.RerankInput{
		Model:          "rerank-english-v3.0",
		Query:          "What is machine learning?",
		CandidateCount: 10,
	})
	span.End()

	spans := endedSpans(rec)
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	obs := toObserverSpan(spans[len(spans)-1])

	spanType := opensearch.DetermineSpanType(obs)
	if spanType != opensearch.SpanTypeRerank {
		t.Errorf("DetermineSpanType = %q, want %q", spanType, opensearch.SpanTypeRerank)
	}

	processed := opensearch.ProcessSpan(obs)
	if processed.AmpAttributes == nil {
		t.Fatal("ProcessSpan returned nil AmpAttributes")
	}
	if processed.AmpAttributes.Kind != string(opensearch.SpanTypeRerank) {
		t.Errorf("AmpAttributes.Kind = %q, want %q", processed.AmpAttributes.Kind, opensearch.SpanTypeRerank)
	}
}

// --------------------------------------------------------------------------
// 7. Chain span round-trip
// --------------------------------------------------------------------------

// TestRoundTrip_ChainSpan proves that a span emitted by amp.ChainSpan is
// resolved to SpanTypeChain by the observer.
func TestRoundTrip_ChainSpan(t *testing.T) {
	rec := setupRecorder(t)

	ctx := context.Background()
	_, span, result := amp.ChainSpan(ctx, amp.ChainInput{
		Name:  "rag-pipeline",
		Input: "What is the capital of France?",
	})
	result.Output = "Paris is the capital of France."
	span.End()

	spans := endedSpans(rec)
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	obs := toObserverSpan(spans[len(spans)-1])

	spanType := opensearch.DetermineSpanType(obs)
	if spanType != opensearch.SpanTypeChain {
		t.Errorf("DetermineSpanType = %q, want %q", spanType, opensearch.SpanTypeChain)
	}

	processed := opensearch.ProcessSpan(obs)
	if processed.AmpAttributes == nil {
		t.Fatal("ProcessSpan returned nil AmpAttributes")
	}
	if processed.AmpAttributes.Kind != string(opensearch.SpanTypeChain) {
		t.Errorf("AmpAttributes.Kind = %q, want %q", processed.AmpAttributes.Kind, opensearch.SpanTypeChain)
	}
}

// --------------------------------------------------------------------------
// 8. Token roll-up: agent-rooted trace with child LLM spans
// --------------------------------------------------------------------------

// TestRoundTrip_AgentTokenRollup proves that an agent span that wraps two LLM
// child spans accumulates the correct token totals. The agent span's
// gen_ai.usage.input_tokens / output_tokens are the sum of the two leaf calls,
// and the observer extracts them correctly through ProcessSpan.
func TestRoundTrip_AgentTokenRollup(t *testing.T) {
	rec := setupRecorder(t)

	ctx := context.Background()

	// Start agent span — this installs the accumulator in ctx.
	ctx, agentSpan, agentResult := amp.AgentSpan(ctx, amp.AgentInput{
		Name:      "rollup-test-agent",
		Framework: "custom",
	})

	// Child LLM call 1.
	_, llmSpan1, llmResult1 := amp.LLMSpan(ctx, amp.LLMInput{
		System:       "openai",
		RequestModel: "gpt-4o",
	})
	llmResult1.Usage = amp.LLMUsage{InputTokens: 100, OutputTokens: 50}
	llmSpan1.End()

	// Child LLM call 2.
	_, llmSpan2, llmResult2 := amp.LLMSpan(ctx, amp.LLMInput{
		System:       "openai",
		RequestModel: "gpt-4o",
	})
	llmResult2.Usage = amp.LLMUsage{InputTokens: 80, OutputTokens: 40}
	llmSpan2.End()

	agentResult.OutputMessages = []map[string]any{
		{"role": "assistant", "content": "Done"},
	}
	agentSpan.End()

	// Find the agent span in the recorder by name.
	agentObs := toObserverSpan(spanByName(t, rec, "invoke_agent"))

	processed := opensearch.ProcessSpan(agentObs)
	if processed.AmpAttributes == nil {
		t.Fatal("ProcessSpan returned nil AmpAttributes for agent span")
	}
	if processed.AmpAttributes.Kind != string(opensearch.SpanTypeAgent) {
		t.Errorf("AmpAttributes.Kind = %q, want %q", processed.AmpAttributes.Kind, opensearch.SpanTypeAgent)
	}

	agentData, ok := processed.AmpAttributes.Data.(opensearch.AgentData)
	if !ok {
		t.Fatalf("AmpAttributes.Data type = %T, want opensearch.AgentData", processed.AmpAttributes.Data)
	}
	if agentData.TokenUsage == nil {
		t.Fatal("AgentData.TokenUsage is nil — roll-up was not preserved through the observer")
	}

	wantInput := 180  // 100 + 80
	wantOutput := 90  // 50 + 40
	wantTotal := 270  // 180 + 90

	if agentData.TokenUsage.InputTokens != wantInput {
		t.Errorf("rolled-up InputTokens = %d, want %d", agentData.TokenUsage.InputTokens, wantInput)
	}
	if agentData.TokenUsage.OutputTokens != wantOutput {
		t.Errorf("rolled-up OutputTokens = %d, want %d", agentData.TokenUsage.OutputTokens, wantOutput)
	}
	if agentData.TokenUsage.TotalTokens != wantTotal {
		t.Errorf("rolled-up TotalTokens = %d, want %d", agentData.TokenUsage.TotalTokens, wantTotal)
	}
}

// --------------------------------------------------------------------------
// 9. Cache token round-trip
// --------------------------------------------------------------------------

// TestRoundTrip_LLMSpan_CacheTokens proves that cache_read_input_tokens is
// preserved through the round-trip and extracted by the observer.
func TestRoundTrip_LLMSpan_CacheTokens(t *testing.T) {
	rec := setupRecorder(t)

	ctx := context.Background()
	_, span, result := amp.LLMSpan(ctx, amp.LLMInput{
		System:       "anthropic",
		RequestModel: "claude-3-5-sonnet-20241022",
	})
	result.Usage = amp.LLMUsage{
		InputTokens:          10,
		OutputTokens:         20,
		CacheReadInputTokens: 500,
	}
	span.End()

	spans := endedSpans(rec)
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	obs := toObserverSpan(spans[len(spans)-1])

	processed := opensearch.ProcessSpan(obs)
	if processed.AmpAttributes == nil {
		t.Fatal("ProcessSpan returned nil AmpAttributes")
	}
	llmData, ok := processed.AmpAttributes.Data.(opensearch.LLMData)
	if !ok {
		t.Fatalf("AmpAttributes.Data type = %T, want opensearch.LLMData", processed.AmpAttributes.Data)
	}
	if llmData.TokenUsage == nil {
		t.Fatal("LLMData.TokenUsage is nil")
	}
	if llmData.TokenUsage.CacheReadInputTokens != 500 {
		t.Errorf("CacheReadInputTokens = %d, want 500", llmData.TokenUsage.CacheReadInputTokens)
	}
}

// --------------------------------------------------------------------------
// 10. Redaction round-trip — kind resolution must survive content=false
// --------------------------------------------------------------------------

// TestRoundTrip_Redaction proves that AMP_TRACE_CONTENT=false does not break
// span kind resolution: even with content redacted, the observer correctly
// identifies each span type and TokenUsage is preserved.
func TestRoundTrip_Redaction(t *testing.T) {
	t.Setenv("AMP_OTEL_ENDPOINT", "https://otel.example.com")
	t.Setenv("AMP_AGENT_API_KEY", "test-key")
	t.Setenv("AMP_TRACE_CONTENT", "false")

	rec := setupRecorder(t)

	ctx := context.Background()

	// LLM span with redaction.
	_, llmSpan, llmResult := amp.LLMSpan(ctx, amp.LLMInput{
		System:       "anthropic",
		RequestModel: "claude-3-5-sonnet-20241022",
		InputMessages: []map[string]any{
			{"role": "user", "content": "secret content"},
		},
	})
	llmResult.OutputMessages = []map[string]any{
		{"role": "assistant", "content": "secret response"},
	}
	llmResult.Usage = amp.LLMUsage{InputTokens: 15, OutputTokens: 8}
	llmSpan.End()

	spans := endedSpans(rec)
	if len(spans) == 0 {
		t.Fatal("no spans recorded")
	}
	obs := toObserverSpan(spans[len(spans)-1])

	// Kind resolution must still work after redaction.
	spanType := opensearch.DetermineSpanType(obs)
	if spanType != opensearch.SpanTypeLLM {
		t.Errorf("DetermineSpanType with redaction = %q, want %q", spanType, opensearch.SpanTypeLLM)
	}

	processed := opensearch.ProcessSpan(obs)
	if processed.AmpAttributes == nil {
		t.Fatal("ProcessSpan returned nil AmpAttributes under redaction")
	}
	if processed.AmpAttributes.Kind != string(opensearch.SpanTypeLLM) {
		t.Errorf("Kind after redaction = %q, want %q", processed.AmpAttributes.Kind, opensearch.SpanTypeLLM)
	}

	// Token counts must survive (they are not redacted).
	llmData, ok := processed.AmpAttributes.Data.(opensearch.LLMData)
	if !ok {
		t.Fatalf("AmpAttributes.Data type = %T, want opensearch.LLMData", processed.AmpAttributes.Data)
	}
	if llmData.TokenUsage == nil {
		t.Fatal("TokenUsage nil after redaction")
	}
	if llmData.TokenUsage.InputTokens != 15 {
		t.Errorf("InputTokens after redaction = %d, want 15", llmData.TokenUsage.InputTokens)
	}
	if llmData.TokenUsage.OutputTokens != 8 {
		t.Errorf("OutputTokens after redaction = %d, want 8", llmData.TokenUsage.OutputTokens)
	}
}

// --------------------------------------------------------------------------
// 11. Redaction: Chain and Tool span kinds survive content=false
// --------------------------------------------------------------------------

// TestRoundTrip_Redaction_ChainAndTool proves that chain and tool span kind
// resolution works even when AMP_TRACE_CONTENT=false, since these kinds rely
// on traceloop.span.kind / gen_ai.operation.name (structural attributes that
// are never redacted).
func TestRoundTrip_Redaction_ChainAndTool(t *testing.T) {
	t.Setenv("AMP_OTEL_ENDPOINT", "https://otel.example.com")
	t.Setenv("AMP_AGENT_API_KEY", "test-key")
	t.Setenv("AMP_TRACE_CONTENT", "false")

	rec := setupRecorder(t)

	ctx := context.Background()

	// Chain span.
	_, chainSpan, chainResult := amp.ChainSpan(ctx, amp.ChainInput{
		Name:  "pipeline",
		Input: "secret pipeline input",
	})
	chainResult.Output = "secret pipeline output"
	chainSpan.End()

	chainObs := toObserverSpan(spanByName(t, rec, "pipeline"))
	chainType := opensearch.DetermineSpanType(chainObs)
	if chainType != opensearch.SpanTypeChain {
		t.Errorf("chain DetermineSpanType under redaction = %q, want %q", chainType, opensearch.SpanTypeChain)
	}

	// Tool span.
	_, toolSpan, toolResult := amp.ToolSpan(ctx, amp.ToolInput{
		Name: "secret_tool",
		Arguments: map[string]any{
			"param": "secret value",
		},
	})
	toolResult.Output = "secret result"
	toolSpan.End()

	toolObs := toObserverSpan(spanByName(t, rec, "execute_tool"))
	toolType := opensearch.DetermineSpanType(toolObs)
	if toolType != opensearch.SpanTypeTool {
		t.Errorf("tool DetermineSpanType under redaction = %q, want %q", toolType, opensearch.SpanTypeTool)
	}
}

// --------------------------------------------------------------------------
// 12. All 7 span kinds in one table-driven test
// --------------------------------------------------------------------------

// TestRoundTrip_AllSevenKinds is a table-driven summary test that asserts each
// of the 7 amp span helpers resolves to its expected SpanType. This makes
// CI output immediately readable: one failure line per broken kind.
func TestRoundTrip_AllSevenKinds(t *testing.T) {
	cases := []struct {
		name      string
		emit      func(ctx context.Context, rec *tracetest.SpanRecorder) opensearch.Span
		wantKind  opensearch.SpanType
		spanName  string
	}{
		{
			name:     "LLM",
			wantKind: opensearch.SpanTypeLLM,
			spanName: "chat",
			emit: func(ctx context.Context, rec *tracetest.SpanRecorder) opensearch.Span {
				_, sp, res := amp.LLMSpan(ctx, amp.LLMInput{
					System: "openai", RequestModel: "gpt-4o",
				})
				res.Usage = amp.LLMUsage{InputTokens: 1, OutputTokens: 1}
				sp.End()
				return toObserverSpan(spanByName(t, rec, "chat"))
			},
		},
		{
			name:     "Agent",
			wantKind: opensearch.SpanTypeAgent,
			spanName: "invoke_agent",
			emit: func(ctx context.Context, rec *tracetest.SpanRecorder) opensearch.Span {
				_, sp, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "test-agent"})
				sp.End()
				return toObserverSpan(spanByName(t, rec, "invoke_agent"))
			},
		},
		{
			name:     "Tool",
			wantKind: opensearch.SpanTypeTool,
			spanName: "execute_tool",
			emit: func(ctx context.Context, rec *tracetest.SpanRecorder) opensearch.Span {
				_, sp, _ := amp.ToolSpan(ctx, amp.ToolInput{Name: "my_tool"})
				sp.End()
				return toObserverSpan(spanByName(t, rec, "execute_tool"))
			},
		},
		{
			name:     "Embedding",
			wantKind: opensearch.SpanTypeEmbedding,
			spanName: "embeddings",
			emit: func(ctx context.Context, rec *tracetest.SpanRecorder) opensearch.Span {
				_, sp, res := amp.EmbeddingSpan(ctx, amp.EmbeddingInput{
					System: "openai", RequestModel: "text-embedding-3-small",
					Texts: []string{"test"},
				})
				res.InputTokens = 1
				sp.End()
				return toObserverSpan(spanByName(t, rec, "embeddings"))
			},
		},
		{
			name:     "Retriever",
			wantKind: opensearch.SpanTypeRetriever,
			spanName: "vector_search",
			emit: func(ctx context.Context, rec *tracetest.SpanRecorder) opensearch.Span {
				_, sp := amp.RetrieverSpan(ctx, amp.RetrieverInput{VectorDB: "weaviate"})
				sp.End()
				return toObserverSpan(spanByName(t, rec, "vector_search"))
			},
		},
		{
			name:     "Rerank",
			wantKind: opensearch.SpanTypeRerank,
			spanName: "rerank",
			emit: func(ctx context.Context, rec *tracetest.SpanRecorder) opensearch.Span {
				_, sp := amp.RerankSpan(ctx, amp.RerankInput{Model: "rerank-english-v3.0"})
				sp.End()
				return toObserverSpan(spanByName(t, rec, "rerank"))
			},
		},
		{
			name:     "Chain",
			wantKind: opensearch.SpanTypeChain,
			spanName: "my-chain",
			emit: func(ctx context.Context, rec *tracetest.SpanRecorder) opensearch.Span {
				_, sp, _ := amp.ChainSpan(ctx, amp.ChainInput{Name: "my-chain"})
				sp.End()
				return toObserverSpan(spanByName(t, rec, "my-chain"))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := setupRecorder(t)
			ctx := context.Background()

			obs := tc.emit(ctx, rec)

			got := opensearch.DetermineSpanType(obs)
			if got != tc.wantKind {
				t.Errorf("DetermineSpanType = %q, want %q", got, tc.wantKind)
			}

			processed := opensearch.ProcessSpan(obs)
			if processed.AmpAttributes == nil {
				t.Fatal("ProcessSpan returned nil AmpAttributes")
			}
			if processed.AmpAttributes.Kind != string(tc.wantKind) {
				t.Errorf("AmpAttributes.Kind = %q, want %q", processed.AmpAttributes.Kind, tc.wantKind)
			}
		})
	}
}
