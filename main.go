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

package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/dmikusa/memory-calc-web/internal/web"
)

func main() {
	var (
		listen = flag.String("listen", "127.0.0.1:8080", "address to listen on")
		noOpen = flag.Bool("no-open", false, "don't open a browser window")
	)
	flag.Parse()

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("unable to listen on %s: %s", *listen, err)
	}

	url := fmt.Sprintf("http://%s", ln.Addr().String())
	fmt.Printf("memory-calc-web listening on %s\n", url)

	if !*noOpen {
		go func() {
			// Give the server a moment to accept connections before the browser
			// navigates to it.
			time.Sleep(250 * time.Millisecond)
			if err := openBrowser(url); err != nil {
				fmt.Fprintf(os.Stderr, "unable to open browser: %s\n", err)
				fmt.Fprintf(os.Stderr, "open %s manually\n", url)
			}
		}()
	}

	srv := &http.Server{
		Handler:           web.NewHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %s", err)
	}
}

func openBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}

	return cmd.Start()
}
