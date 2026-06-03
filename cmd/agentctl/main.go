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
	case "test-install-vscode":
		runTestInstallVSCode()
	case "test-download-codex":
		runTestDownloadCodex()
	case "test-configure":
		runTestConfigure()
	case "test-open-folder":
		runTestOpenFolder(os.Args[2:])
	case "test-mark-complete":
		runTestMarkComplete()
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
  agentctl logs                   print last 200 lines of launcher log

P13.4 verification subcommands (skip the OAuth steps, exercise everything else):
  agentctl test-install-vscode   download + silently install VS Code 1.96.0
  agentctl test-download-codex   download codex.exe to %LOCALAPPDATA%\agentserver-vscode\bin\
  agentctl test-configure        write settings.json + config.toml + setx + install extensions
                                 (assumes VS Code already detected; uses dummy API key)
  agentctl test-open-folder <path>
                                 launch VS Code with our user-data-dir + that folder
  agentctl test-mark-complete    write onboarding.status = complete so launcher takes the
                                 "configured" branch on next double-click`)
}
