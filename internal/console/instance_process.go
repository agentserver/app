package console

import (
	"os"
	"strconv"
	"strings"
)

// parseUIDFromPS parses the uid emitted by `ps -o uid= -p <pid>` (whitespace-padded).
// Returns ok=false when output is empty or non-numeric.
func parseUIDFromPS(out string) (int, bool) {
	s := strings.TrimSpace(out)
	if s == "" {
		return 0, false
	}
	uid, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return uid, true
}

// instanceProcessBelongsToCurrentUserWith is the platform-agnostic core: it asks
// the resolver for the owning uid of `pid` and compares against currentUID.
func instanceProcessBelongsToCurrentUserWith(pid, currentUID int, resolve func(pid int) (int, bool)) bool {
	if pid <= 0 {
		return false
	}
	uid, ok := resolve(pid)
	if !ok {
		return false
	}
	return uid == currentUID
}

func currentUID() int { return os.Getuid() }
