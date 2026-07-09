package main

import "github.com/agentserver/agentserver-pkg/internal/codexdebug"

func runCodexDebugWrapper(args []string) int {
	return codexdebug.Run(args)
}

func codexDebugThreadPaths(stderr string) []string {
	return codexdebug.ThreadPaths(stderr)
}

func codexDebugSessionSummary(path string) string {
	return codexdebug.SessionSummary(path)
}
