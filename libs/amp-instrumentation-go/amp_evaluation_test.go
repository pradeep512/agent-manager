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

// Package amp_test contains tests for the WithEvaluation helper (issue #11).
//
// WithEvaluation attaches task.id / trial.id to the root agent span so the
// observer (traces-observer-service/controllers/controller.go lines 676-681)
// can correlate traces back to evaluation runs.
package amp_test

import (
	"context"
	"testing"

	amp "github.com/wso2/agent-manager/libs/amp-instrumentation-go"
)

// TestWithEvaluation_RootSpanCarriesTaskAndTrialID is the tracer-bullet test.
// It verifies the core behavior: when WithEvaluation is called before AgentSpan,
// the root agent span carries task.id and trial.id as span attributes — exactly
// the keys the controller reads (controller.go:676-681).
func TestWithEvaluation_RootSpanCarriesTaskAndTrialID(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx = amp.WithEvaluation(ctx, "task-abc", "trial-123")

	_, span, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "eval-agent"})
	span.End()

	s := spanByName(rec, "invoke_agent")
	if s == nil {
		t.Fatal("invoke_agent span not found")
	}
	attrs := attrMap(s)

	if attrs["task.id"] != "task-abc" {
		t.Errorf("task.id = %v, want %q", attrs["task.id"], "task-abc")
	}
	if attrs["trial.id"] != "trial-123" {
		t.Errorf("trial.id = %v, want %q", attrs["trial.id"], "trial-123")
	}
}

// TestWithEvaluation_EmptyTaskIDSetsNothing verifies the graceful empty rule:
// an empty taskID sets neither task.id nor trial.id on the root span.
func TestWithEvaluation_EmptyTaskIDSetsNothing(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx = amp.WithEvaluation(ctx, "", "trial-123")

	_, span, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "no-task-agent"})
	span.End()

	s := spanByName(rec, "invoke_agent")
	if s == nil {
		t.Fatal("invoke_agent span not found")
	}
	attrs := attrMap(s)

	if _, ok := attrs["task.id"]; ok {
		t.Error("task.id should be absent when taskID is empty")
	}
	if _, ok := attrs["trial.id"]; ok {
		t.Error("trial.id should be absent when taskID is empty")
	}
}

// TestWithEvaluation_EmptyTrialIDSetsNothing verifies the graceful empty rule:
// an empty trialID sets neither task.id nor trial.id on the root span.
func TestWithEvaluation_EmptyTrialIDSetsNothing(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx = amp.WithEvaluation(ctx, "task-abc", "")

	_, span, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "no-trial-agent"})
	span.End()

	s := spanByName(rec, "invoke_agent")
	if s == nil {
		t.Fatal("invoke_agent span not found")
	}
	attrs := attrMap(s)

	if _, ok := attrs["task.id"]; ok {
		t.Error("task.id should be absent when trialID is empty")
	}
	if _, ok := attrs["trial.id"]; ok {
		t.Error("trial.id should be absent when trialID is empty")
	}
}

// TestWithEvaluation_BothEmptySetsNothing verifies that calling WithEvaluation
// with both IDs empty is a no-op — no spurious attributes on the root span.
func TestWithEvaluation_BothEmptySetsNothing(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx = amp.WithEvaluation(ctx, "", "")

	_, span, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "empty-eval-agent"})
	span.End()

	s := spanByName(rec, "invoke_agent")
	if s == nil {
		t.Fatal("invoke_agent span not found")
	}
	attrs := attrMap(s)

	if _, ok := attrs["task.id"]; ok {
		t.Error("task.id should be absent when both IDs are empty")
	}
	if _, ok := attrs["trial.id"]; ok {
		t.Error("trial.id should be absent when both IDs are empty")
	}
}

// TestWithEvaluation_AbsentWhenNotCalled verifies that when WithEvaluation is
// not called at all, the root span has no task.id or trial.id attributes.
func TestWithEvaluation_AbsentWhenNotCalled(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	_, span, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "plain-agent"})
	span.End()

	s := spanByName(rec, "invoke_agent")
	if s == nil {
		t.Fatal("invoke_agent span not found")
	}
	attrs := attrMap(s)

	if _, ok := attrs["task.id"]; ok {
		t.Error("task.id should be absent when WithEvaluation was not called")
	}
	if _, ok := attrs["trial.id"]; ok {
		t.Error("trial.id should be absent when WithEvaluation was not called")
	}
}

// TestWithEvaluation_DoesNotAffectChildSpans verifies that task.id / trial.id
// are ONLY set on the root agent span — not on child LLM or tool spans.
func TestWithEvaluation_DoesNotAffectChildSpans(t *testing.T) {
	rec := setupInMemoryProvider(t)

	ctx := context.Background()
	ctx = amp.WithEvaluation(ctx, "task-x", "trial-y")

	ctx, agentSpan, _ := amp.AgentSpan(ctx, amp.AgentInput{Name: "parent-agent"})

	_, llmSpan, _ := amp.LLMSpan(ctx, amp.LLMInput{
		System:       "anthropic",
		RequestModel: "claude-3-5-sonnet",
	})
	llmSpan.End()
	agentSpan.End()

	llm := spanByName(rec, "chat")
	if llm == nil {
		t.Fatal("chat span not found")
	}
	llmAttrs := attrMap(llm)

	// Child LLM span must NOT carry evaluation correlation attributes.
	if _, ok := llmAttrs["task.id"]; ok {
		t.Error("task.id should not be set on child LLM spans")
	}
	if _, ok := llmAttrs["trial.id"]; ok {
		t.Error("trial.id should not be set on child LLM spans")
	}
}
