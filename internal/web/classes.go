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
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/paketo-buildpacks/jvm-vendors/count"

	"github.com/dmikusa/memory-calc-web/internal/picker"
)

type countClassesRequest struct {
	Path string `json:"path"`
}

type countClassesResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Path  string `json:"path,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Count int    `json:"count"`
}

type pickPathRequest struct {
	Kind string `json:"kind"`
}

type pickPathResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Canceled bool   `json:"canceled,omitempty"`
	Path     string `json:"path,omitempty"`
}

func countClassesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, countClassesResponse{Error: "method not allowed"})
		return
	}
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, countClassesResponse{Error: "cross-origin requests are not allowed"})
		return
	}

	defer func() { _ = r.Body.Close() }()

	var req countClassesRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, countClassesResponse{Error: "invalid request body: " + err.Error()})
		return
	}

	resp, status := countClasses(req.Path)
	writeJSON(w, status, resp)
}

func countClasses(rawPath string) (countClassesResponse, int) {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return countClassesResponse{Error: "enter a folder or JAR path first"}, http.StatusBadRequest
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return countClassesResponse{Error: fmt.Sprintf("path %q does not exist", path)}, http.StatusBadRequest
		}
		return countClassesResponse{Error: fmt.Sprintf("unable to read %q: %s", path, err)}, http.StatusBadRequest
	}

	kind := "file"
	var n int

	if info.IsDir() {
		kind = "directory"
		n, err = count.Classes(path)
	} else {
		// count.Classes assumes an application directory; for a single JAR use
		// the same helper the buildpack uses for agent JARs.
		var skipped int
		n, skipped, err = count.JarClassesFrom(path)
		if err == nil && skipped > 0 {
			err = fmt.Errorf("path %q does not exist", path)
		}
	}

	if err != nil {
		return countClassesResponse{Error: fmt.Sprintf("unable to count classes in %q: %s", path, err)}, http.StatusUnprocessableEntity
	}

	return countClassesResponse{
		OK:    true,
		Path:  filepath.Clean(path),
		Kind:  kind,
		Count: n,
	}, http.StatusOK
}

func pickPathHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, pickPathResponse{Error: "method not allowed"})
		return
	}
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, pickPathResponse{Error: "cross-origin requests are not allowed"})
		return
	}

	defer func() { _ = r.Body.Close() }()

	var req pickPathRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, pickPathResponse{Error: "invalid request body: " + err.Error()})
		return
	}

	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind != picker.Folder && kind != picker.File {
		writeJSON(w, http.StatusBadRequest, pickPathResponse{Error: `kind must be "folder" or "file"`})
		return
	}

	path, err := picker.Pick(kind)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, pickPathResponse{Error: err.Error()})
		return
	}

	if path == "" {
		writeJSON(w, http.StatusOK, pickPathResponse{OK: true, Canceled: true})
		return
	}

	writeJSON(w, http.StatusOK, pickPathResponse{OK: true, Path: path})
}
