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

// Package accumulator provides a concurrency-safe token-usage accumulator that
// is scoped to the lifetime of an agent span via context.
//
// # Design (ADR 0001)
//
// AgentSpan installs one Accumulator into the ctx it returns. Each leaf span
// (LLMSpan, EmbeddingSpan) calls FromContext to retrieve the accumulator and
// adds its per-call token counts at End(). AgentSpan reads the aggregate totals
// at End() and stamps them as gen_ai.usage.* attributes on itself.
//
// The accumulator uses sync/atomic for all field updates so multiple goroutines
// may call Add concurrently without a mutex or data race.
//
// Documented limit: children that end AFTER the agent span ends are not counted.
// The agent span reads the accumulator once during its own End() — any Add calls
// that arrive after that point are silently ignored (they are safe but have no
// effect on the already-ended agent span).
package accumulator

import (
	"context"
	"sync/atomic"
)

// contextKey is an unexported type used as the context key for the accumulator,
// preventing collisions with other packages' context values.
type contextKey struct{}

// Accumulator holds running token totals for one agent span's trace subtree.
// All fields are updated atomically so concurrent leaf spans are race-free.
//
// Field semantics follow the AMP/OTel gen_ai.usage.* contract:
//   - InputTokens  — raw uncached prompt tokens (gen_ai.usage.input_tokens)
//   - OutputTokens — generated tokens          (gen_ai.usage.output_tokens)
//   - CacheReadInputTokens    — tokens read from the prompt cache
//   - CacheCreationInputTokens — tokens written to the prompt cache (cache write)
type Accumulator struct {
	inputTokens              atomic.Int64
	outputTokens             atomic.Int64
	cacheReadInputTokens     atomic.Int64
	cacheCreationInputTokens atomic.Int64
}

// Totals holds the accumulated token sums read from an Accumulator.
type Totals struct {
	InputTokens              int64
	OutputTokens             int64
	CacheReadInputTokens     int64
	CacheCreationInputTokens int64
}

// Add atomically adds the provided token counts to the accumulator.
// It is safe to call from multiple goroutines simultaneously.
func (a *Accumulator) Add(input, output, cacheRead, cacheCreation int64) {
	if input != 0 {
		a.inputTokens.Add(input)
	}
	if output != 0 {
		a.outputTokens.Add(output)
	}
	if cacheRead != 0 {
		a.cacheReadInputTokens.Add(cacheRead)
	}
	if cacheCreation != 0 {
		a.cacheCreationInputTokens.Add(cacheCreation)
	}
}

// Load reads the current totals snapshot. The snapshot is consistent with
// respect to each individual counter but is not an atomic snapshot across all
// four fields — callers (AgentSpan.End) should call Load only once all child
// spans have ended, which is the normal defer-End pattern.
func (a *Accumulator) Load() Totals {
	return Totals{
		InputTokens:              a.inputTokens.Load(),
		OutputTokens:             a.outputTokens.Load(),
		CacheReadInputTokens:     a.cacheReadInputTokens.Load(),
		CacheCreationInputTokens: a.cacheCreationInputTokens.Load(),
	}
}

// NewContext returns a new context that carries acc as the active accumulator.
// Typically called by AgentSpan immediately after starting the agent OTel span.
func NewContext(ctx context.Context, acc *Accumulator) context.Context {
	return context.WithValue(ctx, contextKey{}, acc)
}

// FromContext retrieves the Accumulator stored in ctx by NewContext. Returns nil
// if no accumulator is present (e.g. an LLMSpan used outside any agent span).
// Callers must nil-check before calling Add.
func FromContext(ctx context.Context) *Accumulator {
	acc, _ := ctx.Value(contextKey{}).(*Accumulator)
	return acc
}
