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

// opensearch-probe — issue #17
//
// Anti-false-green gate for the #14 e2e verification harness.
// Given an OpenSearch _search response (from a file fixture or a live query),
// it extracts the cache-token fields from the first LLM "chat" span that
// carries gen_ai.usage.cache_creation_input_tokens and emits a verdict:
//
//   GATE OK      — cache_creation_input_tokens > 0; proceed to reconciler.
//   INCONCLUSIVE — attribute absent or zero in all spans; do not call this FAIL.
//
// Offline (fixture):
//
//	opensearch-probe --file testdata/fixture_opensearch_search.json
//
// Live:
//
//	opensearch-probe --url https://localhost:9200 --index otel-traces-2026-06-29 \
//	                 --trace-id <traceId> --user admin --password <pw>
package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
)

// osSearchResponse mirrors the OpenSearch _search JSON envelope we care about.
// Attributes are stored as arbitrary key→value (integers arrive as float64).
type osSearchResponse struct {
	Hits struct {
		Total struct {
			Value int `json:"value"`
		} `json:"total"`
		Hits []struct {
			Index  string `json:"_index"`
			Source struct {
				Attributes map[string]any `json:"attributes"`
				SpanID     string         `json:"spanId"`
				TraceID    string         `json:"traceId"`
			} `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

// tokenFields holds the four cache-relevant token counts for a span.
type tokenFields struct {
	SpanID        string
	Input         int64
	Output        int64
	CacheRead     int64
	CacheCreation int64
}

func main() {
	filePath := flag.String("file", "", "path to OpenSearch _search JSON fixture (offline mode)")
	osURL := flag.String("url", "", "OpenSearch base URL, e.g. https://localhost:9200 (live mode)")
	index := flag.String("index", "", "OpenSearch traces index name, e.g. otel-traces-2026-06-29 (live mode)")
	traceID := flag.String("trace-id", "", "traceId to search (live mode)")
	user := flag.String("user", "admin", "OpenSearch basic-auth user (live mode)")
	password := flag.String("password", "", "OpenSearch basic-auth password (live mode)")
	flag.Parse()

	if *filePath == "" && (*osURL == "" || *index == "" || *traceID == "") {
		fmt.Fprintln(os.Stderr, "usage (offline):  opensearch-probe --file <fixture.json>")
		fmt.Fprintln(os.Stderr, "usage (live):     opensearch-probe --url <url> --index <index> --trace-id <traceId> [--user admin --password <pw>]")
		os.Exit(1)
	}

	var raw []byte
	var err error

	if *filePath != "" {
		raw, err = os.ReadFile(*filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading fixture %s: %v\n", *filePath, err)
			os.Exit(1)
		}
	} else {
		raw, err = queryOpenSearch(*osURL, *index, *traceID, *user, *password)
		if err != nil {
			fmt.Fprintf(os.Stderr, "OpenSearch query failed — url=%s index=%s traceId=%s error=%v\n",
				*osURL, *index, *traceID, err)
			os.Exit(1)
		}
	}

	var resp osSearchResponse
	if err = json.Unmarshal(raw, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse OpenSearch response: %v\n", err)
		os.Exit(1)
	}

	tid := *traceID
	if tid == "" && len(resp.Hits.Hits) > 0 {
		tid = resp.Hits.Hits[0].Source.TraceID
	}

	fmt.Printf("opensearch-probe  traceId=%s  url=%s  index=%s\n", tid, *osURL, *index)
	fmt.Printf("  total span docs in response: %d\n", resp.Hits.Total.Value)

	// Find the first LLM chat span (gen_ai.operation.name == "chat") that has
	// cache_creation_input_tokens > 0.  If none exists, widen to any span.
	found := findCacheWriteSpan(resp)
	if found == nil {
		fmt.Printf("\nVERDICT: INCONCLUSIVE\n")
		fmt.Printf("  reason: gen_ai.usage.cache_creation_input_tokens absent or zero in all %d span(s)\n",
			len(resp.Hits.Hits))
		fmt.Printf("  debug:  url=%s  index=%s  traceId=%s\n", *osURL, *index, tid)
		os.Exit(2) // distinct exit code so callers can distinguish INCONCLUSIVE from FAIL
	}

	fmt.Printf("  span with cache_creation (spanId=%s):\n", found.SpanID)
	fmt.Printf("    input_tokens:                 %d\n", found.Input)
	fmt.Printf("    output_tokens:                %d\n", found.Output)
	fmt.Printf("    cache_read_input_tokens:      %d\n", found.CacheRead)
	fmt.Printf("    cache_creation_input_tokens:  %d\n", found.CacheCreation)
	fmt.Printf("\nVERDICT: GATE OK — cache_creation_input_tokens=%d (>0); proceed to reconciler\n",
		found.CacheCreation)
	os.Exit(0)
}

// findCacheWriteSpan returns the first span in the search results that
// carries gen_ai.usage.cache_creation_input_tokens > 0, preferring LLM
// chat spans (gen_ai.operation.name == "chat").
func findCacheWriteSpan(resp osSearchResponse) *tokenFields {
	var fallback *tokenFields // any span with cache_creation > 0

	for _, hit := range resp.Hits.Hits {
		attrs := hit.Source.Attributes
		cc := getIntAttr(attrs, "gen_ai.usage.cache_creation_input_tokens")
		if cc <= 0 {
			continue
		}
		tf := &tokenFields{
			SpanID:        hit.Source.SpanID,
			Input:         getIntAttr(attrs, "gen_ai.usage.input_tokens"),
			Output:        getIntAttr(attrs, "gen_ai.usage.output_tokens"),
			CacheRead:     getIntAttr(attrs, "gen_ai.usage.cache_read_input_tokens"),
			CacheCreation: cc,
		}
		// Prefer spans where gen_ai.operation.name is "chat" (LLM spans).
		if opName, _ := attrs["gen_ai.operation.name"].(string); opName == "chat" {
			return tf
		}
		if fallback == nil {
			fallback = tf
		}
	}
	return fallback
}

// queryOpenSearch issues GET <baseURL>/<index>/_search?q=traceId:<id>
// with basic auth and TLS verification disabled (dev-only self-signed cert).
func queryOpenSearch(baseURL, index, traceID, user, password string) ([]byte, error) {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // dev-only self-signed cert
	}
	client := &http.Client{Transport: tr}

	url := fmt.Sprintf("%s/%s/_search?q=traceId:%s", baseURL, index, traceID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if user != "" && password != "" {
		req.SetBasicAuth(user, password)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// getIntAttr reads a numeric attribute from an OpenSearch attributes map.
// OpenSearch documents JSON-decode numbers as float64 via interface{}.
func getIntAttr(attrs map[string]any, key string) int64 {
	v, ok := attrs[key]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	}
	return 0
}
