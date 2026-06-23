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

// Package amp is the Go instrumentation SDK for WSO2 Agent Manager.
//
// It provides helpers to emit fully-conformant AMP spans for Go agents.
//
// # Quick start
//
//	func main() {
//	    ctx := context.Background()
//	    if err := amp.Init(ctx); err != nil {
//	        log.Fatal(err)
//	    }
//	    defer amp.Shutdown(ctx)
//
//	    ctx, span, result := amp.LLMSpan(ctx, amp.LLMInput{
//	        System:        "anthropic",
//	        RequestModel:  "claude-3-5-sonnet-20241022",
//	        InputMessages: []map[string]any{{"role": "user", "content": "Hello"}},
//	    })
//	    defer span.End()
//
//	    // ... call the LLM ...
//	    result.OutputMessages = []map[string]any{{"role": "assistant", "content": "Hi!"}}
//	    result.InputTokens = 10
//	    result.OutputTokens = 5
//	}
//
// # Environment variables
//
//   - AMP_OTEL_ENDPOINT   – required; AMP gateway base URL
//   - AMP_AGENT_API_KEY   – required; API key for the x-amp-api-key header
//   - AMP_TRACE_CONTENT   – optional; "false" redacts prompt/response text (default "true")
//   - AMP_COMPONENT_UID  – optional; openchoreo.dev/component-uid resource attribute
//   - AMP_DEBUG           – optional; "true" enables verbose SDK logging
package amp

import (
	"context"
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/accumulator"
	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/config"
	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/exporter"
	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/redact"
	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/usage"
)

const tracerName = "amp-instrumentation-go"

// Init configures the global OTel tracer provider to export spans to AMP.
//
// It reads AMP_OTEL_ENDPOINT and AMP_AGENT_API_KEY from the environment;
// returns a descriptive error if either is missing. Idempotent: a second call
// is a no-op and returns nil.
func Init(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	_, err = exporter.InitGlobal(ctx, cfg)
	return err
}

// Shutdown flushes all batched spans and tears down the tracer provider.
// Must be called before the process exits (typically via defer in main).
func Shutdown(ctx context.Context) error {
	return exporter.ShutdownGlobal(ctx)
}

// --------------------------------------------------------------------------
// LLM span
// --------------------------------------------------------------------------

// LLMInput holds the parameters for one LLM chat call. Required fields are
// System, RequestModel; all others are optional.
type LLMInput struct {
	// System is the AI provider / vendor (e.g. "anthropic", "openai").
	System string
	// RequestModel is the model identifier requested by the caller.
	RequestModel string
	// Temperature is the sampling temperature. Only set when SetTemperature is true.
	Temperature float64
	// SetTemperature must be true for Temperature to be emitted.
	SetTemperature bool
	// InputMessages is the list of chat messages sent to the model.
	InputMessages []map[string]any
}

// LLMResult is filled by the caller after the LLM returns.
type LLMResult struct {
	// ResponseModel is the model that actually answered (may differ from request).
	ResponseModel string
	// OutputMessages are the model's response messages.
	OutputMessages []map[string]any
	// Usage holds the token counts for this call.
	Usage usage.LLMUsage
}

// Span is a handle to an in-flight AMP span. The caller fills a result object
// and then calls End() to write the response attributes and close the span.
type Span struct {
	span          trace.Span
	result        *LLMResult
	traceContent  bool
	endAttributes func()
}

// End writes the response attributes recorded on the result handle and closes
// the underlying OTel span. It must be called exactly once, typically as:
//
//	ctx, s, result := amp.LLMSpan(ctx, input)
//	defer s.End()
func (s *Span) End() {
	if s.endAttributes != nil {
		s.endAttributes()
	}
	s.span.End()
}

// Error marks the span as failed with error status and sets error.type.
// It mirrors instrumentation.py's mark_error helper.
func (s *Span) Error(err error) {
	if err == nil {
		return
	}
	s.span.SetStatus(codes.Error, err.Error())
	s.span.SetAttributes(attribute.String("error.type", err.Error()))
}

// LLMSpan starts a chat span, returning (ctx, span, result). The caller fills
// result after the LLM returns, then calls span.End().
//
// Attributes written at Start:
//   - gen_ai.operation.name = "chat"
//   - gen_ai.system
//   - gen_ai.request.model
//   - gen_ai.request.temperature (if set)
//   - gen_ai.input.messages
//
// Attributes written at End (from result):
//   - gen_ai.response.model
//   - gen_ai.output.messages
//   - gen_ai.usage.input_tokens
//   - gen_ai.usage.output_tokens
//   - gen_ai.usage.cache_read_input_tokens (if non-zero)
//   - gen_ai.usage.cache_creation_input_tokens (if non-zero)
func LLMSpan(ctx context.Context, input LLMInput) (context.Context, *Span, *LLMResult) {
	cfg := loadConfigCached()

	tracer := otel.GetTracerProvider().Tracer(tracerName)
	ctx, otelSpan := tracer.Start(ctx, "chat", trace.WithSpanKind(trace.SpanKindClient))

	// Write start-time attributes.
	otelSpan.SetAttributes(
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.system", input.System),
		attribute.String("gen_ai.request.model", input.RequestModel),
	)
	if input.SetTemperature {
		otelSpan.SetAttributes(attribute.Float64("gen_ai.request.temperature", input.Temperature))
	}
	if len(input.InputMessages) > 0 {
		otelSpan.SetAttributes(attribute.String("gen_ai.input.messages",
			redact.Messages(input.InputMessages, cfg.TraceContent)))
	}

	result := &LLMResult{}

	// Capture the accumulator from ctx at span-start time. If there is no
	// enclosing AgentSpan the accumulator will be nil and Add is a no-op.
	acc := accumulator.FromContext(ctx)

	s := &Span{
		span:         otelSpan,
		result:       result,
		traceContent: cfg.TraceContent,
	}
	s.endAttributes = func() {
		if result.ResponseModel != "" {
			otelSpan.SetAttributes(attribute.String("gen_ai.response.model", result.ResponseModel))
		}
		if len(result.OutputMessages) > 0 {
			otelSpan.SetAttributes(attribute.String("gen_ai.output.messages",
				redact.Messages(result.OutputMessages, s.traceContent)))
		}
		// Always set usage — required by the observer.
		otelSpan.SetAttributes(
			attribute.Int64("gen_ai.usage.input_tokens", result.Usage.InputTokens),
			attribute.Int64("gen_ai.usage.output_tokens", result.Usage.OutputTokens),
		)
		if result.Usage.CacheReadInputTokens > 0 {
			otelSpan.SetAttributes(attribute.Int64("gen_ai.usage.cache_read_input_tokens", result.Usage.CacheReadInputTokens))
		}
		if result.Usage.CacheCreationInputTokens > 0 {
			otelSpan.SetAttributes(attribute.Int64("gen_ai.usage.cache_creation_input_tokens", result.Usage.CacheCreationInputTokens))
		}
		// Roll up this call's usage into the nearest ancestor agent span.
		// Safe when acc is nil (LLMSpan used without an enclosing AgentSpan).
		if acc != nil {
			acc.Add(
				result.Usage.InputTokens,
				result.Usage.OutputTokens,
				result.Usage.CacheReadInputTokens,
				result.Usage.CacheCreationInputTokens,
			)
		}
	}

	return ctx, s, result
}

// --------------------------------------------------------------------------
// Agent span
// --------------------------------------------------------------------------

// AgentInput holds the parameters for one agent root invocation. Required field
// is Name; all others are optional.
type AgentInput struct {
	// Name is the agent name (gen_ai.agent.name). Required.
	Name string
	// Description is the agent's human-readable description
	// (gen_ai.agent.description). Optional.
	Description string
	// Framework is the AI framework / vendor (gen_ai.system, shown as the
	// framework chip in the console). Optional.
	Framework string
	// RequestModel is the model the agent uses (gen_ai.request.model). Optional.
	RequestModel string
	// SystemInstructions is the system prompt text
	// (gen_ai.system_instructions). Optional; redacted when AMP_TRACE_CONTENT=false.
	SystemInstructions string
	// ConversationID is the conversation / session identifier
	// (gen_ai.conversation.id). Optional.
	ConversationID string
	// Tools is the list of tools available to the agent. Optional; serialised to
	// JSON and stored in gen_ai.agent.tools.
	Tools []map[string]any
	// InputMessages is the list of messages that initiated this invocation.
	// Optional; serialised to JSON with redaction applied.
	InputMessages []map[string]any
}

// AgentResult is filled by the caller after the agent invocation completes.
type AgentResult struct {
	// OutputMessages are the messages produced by the agent in response.
	// Optional; serialised to JSON with redaction applied at End().
	OutputMessages []map[string]any
}

// AgentSpan starts a root agent invocation span, returning (ctx, span, result).
// The caller fills result after the agent completes, then calls span.End().
//
// Attributes written at Start:
//   - gen_ai.operation.name = "invoke_agent"
//   - gen_ai.agent.name
//   - gen_ai.agent.description (if set)
//   - gen_ai.system (if Framework set)
//   - gen_ai.request.model (if set)
//   - gen_ai.system_instructions (if set; redacted when AMP_TRACE_CONTENT=false)
//   - gen_ai.conversation.id (if set)
//   - gen_ai.agent.tools (if Tools set)
//   - gen_ai.input.messages (if InputMessages set)
//
// Attributes written at End (from result):
//   - gen_ai.output.messages (if set)
func AgentSpan(ctx context.Context, input AgentInput) (context.Context, *Span, *AgentResult) {
	cfg := loadConfigCached()

	tracer := otel.GetTracerProvider().Tracer(tracerName)
	ctx, otelSpan := tracer.Start(ctx, "invoke_agent", trace.WithSpanKind(trace.SpanKindInternal))

	// Write start-time attributes.
	otelSpan.SetAttributes(
		attribute.String("gen_ai.operation.name", "invoke_agent"),
		attribute.String("gen_ai.agent.name", input.Name),
	)
	if input.Description != "" {
		otelSpan.SetAttributes(attribute.String("gen_ai.agent.description", input.Description))
	}
	if input.Framework != "" {
		otelSpan.SetAttributes(attribute.String("gen_ai.system", input.Framework))
	}
	if input.RequestModel != "" {
		otelSpan.SetAttributes(attribute.String("gen_ai.request.model", input.RequestModel))
	}
	if input.SystemInstructions != "" {
		otelSpan.SetAttributes(attribute.String("gen_ai.system_instructions",
			redact.Text(input.SystemInstructions, cfg.TraceContent)))
	}
	if input.ConversationID != "" {
		otelSpan.SetAttributes(attribute.String("gen_ai.conversation.id", input.ConversationID))
	}
	if len(input.Tools) > 0 {
		if b, err := json.Marshal(input.Tools); err == nil {
			otelSpan.SetAttributes(attribute.String("gen_ai.agent.tools", string(b)))
		}
	}
	if len(input.InputMessages) > 0 {
		otelSpan.SetAttributes(attribute.String("gen_ai.input.messages",
			redact.Messages(input.InputMessages, cfg.TraceContent)))
	}

	result := &AgentResult{}

	// Install a fresh accumulator into the context so that all descendant leaf
	// spans (LLMSpan, EmbeddingSpan) can roll their token usage up to this span.
	acc := &accumulator.Accumulator{}
	ctx = accumulator.NewContext(ctx, acc)

	s := &Span{
		span:         otelSpan,
		traceContent: cfg.TraceContent,
	}
	s.endAttributes = func() {
		if len(result.OutputMessages) > 0 {
			otelSpan.SetAttributes(attribute.String("gen_ai.output.messages",
				redact.Messages(result.OutputMessages, s.traceContent)))
		}
		// Read accumulated token totals from all child spans that ended before
		// this agent span ends. Children that end AFTER this point are not
		// counted (documented limit; see ADR 0001).
		totals := acc.Load()
		otelSpan.SetAttributes(
			attribute.Int64("gen_ai.usage.input_tokens", totals.InputTokens),
			attribute.Int64("gen_ai.usage.output_tokens", totals.OutputTokens),
		)
		if totals.CacheReadInputTokens > 0 {
			otelSpan.SetAttributes(attribute.Int64("gen_ai.usage.cache_read_input_tokens", totals.CacheReadInputTokens))
		}
		if totals.CacheCreationInputTokens > 0 {
			otelSpan.SetAttributes(attribute.Int64("gen_ai.usage.cache_creation_input_tokens", totals.CacheCreationInputTokens))
		}
	}

	return ctx, s, result
}

// --------------------------------------------------------------------------
// Tool span
// --------------------------------------------------------------------------

// ToolInput holds the parameters for one tool / function call. Required field
// is Name; all others are optional.
type ToolInput struct {
	// Name is the tool / function name (gen_ai.tool.name). Required.
	Name string
	// Description is the tool's human-readable description (gen_ai.tool.description). Optional.
	Description string
	// CallID is the tool call identifier from the LLM response (gen_ai.tool.call.id). Optional.
	CallID string
	// Arguments is the map of named arguments passed to the tool. Optional; serialised
	// to JSON and stored in traceloop.entity.input, with content redacted when
	// AMP_TRACE_CONTENT=false.
	Arguments map[string]any
}

// ToolResult is filled by the caller after the tool returns.
type ToolResult struct {
	// Output is the tool's return value. Optional; serialised to JSON and stored
	// in traceloop.entity.output, with content redacted when AMP_TRACE_CONTENT=false.
	Output any
}

// ToolSpan starts a tool execution span, returning (ctx, span, result). The
// caller fills result.Output after the tool returns, then calls span.End().
//
// Attributes written at Start:
//   - gen_ai.operation.name = "execute_tool"
//   - gen_ai.tool.name
//   - gen_ai.tool.description (if set)
//   - gen_ai.tool.call.id (if set)
//   - traceloop.entity.input (if Arguments is set)
//
// Attributes written at End (from result):
//   - traceloop.entity.output (if Output is set)
func ToolSpan(ctx context.Context, input ToolInput) (context.Context, *Span, *ToolResult) {
	cfg := loadConfigCached()

	tracer := otel.GetTracerProvider().Tracer(tracerName)
	ctx, otelSpan := tracer.Start(ctx, "execute_tool", trace.WithSpanKind(trace.SpanKindInternal))

	// Write start-time attributes.
	otelSpan.SetAttributes(
		attribute.String("gen_ai.operation.name", "execute_tool"),
		attribute.String("gen_ai.tool.name", input.Name),
	)
	if input.Description != "" {
		otelSpan.SetAttributes(attribute.String("gen_ai.tool.description", input.Description))
	}
	if input.CallID != "" {
		otelSpan.SetAttributes(attribute.String("gen_ai.tool.call.id", input.CallID))
	}
	if len(input.Arguments) > 0 {
		redacted := redact.Value(map[string]any(input.Arguments), cfg.TraceContent)
		if b, err := json.Marshal(redacted); err == nil {
			otelSpan.SetAttributes(attribute.String("traceloop.entity.input", string(b)))
		}
	}

	result := &ToolResult{}

	s := &Span{
		span:         otelSpan,
		traceContent: cfg.TraceContent,
	}
	s.endAttributes = func() {
		if result.Output != nil {
			redacted := redact.Value(result.Output, s.traceContent)
			if b, err := json.Marshal(redacted); err == nil {
				otelSpan.SetAttributes(attribute.String("traceloop.entity.output", string(b)))
			}
		}
	}

	return ctx, s, result
}

// --------------------------------------------------------------------------
// Embedding span
// --------------------------------------------------------------------------

// EmbeddingInput holds the parameters for one embedding call. Required fields
// are System and RequestModel; Texts is the list of strings to embed.
type EmbeddingInput struct {
	// System is the AI provider / vendor (e.g. "voyage", "openai").
	System string
	// RequestModel is the model identifier requested by the caller.
	RequestModel string
	// Texts are the strings being embedded. Each is recorded as
	// gen_ai.prompt.{i}.content. Redacted when AMP_TRACE_CONTENT=false.
	Texts []string
}

// EmbeddingResult is filled by the caller after the embedding call returns.
type EmbeddingResult struct {
	// ResponseModel is the model that actually answered (may differ from request).
	ResponseModel string
	// InputTokens is gen_ai.usage.input_tokens for this embedding call.
	InputTokens int64
}

// EmbeddingSpan starts an embedding span, returning (ctx, span, result). The
// caller fills result after the embedding call returns, then calls span.End().
//
// Attributes written at Start:
//   - gen_ai.operation.name = "embeddings"   (observer discriminator — resolves to embedding kind)
//   - gen_ai.system
//   - gen_ai.request.model
//   - gen_ai.prompt.{i}.content for each input text (redacted when AMP_TRACE_CONTENT=false)
//
// Attributes written at End (from result):
//   - gen_ai.response.model (if set)
//   - gen_ai.usage.input_tokens
func EmbeddingSpan(ctx context.Context, input EmbeddingInput) (context.Context, *Span, *EmbeddingResult) {
	cfg := loadConfigCached()

	tracer := otel.GetTracerProvider().Tracer(tracerName)
	ctx, otelSpan := tracer.Start(ctx, "embeddings", trace.WithSpanKind(trace.SpanKindClient))

	// Write start-time attributes.
	otelSpan.SetAttributes(
		attribute.String("gen_ai.operation.name", "embeddings"),
		attribute.String("gen_ai.system", input.System),
		attribute.String("gen_ai.request.model", input.RequestModel),
	)
	for i, text := range input.Texts {
		otelSpan.SetAttributes(attribute.String(
			fmt.Sprintf("gen_ai.prompt.%d.content", i),
			redact.Text(text, cfg.TraceContent),
		))
	}

	result := &EmbeddingResult{}

	// Capture the accumulator from ctx at span-start time so that this
	// embedding call's input tokens are rolled up to the nearest agent span.
	acc := accumulator.FromContext(ctx)

	s := &Span{
		span:         otelSpan,
		traceContent: cfg.TraceContent,
	}
	s.endAttributes = func() {
		if result.ResponseModel != "" {
			otelSpan.SetAttributes(attribute.String("gen_ai.response.model", result.ResponseModel))
		}
		otelSpan.SetAttributes(attribute.Int64("gen_ai.usage.input_tokens", result.InputTokens))
		// Roll up embedding input tokens; embeddings have no output tokens.
		// Safe when acc is nil (EmbeddingSpan used without an enclosing AgentSpan).
		if acc != nil {
			acc.Add(result.InputTokens, 0, 0, 0)
		}
	}

	return ctx, s, result
}

// --------------------------------------------------------------------------
// Retriever span
// --------------------------------------------------------------------------

// RetrieverInput holds the parameters for one vector-DB retrieval. Required
// field is VectorDB; all others are optional.
//
// This span uses OTel database semantic conventions (db.*), not gen_ai.*.
// The observer resolves it to the "retriever" kind by keying off
// db.system.name being a known vector-DB system name.
//
// Retrieved documents are not extracted by the observer in v1 and are
// therefore not modelled here. There is no free-form text content in this
// span; no redaction is applied (matching the Python retriever_span reference).
type RetrieverInput struct {
	// VectorDB is the vector database system name (db.system.name). Required.
	// Must be a value recognised by the observer as a vector DB, e.g.
	// "pinecone", "weaviate", "qdrant", "milvus", "chroma", "chromadb",
	// "pgvector".
	VectorDB string
	// Collection is the collection / index name (db.collection.name). Optional.
	Collection string
	// TopK is the number of nearest neighbours requested
	// (db.vector.query.top_k). Optional; zero value omits the attribute.
	TopK int64
}

// RetrieverSpan starts a vector-DB retrieval span, returning (ctx, span). Unlike
// LLMSpan/EmbeddingSpan there is no result handle: the observer extracts no
// response fields from retriever spans in v1, so all attributes are written at
// Start. The caller calls span.End() when the retrieval completes.
//
// Attributes written at Start:
//   - db.system.name   (required — observer discriminator for retriever kind)
//   - db.collection.name (if set)
//   - db.vector.query.top_k (if non-zero)
func RetrieverSpan(ctx context.Context, input RetrieverInput) (context.Context, *Span) {
	tracer := otel.GetTracerProvider().Tracer(tracerName)
	ctx, otelSpan := tracer.Start(ctx, "vector_search", trace.WithSpanKind(trace.SpanKindClient))

	// db.system.name is required — it is the observer's discriminator.
	otelSpan.SetAttributes(attribute.String("db.system.name", input.VectorDB))
	if input.Collection != "" {
		otelSpan.SetAttributes(attribute.String("db.collection.name", input.Collection))
	}
	if input.TopK > 0 {
		otelSpan.SetAttributes(attribute.Int64("db.vector.query.top_k", input.TopK))
	}

	s := &Span{
		span: otelSpan,
	}
	// No endAttributes: all attributes are written at Start.

	return ctx, s
}

// --------------------------------------------------------------------------
// Rerank span
// --------------------------------------------------------------------------

// RerankInput holds the parameters for one reranking step. Model is required;
// Query and CandidateCount are the key signals set on the span.
//
// Rerank is recognized as a kind only (no data card in v1). The observer keys
// it off the Layer-2 traceloop.span.kind = "rerank" attribute. The de-facto
// signal keys (gen_ai.operation.name, rerank.model, gen_ai.request.model, and
// traceloop.entity.input) mirror the Python rerank_span reference exactly.
type RerankInput struct {
	// Model is the reranker model identifier. Required.
	// Set as both rerank.model and gen_ai.request.model.
	Model string
	// Query is the text query being reranked. Redacted when AMP_TRACE_CONTENT=false.
	Query string
	// CandidateCount is the number of candidate documents provided to the reranker.
	// Set inside traceloop.entity.input as "candidate_count". Optional; zero omits it.
	CandidateCount int
}

// RerankSpan starts a reranking span, returning (ctx, span). Like RetrieverSpan
// there is no result handle: the observer extracts no response fields from rerank
// spans in v1, so all attributes are written at Start. The caller calls
// span.End() when the reranking step completes.
//
// The span name is "rerank" and SpanKind is CLIENT (matching the Python reference).
//
// Attributes written at Start:
//   - traceloop.span.kind = "rerank"   (required — sole published contract key)
//   - gen_ai.operation.name = "rerank" (de-facto signal; observer fallback)
//   - rerank.model                     (de-facto signal; observer fallback)
//   - gen_ai.request.model             (same value as rerank.model)
//   - traceloop.entity.input           (JSON: {"query":..., "candidate_count":...})
//     Query is redacted when AMP_TRACE_CONTENT=false; candidate_count is preserved.
func RerankSpan(ctx context.Context, input RerankInput) (context.Context, *Span) {
	cfg := loadConfigCached()

	tracer := otel.GetTracerProvider().Tracer(tracerName)
	ctx, otelSpan := tracer.Start(ctx, "rerank", trace.WithSpanKind(trace.SpanKindClient))

	// Write discriminator and de-facto signal keys at Start.
	otelSpan.SetAttributes(
		attribute.String("traceloop.span.kind", "rerank"),
		attribute.String("gen_ai.operation.name", "rerank"),
		attribute.String("rerank.model", input.Model),
		attribute.String("gen_ai.request.model", input.Model),
	)

	// Build traceloop.entity.input carrying query (redactable) + candidate_count.
	// Mirrors the Python reference:
	//   span.set_attribute("traceloop.entity.input", json.dumps({
	//       "query": _content(query), "candidate_count": candidate_count,
	//   }))
	entityInput := map[string]any{
		"query":           redact.Text(input.Query, cfg.TraceContent),
		"candidate_count": input.CandidateCount,
	}
	if b, err := json.Marshal(entityInput); err == nil {
		otelSpan.SetAttributes(attribute.String("traceloop.entity.input", string(b)))
	}

	s := &Span{
		span:         otelSpan,
		traceContent: cfg.TraceContent,
	}
	// No endAttributes: all attributes are written at Start (no result handle).

	return ctx, s
}

// --------------------------------------------------------------------------
// Chain span
// --------------------------------------------------------------------------

// ChainInput holds the parameters for one chain / workflow step. Name is
// required (it becomes the span name); Input is optional.
//
// The chain kind has no OTel gen_ai.* discriminator. The observer resolves it
// from the Layer-2 key traceloop.span.kind = "workflow" (process.go:
// case "task", "workflow": return SpanTypeChain).
type ChainInput struct {
	// Name is the workflow / pipeline step name. Required; used as the span name.
	// Mirrors the Python chain_span(name=...) parameter.
	Name string
	// Input is the workflow's input payload. Optional; serialised to JSON as
	// {"input": <value>} and stored in traceloop.entity.input. Redacted when
	// AMP_TRACE_CONTENT=false.
	Input any
}

// ChainResult is filled by the caller after the chain step completes.
type ChainResult struct {
	// Output is the chain step's result value. Optional; serialised to JSON as
	// {"output": <value>} and stored in traceloop.entity.output at span.End().
	// Redacted when AMP_TRACE_CONTENT=false.
	Output any
}

// ChainSpan starts a chain / workflow span, returning (ctx, span, result). The
// caller fills result.Output after the step completes, then calls span.End().
//
// This span kind is resolved by the observer purely through the Layer-2
// traceloop.span.kind = "workflow" attribute — OTel has no standardised key for
// the chain/workflow concept. The Python reference (instrumentation.py →
// chain_span) is the authoritative source for this attribute set.
//
// Attributes written at Start:
//   - traceloop.span.kind = "workflow"  (required — observer discriminator → chain kind)
//   - traceloop.entity.input            (optional; present when Input is non-nil)
//
// Attributes written at End (from result):
//   - traceloop.entity.output           (optional; present when result.Output is non-nil)
func ChainSpan(ctx context.Context, input ChainInput) (context.Context, *Span, *ChainResult) {
	cfg := loadConfigCached()

	tracer := otel.GetTracerProvider().Tracer(tracerName)
	ctx, otelSpan := tracer.Start(ctx, input.Name, trace.WithSpanKind(trace.SpanKindInternal))

	// Write the Layer-2 discriminator — the observer resolves this to SpanTypeChain.
	otelSpan.SetAttributes(attribute.String("traceloop.span.kind", "workflow"))

	// Write input entity if provided. JSON envelope: {"input": <value>}.
	// Mirrors Python: json.dumps({"input": _redact_value(workflow_input)})
	if input.Input != nil {
		redacted := redact.Value(input.Input, cfg.TraceContent)
		envelope := map[string]any{"input": redacted}
		if b, err := json.Marshal(envelope); err == nil {
			otelSpan.SetAttributes(attribute.String("traceloop.entity.input", string(b)))
		}
	}

	result := &ChainResult{}

	s := &Span{
		span:         otelSpan,
		traceContent: cfg.TraceContent,
	}
	s.endAttributes = func() {
		if result.Output != nil {
			redacted := redact.Value(result.Output, s.traceContent)
			envelope := map[string]any{"output": redacted}
			if b, err := json.Marshal(envelope); err == nil {
				otelSpan.SetAttributes(attribute.String("traceloop.entity.output", string(b)))
			}
		}
	}

	return ctx, s, result
}

// --------------------------------------------------------------------------
// config cache (avoid re-reading env on every span)
// --------------------------------------------------------------------------

var cachedConfig *config.Config

// loadConfigCached returns the last successfully loaded config, or a default
// config if Init was never called. This is a best-effort fallback; callers
// should always call Init first.
func loadConfigCached() *config.Config {
	if cachedConfig != nil {
		return cachedConfig
	}
	cfg, err := config.Load()
	if err != nil {
		// Return a safe default that leaves content on.
		return &config.Config{TraceContent: true}
	}
	cachedConfig = cfg
	return cfg
}

// ResetConfigCache clears the package-level config cache so that the next span
// call re-reads the environment. Intended for use in tests only.
func ResetConfigCache() {
	cachedConfig = nil
}
