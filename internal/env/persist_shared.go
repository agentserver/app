package env

import "strings"

const (
	managedStartMarker = "# agentserver-managed:start"
	managedEndMarker   = "# agentserver-managed:end"
)

// injectManagedBlock replaces any existing managed block in `content` with one
// containing `lines`, preserving everything else. Appends at end if absent.
func injectManagedBlock(content, lines, start, end string) string {
	cleaned := removeManagedBlock(content, start, end)
	cleaned = strings.TrimRight(cleaned, "\n")
	if cleaned != "" {
		cleaned += "\n"
	}
	return cleaned + start + "\n" + ensureTrailingNewline(lines) + end + "\n"
}

// removeManagedBlock strips the managed block (inclusive) from `content`.
func removeManagedBlock(content, start, end string) string {
	sIdx := strings.Index(content, start)
	if sIdx < 0 {
		return content
	}
	eIdx := strings.Index(content[sIdx:], end)
	if eIdx < 0 {
		return content
	}
	eIdx += sIdx + len(end)
	rest := content[eIdx:]
	rest = strings.TrimPrefix(rest, "\n")
	return content[:sIdx] + rest
}

func ensureTrailingNewline(s string) string {
	if s == "" {
		return ""
	}
	if !strings.HasSuffix(s, "\n") {
		return s + "\n"
	}
	return s
}

// shellQuote wraps s in POSIX single quotes so it is safe to embed in a shell
// rc file (export KEY=<quoted>). Embedded single quotes are escaped with the
// standard close-quote/escaped-quote/reopen-quote sequence. Go's %q is NOT
// shell-safe (it leaves $, backtick, and quote characters intact), so it must
// not be used here — a value containing $(...) or backticks would be executed
// when the rc file is sourced.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
