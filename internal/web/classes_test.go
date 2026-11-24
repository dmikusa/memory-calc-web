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
	"archive/zip"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestCountClassesDirectory(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, filepath.Join(dir, "alpha.class"))
	writeTestFile(t, filepath.Join(dir, "bravo", "charlie.class"))
	writeTestFile(t, filepath.Join(dir, "readme.txt"))

	resp, status := countClasses(dir)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d (error: %s)", status, http.StatusOK, resp.Error)
	}
	if !resp.OK {
		t.Fatal("response not marked OK")
	}
	if got, want := resp.Count, 2; got != want {
		t.Errorf("count = %d, want %d", got, want)
	}
	if got, want := resp.Kind, "directory"; got != want {
		t.Errorf("kind = %q, want %q", got, want)
	}
}

func TestCountClassesJar(t *testing.T) {
	jar := filepath.Join(t.TempDir(), "app.jar")

	f, err := os.Create(jar)
	if err != nil {
		t.Fatal(err)
	}

	zw := zip.NewWriter(f)
	for _, name := range []string{"a/B.class", "C.class", "notes.txt"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	resp, status := countClasses(jar)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d (error: %s)", status, http.StatusOK, resp.Error)
	}
	if got, want := resp.Count, 2; got != want {
		t.Errorf("count = %d, want %d", got, want)
	}
	if got, want := resp.Kind, "file"; got != want {
		t.Errorf("kind = %q, want %q", got, want)
	}
}

func TestCountClassesErrors(t *testing.T) {
	t.Run("empty path", func(t *testing.T) {
		resp, status := countClasses("  ")
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
		}
		if resp.Error == "" {
			t.Error("expected an error message")
		}
	})

	t.Run("missing path", func(t *testing.T) {
		resp, status := countClasses(filepath.Join(t.TempDir(), "nope"))
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
		}
		if resp.Error == "" {
			t.Error("expected an error message")
		}
	})
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
}
