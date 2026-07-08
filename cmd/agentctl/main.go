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
	case "install-codex":
		if err := runInstallCodex(os.Args[2:]); err != nil {
			die(err)
		}
	case "codex-debug-wrapper":
		os.Exit(runCodexDebugWrapper(os.Args[2:]))
	case "set-model":
		runSetModel(os.Args[2:])
	case "test-install-vscode":
		runTestInstallVSCode()
	case "test-install-codex-desktop":
		runTestInstallCodexDesktop()
	case "test-download-codex":
		runTestDownloadCodex()
	case "test-configure":
		runTestConfigure()
	case "test-configure-codex-desktop":
		runTestConfigureCodexDesktop()
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
	fmt.Fprintln(os.Stderr, `agentctl - maintenance CLI for 星池指挥官

USAGE:
  agentctl doctor                 print install health
  agentctl reconfigure            relaunch the onboarding UI
  agentctl uninstall [--silent] [--vscode]
                                  remove shortcuts/registry/state; --vscode also removes VS Code
  agentctl logs                   print last 200 lines of launcher log
  agentctl install-codex --manifest <path>
                                  download and install Codex runtime from npm mirrors
  agentctl codex-debug-wrapper --codex <path> [args...]
                                  run Codex and print safe resume diagnostics on failure
  agentctl set-model <name>      set the Codex model (gpt-5.5 / deepseek-v4-pro / glm-5.2)

P13.4 verification subcommands (skip the OAuth steps, exercise everything else):
  agentctl test-install-vscode   download + run the VS Code Microsoft Store bootstrapper
  agentctl test-install-codex-desktop
                                 install Codex Desktop with winget and persist state
  agentctl test-download-codex   download codex.exe to %LOCALAPPDATA%\agentserver-app\bin\
	  agentctl test-configure        write settings.json + config.toml + setx + install extensions
	                                 (assumes VS Code already detected; uses local proxy key)
	  agentctl test-configure-codex-desktop
	                                 write Codex config + local proxy key for Codex Desktop
  agentctl test-open-folder <path>
                                 launch VS Code with our user-data-dir + that folder
  agentctl test-mark-complete    write onboarding.status = complete so launcher takes the
                                 "configured" branch on next double-click`)
}
