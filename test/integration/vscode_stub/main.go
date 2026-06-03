// vscode_stub is a fake `code` CLI used in integration tests. It records
// every invocation (argv joined by tab) to $VSCODE_STUB_LOG, and outputs
// a fixed version when called with --version.
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	args := os.Args[1:]
	if logPath := os.Getenv("VSCODE_STUB_LOG"); logPath != "" {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintln(f, strings.Join(args, "\t"))
			f.Close()
		}
	}
	if len(args) > 0 && args[0] == "--version" {
		fmt.Println("1.96.0")
		fmt.Println("abcdef0123")
		fmt.Println("x64")
		return
	}
}
