package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "doctor":
		runDoctor()
	case "uninstall":
		runUninstall(os.Args[2:])
	case "reconfigure":
		runReconfigure()
	case "logs":
		runLogs()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `agentctl - maintenance CLI for agentserver-vscode

USAGE:
  agentctl doctor                 print install health
  agentctl reconfigure            relaunch the onboarding UI
  agentctl uninstall [--silent] [--vscode]
                                  remove shortcuts/registry/state; --vscode also removes VS Code
  agentctl logs                   print last 200 lines of launcher log`)
}
