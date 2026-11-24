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
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/paketo-buildpacks/jvm-vendors/calc"
)

const (
	// Mirrors helper.ClassLoadFactor in jvm-vendors.
	classLoadFactor = 0.35

	// Mirrors helper.MaxJVMSize in jvm-vendors.
	maxJVMSize = 64 * calc.Tebi

	compactObjectHeadersFlag = "-XX:+UseCompactObjectHeaders"
)

var regionDescriptions = map[string]string{
	"heap": "Stores the objects and arrays your application allocates. It is the largest and most variable part of the budget.\n\n" +
		"The calculator sets -Xmx to whatever is left after every non-heap region is reserved. Setting -Xmx in JAVA_TOOL_OPTIONS overrides this and can leave memory unallocated.",

	"metaspace": "Native memory holding class metadata: loaded class definitions, method metadata, constant pools, and annotations. It is separate from the Java heap.\n\n" +
		"The calculator estimates it as 14,000,000 bytes plus 5,800 bytes per loaded class, unless you set -XX:MaxMetaspaceSize.",

	"code-cache": "Native memory where the JIT stores compiled machine code for hot methods, along with JVM-generated stubs and adapters.\n\n" +
		"The default reservation is 240M. The low-memory profile scales it down, which reduces JIT capacity and can hurt performance under load.",

	"stack": "Each Java thread has its own stack for frames, local variables, and partial results. Total stack memory is the per-thread -Xss value multiplied by the thread count.\n\n" +
		"At the defaults of 1M and 250 threads, stacks alone reserve 250M.",

	"direct": "Native memory used by direct ByteBuffers and some NIO and framework internals. It bypasses the Java heap but still counts against the container limit.\n\n" +
		"The calculator reserves -XX:MaxDirectMemorySize, 10M by default.",

	"headroom": "A deliberate reserve for memory the calculator cannot assign to a specific JVM region, such as JVM internals, allocator overhead, and native libraries.\n\n" +
		"It is a percentage of the total container limit and is 0 by default.",
}

type request struct {
	MemoryLimit      string      `json:"memoryLimit"`
	HeadroomPercent  int         `json:"headroomPercent"`
	ThreadCount      *int        `json:"threadCount"`
	LoadedClassCount *int        `json:"loadedClassCount"`
	ClassCounts      classCounts `json:"classCounts"`
	LowProfile       bool        `json:"lowProfile"`
	JavaToolOptions  string      `json:"javaToolOptions"`
	JVMVersion       string      `json:"jvmVersion"`
}

type classCounts struct {
	JVM        int    `json:"jvm"`
	App        int    `json:"app"`
	Agent      int    `json:"agent"`
	Adjustment string `json:"adjustment"`
}

type calculationResponse struct {
	OK      bool           `json:"ok"`
	Error   string         `json:"error,omitempty"`
	Inputs  resolvedInputs `json:"inputs"`
	Regions []region       `json:"regions"`
	Totals  totals         `json:"totals"`
	Options options        `json:"options"`
	Notes   []note         `json:"notes"`
}

type resolvedInputs struct {
	MemoryLimit        string  `json:"memoryLimit"`
	TotalMemoryBytes   int64   `json:"totalMemoryBytes"`
	HeadroomPercent    int     `json:"headroomPercent"`
	ThreadCount        int     `json:"threadCount"`
	ThreadCountSource  string  `json:"threadCountSource"`
	LoadedClassCount   int     `json:"loadedClassCount"`
	LoadedClassSource  string  `json:"loadedClassSource"`
	LoadedClassFormula string  `json:"loadedClassFormula,omitempty"`
	LowProfile         bool    `json:"lowProfile"`
	ScalingFactor      float64 `json:"scalingFactor"`
}

type region struct {
	Key         string  `json:"key"`
	Label       string  `json:"label"`
	Bytes       int64   `json:"bytes"`
	Size        string  `json:"size"`
	Percent     float64 `json:"percent"`
	Flag        string  `json:"flag"`
	Provenance  string  `json:"provenance"`
	Detail      string  `json:"detail,omitempty"`
	Description string  `json:"description,omitempty"`
}

type totals struct {
	TotalBytes       int64   `json:"totalBytes"`
	Total            string  `json:"total"`
	HeapBytes        int64   `json:"heapBytes"`
	Heap             string  `json:"heap"`
	NonHeapBytes     int64   `json:"nonHeapBytes"`
	NonHeap          string  `json:"nonHeap"`
	UnallocatedBytes int64   `json:"unallocatedBytes"`
	Unallocated      string  `json:"unallocated"`
	HeapPercent      float64 `json:"heapPercent"`
	NonHeapPercent   float64 `json:"nonHeapPercent"`
}

type options struct {
	Calculated []string `json:"calculated"`
	Extras     []string `json:"extras"`
	Final      string   `json:"final"`
}

type note struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

func calculateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, calculationResponse{
			Error: "method not allowed; POST a JSON body to /api/calculate",
			Notes: []note{},
		})
		return
	}
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, calculationResponse{
			Error: "cross-origin requests are not allowed",
			Notes: []note{},
		})
		return
	}

	defer func() { _ = r.Body.Close() }()

	var req request
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, calculationResponse{
			Error: "invalid request body: " + err.Error(),
			Notes: []note{},
		})
		return
	}

	resp, status := calculate(req)
	writeJSON(w, status, resp)
}

func calculate(req request) (calculationResponse, int) {
	notes := make([]note, 0, 4)
	resp := calculationResponse{Notes: notes}

	if req.HeadroomPercent < 0 {
		resp.Error = "head room cannot be negative"
		return resp, http.StatusBadRequest
	}

	total, memoryNotes, err := resolveMemory(req.MemoryLimit)
	if err != nil {
		resp.Error = err.Error()
		return resp, http.StatusBadRequest
	}
	notes = append(notes, memoryNotes...)

	if !req.LowProfile && total < calc.LowProfileThreshold {
		notes = append(notes, note{
			Kind: "warn",
			Text: fmt.Sprintf("Container memory is below %s. For best results, enable the Low memory profile toggle: it scales stack, code cache, and thread count down for small containers. Production only applies it when BPL_LOW_MEMORY_PROFILE_DISABLED=false.", formatSize(calc.LowProfileThreshold)),
		})
	}

	loadedClassCount := 0
	loadedClassSource := "explicit"
	loadedClassFormula := ""

	if req.LoadedClassCount != nil {
		if *req.LoadedClassCount < 0 {
			resp.Error = "loaded class count cannot be negative"
			resp.Notes = notes
			return resp, http.StatusBadRequest
		}
		loadedClassCount = *req.LoadedClassCount
	} else {
		derived, formula, err := deriveLoadedClassCount(req.ClassCounts)
		if err != nil {
			resp.Error = err.Error()
			resp.Notes = notes
			return resp, http.StatusBadRequest
		}
		loadedClassCount = derived
		loadedClassSource = "derived"
		loadedClassFormula = formula
	}

	threadCount := calc.DefaultThreadCountValue
	threadCountSource := "default"
	if req.ThreadCount != nil {
		if *req.ThreadCount < 0 {
			resp.Error = "thread count cannot be negative"
			resp.Notes = notes
			return resp, http.StatusBadRequest
		}
		threadCount = calc.ThreadCount{Value: *req.ThreadCount, Provenance: calc.UserConfigured}
		threadCountSource = "explicit"
		if *req.ThreadCount == 0 {
			notes = append(notes, note{Kind: "warn", Text: "Thread count is 0, so no stack memory is reserved."})
		}
	}

	c := calc.Calculator{
		HeadRoom:         req.HeadroomPercent,
		LoadedClassCount: loadedClassCount,
		LowProfile:       req.LowProfile,
		ThreadCount:      threadCount,
		TotalMemory:      calc.Size{Value: total},
	}

	out, err := c.Calculate(req.JavaToolOptions)
	if err != nil {
		resp.Error = err.Error()
		resp.Inputs = resolvedInputs{
			MemoryLimit:        formatSize(total),
			TotalMemoryBytes:   total,
			HeadroomPercent:    req.HeadroomPercent,
			ThreadCount:        threadCount.Value,
			ThreadCountSource:  errorThreadSource(threadCountSource),
			LoadedClassCount:   loadedClassCount,
			LoadedClassSource:  loadedClassSource,
			LoadedClassFormula: loadedClassFormula,
			LowProfile:         req.LowProfile,
			ScalingFactor:      0,
		}
		resp.Notes = notes
		return resp, http.StatusUnprocessableEntity
	}

	m := out.Memory
	threads := out.ThreadCount.Value
	if threadCountSource != "explicit" && m.ScalingFactor != 1 {
		threadCountSource = "scaled"
	}

	if req.LowProfile {
		notes = append(notes, note{
			Kind: "info",
			Text: fmt.Sprintf("Low-memory profile applied: stack, code cache, and thread count scaled to %.0f%% of the 1G baseline.", m.ScalingFactor*100),
		})
	}

	if m.Heap.Provenance == calc.UserConfigured {
		notes = append(notes, note{
			Kind: "warn",
			Text: "-Xmx is set directly in JAVA_TOOL_OPTIONS. The buildpack warns about this because it can cause memory-calculator failures or leave unused memory in the container.",
		})
	}

	if m.ReservedCodeCache.Provenance == calc.Calculated && m.ReservedCodeCache.Value < 240*calc.Mebi {
		notes = append(notes, note{
			Kind: "warn",
			Text: fmt.Sprintf("Code cache size is %s, below the 240M default. JIT compilation performance may be reduced under load.", formatSize(m.ReservedCodeCache.Value)),
		})
	}

	heapBytes := m.Heap.Value
	stackBytes := m.Stack.Value * int64(threads)
	nonHeapBytes := m.DirectMemory.Value + m.Metaspace.Value + m.ReservedCodeCache.Value + stackBytes + m.HeadRoom.Value
	accounted := heapBytes + nonHeapBytes
	unallocated := total - accounted

	if unallocated > 0 {
		notes = append(notes, note{
			Kind: "info",
			Text: fmt.Sprintf("%s is not allocated because -Xmx was configured explicitly.", formatSize(unallocated)),
		})
	}

	if len(notes) == 0 {
		notes = append(notes, note{Kind: "info", Text: "All memory accounted for."})
	}

	scaling := m.ScalingFactor
	regions := []region{
		{
			Key:         "heap",
			Label:       "Heap",
			Bytes:       heapBytes,
			Size:        formatSize(heapBytes),
			Percent:     percentOf(heapBytes, total),
			Flag:        m.Heap.String(),
			Provenance:  provenanceName(m.Heap.Provenance),
			Detail:      heapDetail(m.Heap.Provenance),
			Description: regionDescriptions["heap"],
		},
		{
			Key:         "metaspace",
			Label:       "Metaspace",
			Bytes:       m.Metaspace.Value,
			Size:        formatSize(m.Metaspace.Value),
			Percent:     percentOf(m.Metaspace.Value, total),
			Flag:        m.Metaspace.String(),
			Provenance:  provenanceName(m.Metaspace.Provenance),
			Detail:      metaspaceDetail(m.Metaspace.Provenance, loadedClassCount),
			Description: regionDescriptions["metaspace"],
		},
		{
			Key:         "code-cache",
			Label:       "Code cache",
			Bytes:       m.ReservedCodeCache.Value,
			Size:        formatSize(m.ReservedCodeCache.Value),
			Percent:     percentOf(m.ReservedCodeCache.Value, total),
			Flag:        m.ReservedCodeCache.String(),
			Provenance:  provenanceName(m.ReservedCodeCache.Provenance),
			Detail:      codeCacheDetail(m.ReservedCodeCache.Provenance, scaling),
			Description: regionDescriptions["code-cache"],
		},
		{
			Key:         "stack",
			Label:       "Stack",
			Bytes:       stackBytes,
			Size:        formatSize(stackBytes),
			Percent:     percentOf(stackBytes, total),
			Flag:        fmt.Sprintf("%s × %d threads", m.Stack, threads),
			Provenance:  provenanceName(m.Stack.Provenance),
			Detail:      stackDetail(m.Stack.Provenance, scaling, threads),
			Description: regionDescriptions["stack"],
		},
		{
			Key:         "direct",
			Label:       "Direct memory",
			Bytes:       m.DirectMemory.Value,
			Size:        formatSize(m.DirectMemory.Value),
			Percent:     percentOf(m.DirectMemory.Value, total),
			Flag:        m.DirectMemory.String(),
			Provenance:  provenanceName(m.DirectMemory.Provenance),
			Detail:      directMemoryDetail(m.DirectMemory.Provenance),
			Description: regionDescriptions["direct"],
		},
		{
			Key:         "headroom",
			Label:       "Head room",
			Bytes:       m.HeadRoom.Value,
			Size:        formatSize(m.HeadRoom.Value),
			Percent:     percentOf(m.HeadRoom.Value, total),
			Flag:        fmt.Sprintf("%d%% of total", req.HeadroomPercent),
			Provenance:  provenanceName(m.HeadRoom.Provenance),
			Detail:      fmt.Sprintf("%d%% of %s", req.HeadroomPercent, formatSize(total)),
			Description: regionDescriptions["headroom"],
		},
	}

	calculated, extras := buildOptions(m, req)
	final := buildFinalOptions(req.JavaToolOptions, calculated, extras)

	resp.OK = true
	resp.Inputs = resolvedInputs{
		MemoryLimit:        formatSize(total),
		TotalMemoryBytes:   total,
		HeadroomPercent:    req.HeadroomPercent,
		ThreadCount:        threads,
		ThreadCountSource:  threadCountSource,
		LoadedClassCount:   loadedClassCount,
		LoadedClassSource:  loadedClassSource,
		LoadedClassFormula: loadedClassFormula,
		LowProfile:         req.LowProfile,
		ScalingFactor:      scaling,
	}
	resp.Regions = regions
	resp.Totals = totals{
		TotalBytes:       total,
		Total:            formatSize(total),
		HeapBytes:        heapBytes,
		Heap:             formatSize(heapBytes),
		NonHeapBytes:     nonHeapBytes,
		NonHeap:          formatSize(nonHeapBytes),
		UnallocatedBytes: unallocated,
		Unallocated:      formatSize(unallocated),
		HeapPercent:      percentOf(heapBytes, total),
		NonHeapPercent:   percentOf(nonHeapBytes, total),
	}
	resp.Options = options{
		Calculated: calculated,
		Extras:     extras,
		Final:      final,
	}
	resp.Notes = notes

	return resp, http.StatusOK
}

func errorThreadSource(source string) string {
	// Low-profile scaling happens before the calculator's guard clauses, so the
	// pre-scaling thread count would be misleading when Calculate fails. The
	// error message itself reports the effective thread count.
	if source == "explicit" {
		return source
	}
	return ""
}

func resolveMemory(raw string) (int64, []note, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return calc.Gibi, []note{{Kind: "info", Text: "No memory limit supplied; using the buildpack default of 1G."}}, nil
	}

	size, err := calc.ParseSize(raw)
	if err != nil {
		return 0, nil, fmt.Errorf("invalid memory limit %q: use a whole number with an optional K, M, G, or T suffix (for example 512M or 2G)", raw)
	}

	if size.Value <= 0 {
		return 0, nil, fmt.Errorf("memory limit must be greater than zero")
	}

	if size.Value > maxJVMSize {
		return maxJVMSize, []note{{
			Kind: "warn",
			Text: fmt.Sprintf("Memory limit %s is above the buildpack maximum; using 64T.", formatSize(size.Value)),
		}}, nil
	}

	return size.Value, nil, nil
}

func deriveLoadedClassCount(cc classCounts) (int, string, error) {
	if cc.JVM < 0 || cc.App < 0 || cc.Agent < 0 {
		return 0, "", fmt.Errorf("class counts cannot be negative")
	}

	adjustmentFactor := 100
	staticAdjustment := 0

	if adj := strings.TrimSpace(cc.Adjustment); adj != "" {
		if strings.HasSuffix(adj, "%") {
			v, err := strconv.ParseUint(strings.TrimSuffix(adj, "%"), 10, 32)
			if err != nil {
				return 0, "", fmt.Errorf("invalid class adjustment %q: use a percentage like 110%% or a whole number", cc.Adjustment)
			}
			adjustmentFactor = int(v)
		} else {
			v, err := strconv.Atoi(adj)
			if err != nil {
				return 0, "", fmt.Errorf("invalid class adjustment %q: use a percentage like 110%% or a whole number", cc.Adjustment)
			}
			staticAdjustment = v
		}
	}

	total := float64(cc.JVM+cc.App+cc.Agent+staticAdjustment) * (float64(adjustmentFactor) / 100.0)
	loaded := int(total * classLoadFactor)
	if loaded < 0 {
		loaded = 0
	}

	formula := fmt.Sprintf("%d%% × (%d JVM + %d app + %d agent + %d adjustment) × %.2f",
		adjustmentFactor, cc.JVM, cc.App, cc.Agent, staticAdjustment, classLoadFactor)

	return loaded, formula, nil
}

func buildOptions(m calc.MemoryRegions, req request) ([]string, []string) {
	calculated := make([]string, 0, 5)

	if m.DirectMemory.Provenance != calc.UserConfigured {
		calculated = append(calculated, m.DirectMemory.String())
	}
	if m.Heap.Provenance != calc.UserConfigured {
		calculated = append(calculated, m.Heap.String())
	}
	if m.Metaspace.Provenance != calc.UserConfigured {
		calculated = append(calculated, m.Metaspace.String())
	}
	if m.ReservedCodeCache.Provenance != calc.UserConfigured {
		calculated = append(calculated, m.ReservedCodeCache.String())
	}
	if m.Stack.Provenance != calc.UserConfigured {
		calculated = append(calculated, m.Stack.String())
	}

	extras := make([]string, 0, 1)
	if req.LowProfile &&
		javaMajor(req.JVMVersion) >= 25 &&
		!strings.Contains(req.JavaToolOptions, compactObjectHeadersFlag) {
		extras = append(extras, compactObjectHeadersFlag)
	}

	return calculated, extras
}

func buildFinalOptions(raw string, calculated, extras []string) string {
	parts := make([]string, 0, 1+len(calculated)+len(extras))

	if s := strings.TrimSpace(raw); s != "" {
		parts = append(parts, s)
	}
	parts = append(parts, calculated...)
	parts = append(parts, extras...)

	return strings.Join(parts, " ")
}

func javaMajor(version string) int {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	if version == "" {
		return 0
	}

	parts := strings.SplitN(version, ".", 3)
	major, err := strconv.Atoi(leadingDigits(parts[0]))
	if err != nil {
		return 0
	}

	// Legacy versions are written as 1.8.0 rather than 8.0.0.
	if major == 1 && len(parts) > 1 {
		if v, err := strconv.Atoi(leadingDigits(parts[1])); err == nil {
			return v
		}
	}

	return major
}

func leadingDigits(s string) string {
	for i, r := range s {
		if r < '0' || r > '9' {
			return s[:i]
		}
	}
	return s
}

func formatSize(bytes int64) string {
	return calc.Size{Value: bytes}.String()
}

func percentOf(value, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return math.Round((float64(value)/float64(total))*100*100) / 100
}

func provenanceName(p calc.Provenance) string {
	switch p {
	case calc.Default:
		return "default"
	case calc.UserConfigured:
		return "user"
	case calc.Calculated:
		return "calculated"
	default:
		return "unknown"
	}
}

func heapDetail(p calc.Provenance) string {
	if p == calc.UserConfigured {
		return "set explicitly via -Xmx"
	}
	return "remainder of total memory after non-heap regions"
}

func metaspaceDetail(p calc.Provenance, loadedClasses int) string {
	if p == calc.UserConfigured {
		return "set explicitly via -XX:MaxMetaspaceSize"
	}
	return fmt.Sprintf("%d bytes overhead + %d bytes per class × %d classes", calc.ClassOverhead, calc.ClassSize, loadedClasses)
}

func codeCacheDetail(p calc.Provenance, scaling float64) string {
	if p == calc.UserConfigured {
		return "set explicitly via -XX:ReservedCodeCacheSize"
	}
	if scaling != 1 {
		return fmt.Sprintf("scaled to %.0f%% of the 240M default", scaling*100)
	}
	return "240M default"
}

func stackDetail(p calc.Provenance, scaling float64, threads int) string {
	if p == calc.UserConfigured {
		return fmt.Sprintf("set explicitly via -Xss, multiplied by %d threads", threads)
	}
	if scaling != 1 {
		return fmt.Sprintf("%.0f%% of the 1M default per thread, multiplied by %d threads", scaling*100, threads)
	}
	return fmt.Sprintf("1M default per thread, multiplied by %d threads", threads)
}

func directMemoryDetail(p calc.Provenance) string {
	if p == calc.UserConfigured {
		return "set explicitly via -XX:MaxDirectMemorySize"
	}
	return "10M default"
}
