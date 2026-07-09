package main

import (
	"os"

	"github.com/agentserver/agentserver-pkg/internal/codexdebug"
)

func main() {
	os.Exit(codexdebug.Run(os.Args[1:]))
}
