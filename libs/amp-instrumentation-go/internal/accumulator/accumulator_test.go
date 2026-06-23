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

package accumulator_test

import (
	"context"
	"sync"
	"testing"

	"github.com/wso2/agent-manager/libs/amp-instrumentation-go/internal/accumulator"
)

// TestAccumulator_ZeroValue asserts that a fresh Accumulator returns all zeros.
func TestAccumulator_ZeroValue(t *testing.T) {
	acc := &accumulator.Accumulator{}
	got := acc.Load()

	if got.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", got.InputTokens)
	}
	if got.OutputTokens != 0 {
		t.Errorf("OutputTokens = %d, want 0", got.OutputTokens)
	}
	if got.CacheReadInputTokens != 0 {
		t.Errorf("CacheReadInputTokens = %d, want 0", got.CacheReadInputTokens)
	}
	if got.CacheCreationInputTokens != 0 {
		t.Errorf("CacheCreationInputTokens = %d, want 0", got.CacheCreationInputTokens)
	}
}

// TestAccumulator_SingleAdd asserts that a single Add call is correctly reflected
// in Load.
func TestAccumulator_SingleAdd(t *testing.T) {
	acc := &accumulator.Accumulator{}
	acc.Add(10, 5, 3, 2)

	got := acc.Load()
	if got.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", got.InputTokens)
	}
	if got.OutputTokens != 5 {
		t.Errorf("OutputTokens = %d, want 5", got.OutputTokens)
	}
	if got.CacheReadInputTokens != 3 {
		t.Errorf("CacheReadInputTokens = %d, want 3", got.CacheReadInputTokens)
	}
	if got.CacheCreationInputTokens != 2 {
		t.Errorf("CacheCreationInputTokens = %d, want 2", got.CacheCreationInputTokens)
	}
}

// TestAccumulator_MultipleAdds asserts that multiple sequential Add calls
// accumulate correctly across all four fields.
func TestAccumulator_MultipleAdds(t *testing.T) {
	acc := &accumulator.Accumulator{}
	acc.Add(100, 50, 30, 20)
	acc.Add(200, 100, 60, 40)
	acc.Add(50, 25, 15, 10)

	got := acc.Load()
	if got.InputTokens != 350 {
		t.Errorf("InputTokens = %d, want 350", got.InputTokens)
	}
	if got.OutputTokens != 175 {
		t.Errorf("OutputTokens = %d, want 175", got.OutputTokens)
	}
	if got.CacheReadInputTokens != 105 {
		t.Errorf("CacheReadInputTokens = %d, want 105", got.CacheReadInputTokens)
	}
	if got.CacheCreationInputTokens != 70 {
		t.Errorf("CacheCreationInputTokens = %d, want 70", got.CacheCreationInputTokens)
	}
}

// TestAccumulator_ZeroFieldsIgnored asserts that zero fields in an Add call do
// not corrupt the running total.
func TestAccumulator_ZeroFieldsIgnored(t *testing.T) {
	acc := &accumulator.Accumulator{}
	acc.Add(10, 5, 0, 0)
	acc.Add(0, 0, 4, 2)

	got := acc.Load()
	if got.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", got.InputTokens)
	}
	if got.OutputTokens != 5 {
		t.Errorf("OutputTokens = %d, want 5", got.OutputTokens)
	}
	if got.CacheReadInputTokens != 4 {
		t.Errorf("CacheReadInputTokens = %d, want 4", got.CacheReadInputTokens)
	}
	if got.CacheCreationInputTokens != 2 {
		t.Errorf("CacheCreationInputTokens = %d, want 2", got.CacheCreationInputTokens)
	}
}

// TestAccumulator_ConcurrentAdd fans out 100 goroutines each calling Add once,
// then asserts the final totals equal the sum of all contributions.
//
// Run with: go test -race ./internal/accumulator/...
func TestAccumulator_ConcurrentAdd(t *testing.T) {
	const goroutines = 100

	acc := &accumulator.Accumulator{}
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			// Each goroutine adds (10, 5, 3, 2) — totals should be ×goroutines.
			acc.Add(10, 5, 3, 2)
		}()
	}
	wg.Wait()

	got := acc.Load()
	if got.InputTokens != int64(goroutines*10) {
		t.Errorf("InputTokens = %d, want %d", got.InputTokens, goroutines*10)
	}
	if got.OutputTokens != int64(goroutines*5) {
		t.Errorf("OutputTokens = %d, want %d", got.OutputTokens, goroutines*5)
	}
	if got.CacheReadInputTokens != int64(goroutines*3) {
		t.Errorf("CacheReadInputTokens = %d, want %d", got.CacheReadInputTokens, goroutines*3)
	}
	if got.CacheCreationInputTokens != int64(goroutines*2) {
		t.Errorf("CacheCreationInputTokens = %d, want %d", got.CacheCreationInputTokens, goroutines*2)
	}
}

// TestAccumulator_ContextRoundtrip asserts that NewContext + FromContext
// retrieves the same accumulator pointer.
func TestAccumulator_ContextRoundtrip(t *testing.T) {
	acc := &accumulator.Accumulator{}
	ctx := accumulator.NewContext(context.Background(), acc)

	got := accumulator.FromContext(ctx)
	if got != acc {
		t.Errorf("FromContext returned %p, want %p", got, acc)
	}
}

// TestAccumulator_FromContext_NilWhenAbsent asserts that FromContext returns nil
// when no accumulator has been installed — so leaf spans used outside an agent
// span remain safe.
func TestAccumulator_FromContext_NilWhenAbsent(t *testing.T) {
	got := accumulator.FromContext(context.Background())
	if got != nil {
		t.Errorf("expected nil from bare context, got %p", got)
	}
}

// TestAccumulator_IsolatedInstances asserts that two independent accumulator
// instances installed in sibling contexts do not share state, supporting nested
// / multi-agent trace patterns.
func TestAccumulator_IsolatedInstances(t *testing.T) {
	acc1 := &accumulator.Accumulator{}
	acc2 := &accumulator.Accumulator{}

	ctx1 := accumulator.NewContext(context.Background(), acc1)
	ctx2 := accumulator.NewContext(context.Background(), acc2)

	accumulator.FromContext(ctx1).Add(100, 50, 0, 0)
	accumulator.FromContext(ctx2).Add(200, 100, 0, 0)

	got1 := acc1.Load()
	got2 := acc2.Load()

	if got1.InputTokens != 100 {
		t.Errorf("acc1 InputTokens = %d, want 100", got1.InputTokens)
	}
	if got2.InputTokens != 200 {
		t.Errorf("acc2 InputTokens = %d, want 200", got2.InputTokens)
	}
}
