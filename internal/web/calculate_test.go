/*
 * Copyright 2026 the original author or authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      https://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/paketo-buildpacks/jvm-vendors/calc"
)

func intPtr(v int) *int {
	return &v
}

func TestCalculateDefaults(t *testing.T) {
	resp, status := calculate(request{
		MemoryLimit: "1G",
		ClassCounts: classCounts{JVM: 8000},
	})

	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d (error: %s)", status, http.StatusOK, resp.Error)
	}
	if !resp.OK {
		t.Fatal("response not marked OK")
	}

	if got, want := resp.Inputs.LoadedClassCount, 2800; got != want {
		t.Errorf("loaded class count = %d, want %d", got, want)
	}
	if got, want := resp.Inputs.ThreadCount, calc.DefaultThreadCount; got != want {
		t.Errorf("thread count = %d, want %d", got, want)
	}
	if resp.Inputs.ThreadCountSource != "default" {
		t.Errorf("thread count source = %q, want default", resp.Inputs.ThreadCountSource)
	}

	metaspace := calc.ClassOverhead + calc.ClassSize*2800
	wantHeap := int64(calc.Gibi) -
		(calc.DefaultDirectMemory.Value + metaspace + calc.DefaultReservedCodeCache.Value + calc.DefaultStack.Value*calc.DefaultThreadCount)

	if got := resp.Totals.HeapBytes; got != wantHeap {
		t.Errorf("heap = %d, want %d", got, wantHeap)
	}
	if got := resp.Totals.UnallocatedBytes; got != 0 {
		t.Errorf("unallocated = %d, want 0", got)
	}
	if !strings.Contains(resp.Options.Final, "-Xmx") {
		t.Errorf("generated options missing -Xmx: %q", resp.Options.Final)
	}

	for _, region := range resp.Regions {
		if region.Description == "" {
			t.Errorf("region %q has no description", region.Key)
		}
	}
}

func TestCalculateLowProfile(t *testing.T) {
	resp, status := calculate(request{
		MemoryLimit: "512M",
		LowProfile:  true,
		ClassCounts: classCounts{JVM: 8000},
	})

	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d (error: %s)", status, http.StatusOK, resp.Error)
	}

	if got, want := resp.Inputs.ScalingFactor, 0.5; got != want {
		t.Errorf("scaling factor = %v, want %v", got, want)
	}
	if got, want := resp.Inputs.ThreadCount, 125; got != want {
		t.Errorf("thread count = %d, want %d", got, want)
	}
	if got, want := resp.Inputs.ThreadCountSource, "scaled"; got != want {
		t.Errorf("thread count source = %q, want %q", got, want)
	}

	region := findRegion(t, resp, "stack")
	if got, want := region.Bytes, int64(512*calc.Kibi)*125; got != want {
		t.Errorf("stack bytes = %d, want %d", got, want)
	}

	cache := findRegion(t, resp, "code-cache")
	if got, want := cache.Bytes, int64(120*calc.Mebi); got != want {
		t.Errorf("code cache bytes = %d, want %d", got, want)
	}
}

func TestCalculateUserHeapLeavesMemoryUnallocated(t *testing.T) {
	resp, status := calculate(request{
		MemoryLimit:      "1G",
		LoadedClassCount: intPtr(500),
		JavaToolOptions:  "-Xmx256M",
	})

	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d (error: %s)", status, http.StatusOK, resp.Error)
	}

	heap := findRegion(t, resp, "heap")
	if heap.Provenance != "user" {
		t.Errorf("heap provenance = %q, want user", heap.Provenance)
	}
	if got, want := resp.Totals.HeapBytes, int64(256*calc.Mebi); got != want {
		t.Errorf("heap = %d, want %d", got, want)
	}
	if resp.Totals.UnallocatedBytes <= 0 {
		t.Errorf("unallocated = %d, want > 0", resp.Totals.UnallocatedBytes)
	}
	for _, option := range resp.Options.Calculated {
		if strings.HasPrefix(option, "-Xmx") {
			t.Errorf("generated options should not include a calculated -Xmx: %q", option)
		}
	}
	if !strings.HasPrefix(resp.Options.Final, "-Xmx256M") {
		t.Errorf("final options should preserve the user -Xmx: %q", resp.Options.Final)
	}
}

func TestCalculateErrors(t *testing.T) {
	t.Run("fixed regions too large", func(t *testing.T) {
		resp, status := calculate(request{MemoryLimit: "1K"})
		if status != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", status, http.StatusUnprocessableEntity)
		}
		if !strings.Contains(resp.Error, "fixed memory regions require") {
			t.Errorf("error = %q, want fixed-regions message", resp.Error)
		}
	})

	t.Run("invalid memory limit", func(t *testing.T) {
		resp, status := calculate(request{MemoryLimit: "lots"})
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
		}
		if resp.Error == "" {
			t.Error("expected an error message")
		}
	})

	t.Run("invalid class adjustment", func(t *testing.T) {
		resp, status := calculate(request{
			MemoryLimit: "1G",
			ClassCounts: classCounts{JVM: 1000, Adjustment: "wat"},
		})
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
		}
		if !strings.Contains(resp.Error, "invalid class adjustment") {
			t.Errorf("error = %q, want class adjustment message", resp.Error)
		}
	})
}

func TestCalculateClassAdjustment(t *testing.T) {
	resp, status := calculate(request{
		MemoryLimit: "1G",
		ClassCounts: classCounts{JVM: 1000, App: 500, Agent: 100, Adjustment: "50%"},
	})

	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d (error: %s)", status, http.StatusOK, resp.Error)
	}
	if got, want := resp.Inputs.LoadedClassCount, 280; got != want {
		t.Errorf("loaded class count = %d, want %d", got, want)
	}
}

func TestCalculateLowProfileRecommendation(t *testing.T) {
	hasRecommendation := func(resp calculationResponse) bool {
		for _, n := range resp.Notes {
			if n.Kind == "warn" && strings.Contains(n.Text, "For best results") {
				return true
			}
		}
		return false
	}

	t.Run("warns below 1G when low profile is off", func(t *testing.T) {
		resp, status := calculate(request{MemoryLimit: "768M"})
		if status != http.StatusOK {
			t.Fatalf("status = %d, want %d (error: %s)", status, http.StatusOK, resp.Error)
		}
		if !hasRecommendation(resp) {
			t.Errorf("notes = %v, want low profile recommendation", resp.Notes)
		}
	})

	t.Run("warns when a small container fails to calculate", func(t *testing.T) {
		resp, status := calculate(request{MemoryLimit: "512M"})
		if status != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", status, http.StatusUnprocessableEntity)
		}
		if !hasRecommendation(resp) {
			t.Errorf("notes = %v, want low profile recommendation", resp.Notes)
		}
	})

	t.Run("does not warn at 1G", func(t *testing.T) {
		resp, status := calculate(request{MemoryLimit: "1G"})
		if status != http.StatusOK {
			t.Fatalf("status = %d, want %d (error: %s)", status, http.StatusOK, resp.Error)
		}
		if hasRecommendation(resp) {
			t.Errorf("notes = %v, want no low profile recommendation", resp.Notes)
		}
	})

	t.Run("does not warn when low profile is on", func(t *testing.T) {
		resp, status := calculate(request{MemoryLimit: "768M", LowProfile: true})
		if status != http.StatusOK {
			t.Fatalf("status = %d, want %d (error: %s)", status, http.StatusOK, resp.Error)
		}
		if hasRecommendation(resp) {
			t.Errorf("notes = %v, want no low profile recommendation", resp.Notes)
		}
	})
}

func TestCalculateCompactObjectHeaders(t *testing.T) {
	t.Run("java 25", func(t *testing.T) {
		resp, status := calculate(request{
			MemoryLimit: "512M",
			LowProfile:  true,
			JVMVersion:  "25",
		})
		if status != http.StatusOK {
			t.Fatalf("status = %d, want %d (error: %s)", status, http.StatusOK, resp.Error)
		}
		if !contains(resp.Options.Extras, compactObjectHeadersFlag) {
			t.Errorf("extras = %v, want %s", resp.Options.Extras, compactObjectHeadersFlag)
		}
	})

	t.Run("java 21", func(t *testing.T) {
		resp, status := calculate(request{
			MemoryLimit: "512M",
			LowProfile:  true,
			JVMVersion:  "21",
		})
		if status != http.StatusOK {
			t.Fatalf("status = %d, want %d (error: %s)", status, http.StatusOK, resp.Error)
		}
		if contains(resp.Options.Extras, compactObjectHeadersFlag) {
			t.Errorf("extras = %v, want no compact object headers", resp.Options.Extras)
		}
	})
}

func findRegion(t *testing.T, resp calculationResponse, key string) region {
	t.Helper()
	for _, r := range resp.Regions {
		if r.Key == key {
			return r
		}
	}
	t.Fatalf("region %q not found", key)
	return region{}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
