package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/paths"
)

func runLogs() {
	p, _ := paths.Default()
	logPath := filepath.Join(p.InstallRoot, "launcher.log")
	b, err := os.ReadFile(logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "no log:", err)
		return
	}
	fmt.Print(string(b))
}
