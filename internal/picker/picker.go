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

// Package picker opens a native file or folder chooser on the machine running
// the server. Browsers cannot expose absolute paths from an <input type=file>,
// so the server asks the host OS instead.
package picker

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	Folder = "folder"
	File   = "file"
)

// ErrUnsupported is returned when no native dialog helper is available.
var ErrUnsupported = errors.New("native file dialog is not available on this platform; type a path instead")

// Pick opens a native chooser and returns the selected absolute path. An empty
// path with a nil error means the user canceled.
func Pick(kind string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return pickDarwin(kind)
	case "linux":
		return pickLinux(kind)
	case "windows":
		return pickWindows(kind)
	default:
		return "", ErrUnsupported
	}
}

func pickDarwin(kind string) (string, error) {
	var script string

	switch kind {
	case Folder:
		script = `POSIX path of (choose folder with prompt "Select the application folder")`
	case File:
		script = `POSIX path of (choose file with prompt "Select a JAR file" of type {"jar"})`
	default:
		return "", fmt.Errorf("unknown picker kind %q", kind)
	}

	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		if appleScriptCanceled(err) {
			return "", nil
		}
		return "", fmt.Errorf("unable to open the native file dialog: %w", err)
	}

	return clean(out), nil
}

func pickLinux(kind string) (string, error) {
	if zenity, err := exec.LookPath("zenity"); err == nil {
		args := []string{"--file-selection", "--title=Select the application " + kind}

		switch kind {
		case Folder:
			args = append(args, "--directory")
		case File:
			args = append(args, "--file-filter=JAR files | *.jar")
		default:
			return "", fmt.Errorf("unknown picker kind %q", kind)
		}

		out, err := exec.Command(zenity, args...).Output()
		if err != nil {
			if exitCode(err) == 1 {
				return "", nil
			}
			return "", fmt.Errorf("zenity failed: %w", err)
		}
		return clean(out), nil
	}

	if kdialog, err := exec.LookPath("kdialog"); err == nil {
		var args []string

		switch kind {
		case Folder:
			args = []string{"--getexistingdirectory", "."}
		case File:
			args = []string{"--getopenfilename", ".", "*.jar"}
		default:
			return "", fmt.Errorf("unknown picker kind %q", kind)
		}

		out, err := exec.Command(kdialog, args...).Output()
		if err != nil {
			if exitCode(err) == 1 {
				return "", nil
			}
			return "", fmt.Errorf("kdialog failed: %w", err)
		}
		return clean(out), nil
	}

	return "", ErrUnsupported
}

func pickWindows(kind string) (string, error) {
	var script string

	switch kind {
	case Folder:
		script = `Add-Type -AssemblyName System.Windows.Forms
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = "Select the application folder"
if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $d.SelectedPath }`
	case File:
		script = `Add-Type -AssemblyName System.Windows.Forms
$d = New-Object System.Windows.Forms.OpenFileDialog
$d.Filter = "JAR files (*.jar)|*.jar|All files (*.*)|*.*"
$d.Title = "Select a JAR file"
if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $d.FileName }`
	default:
		return "", fmt.Errorf("unknown picker kind %q", kind)
	}

	out, err := exec.Command("powershell", "-NoProfile", "-STA", "-Command", script).Output()
	if err != nil {
		return "", fmt.Errorf("unable to open the native file dialog: %w", err)
	}

	return clean(out), nil
}

func clean(out []byte) string {
	path := strings.TrimSpace(string(out))
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func appleScriptCanceled(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}

	stderr := strings.ToLower(string(exitErr.Stderr))
	return strings.Contains(stderr, "-128") ||
		strings.Contains(stderr, "user canceled") ||
		strings.Contains(stderr, "user cancelled")
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return -1
	}
	return exitErr.ExitCode()
}
