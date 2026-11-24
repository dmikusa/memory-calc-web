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
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/url"
)

//go:embed static
var staticFS embed.FS

// NewHandler returns the HTTP handler serving both the API and the embedded UI.
func NewHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}

	files := http.FileServer(http.FS(sub))

	mux := http.NewServeMux()
	mux.HandleFunc("/api/calculate", calculateHandler)
	mux.HandleFunc("/api/count-classes", countClassesHandler)
	mux.HandleFunc("/api/pick-path", pickPathHandler)
	mux.Handle("/", noCache(files))

	return mux
}

// sameOrigin rejects cross-site POSTs that could otherwise be used to trigger
// native dialogs or probe local paths from another web page.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	return u.Host == r.Host
}

func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The response is already committed; there is nowhere useful to report this.
		return
	}
}
