package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"bankmonitoring/internal/config"
)

// healthcheck lets the container probe itself; the distroless image has no
// curl or wget, so the binary does it.
func healthcheck() int {
	addr := config.HTTPAddr(os.Getenv)
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "unexpected status", resp.StatusCode)
		return 1
	}
	return 0
}
