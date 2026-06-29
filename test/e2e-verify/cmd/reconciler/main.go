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

// reconciler — issue #18
//
// Cross-layer token-total reconciler: the authoritative proof that the #14
// observer change reads cache_creation tokens from a real ingested span and
// totals them at the trace level.
//
// Two assertions:
//  1. At least one LLM span carries cacheCreationInputTokens > 0 in its
//     ampAttributes.data.tokenUsage (the observer surfaced the field).
//  2. The trace-level tokenUsage.totalTokens equals
//     inputTokens + outputTokens + cacheReadInputTokens + cacheCreationInputTokens
//     (recomputed arithmetically — never "looks bigger").
//
// Per-span TotalTokens stays input+output by design; do not expect cache folded
// there.  Missing cache_read is informational, not a failure.
//
// Verdicts: PASS / FAIL (INCONCLUSIVE is the probe's job, not ours).
//
// Offline (fixtures):
//
//	reconciler --traces-file testdata/fixture_observer_traces.json \
//	           --llm-span-files testdata/fixture_observer_span_llm1_cache_write.json,testdata/fixture_observer_span_llm2_cache_read.json
//
// Live:
//
//	reconciler --observer-url http://localhost:9098 \
//	           --bearer <jwt> \
//	           --org default --project default --agent e2e-cache-agent --env default \
//	           --trace-id <traceId> --start <RFC3339> --end <RFC3339>
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// Observer API JSON types
// ---------------------------------------------------------------------------

// tracesResponse mirrors GET /api/v1/traces.
type tracesResponse struct {
	Traces     []traceOverview `json:"traces"`
	TotalCount int             `json:"totalCount"`
}

type traceOverview struct {
	TraceID    string     `json:"traceId"`
	TokenUsage tokenUsage `json:"tokenUsage"`
}

type tokenUsage struct {
	InputTokens              int64 `json:"inputTokens"`
	OutputTokens             int64 `json:"outputTokens"`
	CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
	TotalTokens              int64 `json:"totalTokens"`
}

// spansListResponse mirrors GET /api/v1/traces/{traceId}/spans.
type spansListResponse struct {
	Spans      []spanSummary `json:"spans"`
	TotalCount int           `json:"totalCount"`
}

type spanSummary struct {
	SpanID   string `json:"spanId"`
	SpanName string `json:"spanName"`
	SpanKind string `json:"spanKind"`
}

// spanDetailResponse mirrors GET /api/v1/traces/{traceId}/spans/{spanId}.
type spanDetailResponse struct {
	TraceID       string        `json:"traceId"`
	SpanID        string        `json:"spanId"`
	Name          string        `json:"name"`
	AmpAttributes ampAttributes `json:"ampAttributes"`
}

type ampAttributes struct {
	Kind string      `json:"kind"`
	Data ampDataNode `json:"data"`
}

type ampDataNode struct {
	TokenUsage spanTokenUsage `json:"tokenUsage"`
}

// spanTokenUsage is the per-span token breakdown in ampAttributes.
// Per #14 design: TotalTokens == InputTokens + OutputTokens (cache NOT folded here).
type spanTokenUsage struct {
	InputTokens              int64 `json:"inputTokens"`
	OutputTokens             int64 `json:"outputTokens"`
	CacheReadInputTokens     int64 `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int64 `json:"cacheCreationInputTokens"`
	TotalTokens              int64 `json:"totalTokens"`
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	// Offline flags
	tracesFile := flag.String("traces-file", "", "path to observer /api/v1/traces JSON fixture")
	llmSpanFiles := flag.String("llm-span-files", "", "comma-separated paths to LLM span detail JSON fixtures")

	// Live flags
	observerURL := flag.String("observer-url", "", "observer base URL, e.g. http://localhost:9098")
	bearer := flag.String("bearer", "", "Bearer JWT for /api/v1/* auth")
	org := flag.String("org", "default", "organization name")
	project := flag.String("project", "default", "project name")
	agent := flag.String("agent", "", "agent name")
	env := flag.String("env", "default", "environment name")
	traceID := flag.String("trace-id", "", "traceId to verify (live mode)")
	startTime := flag.String("start", "", "startTime in RFC3339 (live mode)")
	endTime := flag.String("end", "", "endTime in RFC3339 (live mode)")

	flag.Parse()

	offline := *tracesFile != "" || *llmSpanFiles != ""
	live := *observerURL != ""

	if !offline && !live {
		printUsage()
		os.Exit(1)
	}

	var tu tokenUsage
	var llmSpanDetails []spanDetailResponse
	var err error

	if offline {
		if *tracesFile == "" || *llmSpanFiles == "" {
			fmt.Fprintln(os.Stderr, "offline mode requires both --traces-file and --llm-span-files")
			os.Exit(1)
		}
		tu, llmSpanDetails, err = loadOffline(*tracesFile, *llmSpanFiles)
	} else {
		if *bearer == "" || *traceID == "" || *startTime == "" || *endTime == "" || *agent == "" {
			fmt.Fprintln(os.Stderr, "live mode requires: --bearer, --agent, --trace-id, --start, --end")
			os.Exit(1)
		}
		tu, llmSpanDetails, err = fetchLive(*observerURL, *bearer, *org, *project, *agent, *env, *traceID, *startTime, *endTime)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "data load failed: %v\n", err)
		os.Exit(1)
	}

	runReconcile(tu, llmSpanDetails)
}

// ---------------------------------------------------------------------------
// Offline loader
// ---------------------------------------------------------------------------

func loadOffline(tracesFilePath, llmSpanFilePaths string) (tokenUsage, []spanDetailResponse, error) {
	// Load trace overview
	raw, err := os.ReadFile(tracesFilePath)
	if err != nil {
		return tokenUsage{}, nil, fmt.Errorf("read traces file: %w", err)
	}
	var tr tracesResponse
	if err = json.Unmarshal(raw, &tr); err != nil {
		return tokenUsage{}, nil, fmt.Errorf("parse traces file: %w", err)
	}
	if len(tr.Traces) == 0 {
		return tokenUsage{}, nil, fmt.Errorf("traces file contains no trace entries")
	}
	tu := tr.Traces[0].TokenUsage

	// Load each LLM span detail file
	var details []spanDetailResponse
	for _, p := range strings.Split(llmSpanFilePaths, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return tokenUsage{}, nil, fmt.Errorf("read span file %s: %w", p, err)
		}
		var d spanDetailResponse
		if err = json.Unmarshal(b, &d); err != nil {
			return tokenUsage{}, nil, fmt.Errorf("parse span file %s: %w", p, err)
		}
		if d.AmpAttributes.Kind == "llm" {
			details = append(details, d)
		} else {
			fmt.Printf("  skipping span %s (kind=%q, not llm)\n", d.SpanID, d.AmpAttributes.Kind)
		}
	}
	if len(details) == 0 {
		return tokenUsage{}, nil, fmt.Errorf("no LLM span detail files provided or none had kind=llm")
	}
	return tu, details, nil
}

// ---------------------------------------------------------------------------
// Live loader
// ---------------------------------------------------------------------------

func fetchLive(baseURL, bearer, org, project, agentName, envName, traceID, start, end string) (tokenUsage, []spanDetailResponse, error) {
	client := &http.Client{}

	// 1. GET /api/v1/traces — find our trace
	params := url.Values{
		"organization": {org},
		"project":      {project},
		"agent":        {agentName},
		"environment":  {envName},
		"startTime":    {start},
		"endTime":      {end},
		"limit":        {"100"},
	}
	tracesURL := fmt.Sprintf("%s/api/v1/traces?%s", baseURL, params.Encode())
	body, err := doGet(client, tracesURL, bearer)
	if err != nil {
		return tokenUsage{}, nil, fmt.Errorf("GET /api/v1/traces: %w", err)
	}
	var tr tracesResponse
	if err = json.Unmarshal(body, &tr); err != nil {
		return tokenUsage{}, nil, fmt.Errorf("parse /api/v1/traces: %w", err)
	}

	var tu tokenUsage
	found := false
	for _, t := range tr.Traces {
		if t.TraceID == traceID {
			tu = t.TokenUsage
			found = true
			break
		}
	}
	if !found {
		return tokenUsage{}, nil, fmt.Errorf("traceId %s not found in /api/v1/traces response (%d entries)", traceID, len(tr.Traces))
	}

	// 2. GET /api/v1/traces/{traceId}/spans — list spans, find LLM ones
	spansParams := url.Values{
		"organization": {org},
		"startTime":    {start},
		"endTime":      {end},
		"limit":        {"1000"},
	}
	spansURL := fmt.Sprintf("%s/api/v1/traces/%s/spans?%s", baseURL, traceID, spansParams.Encode())
	body, err = doGet(client, spansURL, bearer)
	if err != nil {
		return tokenUsage{}, nil, fmt.Errorf("GET /api/v1/traces/{id}/spans: %w", err)
	}
	var sl spansListResponse
	if err = json.Unmarshal(body, &sl); err != nil {
		return tokenUsage{}, nil, fmt.Errorf("parse spans list: %w", err)
	}

	// 3. For each LLM span, GET /api/v1/traces/{traceId}/spans/{spanId}
	var details []spanDetailResponse
	for _, s := range sl.Spans {
		if s.SpanKind != "llm" {
			continue
		}
		detailURL := fmt.Sprintf("%s/api/v1/traces/%s/spans/%s", baseURL, traceID, s.SpanID)
		body, err = doGet(client, detailURL, bearer)
		if err != nil {
			return tokenUsage{}, nil, fmt.Errorf("GET span detail %s: %w", s.SpanID, err)
		}
		var d spanDetailResponse
		if err = json.Unmarshal(body, &d); err != nil {
			return tokenUsage{}, nil, fmt.Errorf("parse span detail %s: %w", s.SpanID, err)
		}
		details = append(details, d)
	}
	if len(details) == 0 {
		return tokenUsage{}, nil, fmt.Errorf("no LLM spans found in trace %s", traceID)
	}
	return tu, details, nil
}

// ---------------------------------------------------------------------------
// Reconciliation logic
// ---------------------------------------------------------------------------

func runReconcile(tu tokenUsage, llmSpans []spanDetailResponse) {
	fmt.Printf("reconciler\n")
	fmt.Printf("  trace tokenUsage:\n")
	fmt.Printf("    inputTokens:              %d\n", tu.InputTokens)
	fmt.Printf("    outputTokens:             %d\n", tu.OutputTokens)
	fmt.Printf("    cacheReadInputTokens:     %d  (informational)\n", tu.CacheReadInputTokens)
	fmt.Printf("    cacheCreationInputTokens: %d\n", tu.CacheCreationInputTokens)
	fmt.Printf("    totalTokens (reported):   %d\n", tu.TotalTokens)

	// Assertion 1: recompute total
	expected := tu.InputTokens + tu.OutputTokens + tu.CacheReadInputTokens + tu.CacheCreationInputTokens
	fmt.Printf("\n  recomputed total: %d + %d + %d + %d = %d\n",
		tu.InputTokens, tu.OutputTokens, tu.CacheReadInputTokens, tu.CacheCreationInputTokens, expected)

	totalOK := expected == tu.TotalTokens
	if totalOK {
		fmt.Printf("  [OK] totalTokens matches recomputed value (%d == %d)\n", tu.TotalTokens, expected)
	} else {
		fmt.Printf("  [FAIL] totalTokens mismatch: reported=%d, recomputed=%d\n", tu.TotalTokens, expected)
	}

	// Assertion 2: at least one LLM span must carry cacheCreationInputTokens > 0
	fmt.Printf("\n  LLM spans checked:\n")
	var cacheCreationSpan *spanDetailResponse
	for i := range llmSpans {
		sd := &llmSpans[i]
		stu := sd.AmpAttributes.Data.TokenUsage
		fmt.Printf("    spanId=%s  input=%d  output=%d  cacheRead=%d  cacheCreation=%d  spanTotal=%d\n",
			sd.SpanID, stu.InputTokens, stu.OutputTokens, stu.CacheReadInputTokens, stu.CacheCreationInputTokens, stu.TotalTokens)
		// Verify per-span total stays input+output (cache NOT folded at span level).
		expectedSpanTotal := stu.InputTokens + stu.OutputTokens
		if stu.TotalTokens != expectedSpanTotal {
			fmt.Printf("      [NOTE] per-span totalTokens=%d != input+output=%d (unexpected — check observer)\n",
				stu.TotalTokens, expectedSpanTotal)
		}
		if stu.CacheCreationInputTokens > 0 && cacheCreationSpan == nil {
			cacheCreationSpan = sd
		}
	}

	spanOK := cacheCreationSpan != nil
	if spanOK {
		fmt.Printf("  [OK] LLM span %s carries cacheCreationInputTokens=%d\n",
			cacheCreationSpan.SpanID, cacheCreationSpan.AmpAttributes.Data.TokenUsage.CacheCreationInputTokens)
	} else {
		fmt.Printf("  [FAIL] no LLM span carries cacheCreationInputTokens > 0\n")
	}

	// Final verdict
	fmt.Println()
	if totalOK && spanOK {
		fmt.Printf("VERDICT: PASS\n")
		fmt.Printf("  #14 observer reads cache_creation_input_tokens and totals input+output+cacheRead+cacheCreation\n")
		os.Exit(0)
	}
	fmt.Printf("VERDICT: FAIL\n")
	if !totalOK {
		fmt.Printf("  trace totalTokens=%d does not match recomputed=%d\n", tu.TotalTokens, expected)
	}
	if !spanOK {
		fmt.Printf("  no LLM span in the observer response carries cacheCreationInputTokens\n")
	}
	os.Exit(1)
}

// ---------------------------------------------------------------------------
// HTTP helper
// ---------------------------------------------------------------------------

func doGet(client *http.Client, rawURL, bearer string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", rawURL, err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body from %s: %w", rawURL, err)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, rawURL, string(body))
	}
	return body, nil
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage (offline):")
	fmt.Fprintln(os.Stderr, "  reconciler --traces-file <traces.json> --llm-span-files <span1.json,span2.json>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "usage (live):")
	fmt.Fprintln(os.Stderr, "  reconciler --observer-url <url> --bearer <jwt> --org <org> --project <proj>")
	fmt.Fprintln(os.Stderr, "             --agent <agent> --env <env> --trace-id <traceId> --start <RFC3339> --end <RFC3339>")
}
