package codexdebug

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maxStderr = 512 * 1024

type wrapperConfig struct {
	CodexBin string `json:"codex_bin"`
}

func Run(args []string) int {
	codexPath, codexArgs, err := ParseArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[codex-debug] error=%s\n", SanitizeValue(err.Error()))
		return 2
	}
	if wrapperPath, err := os.Executable(); err == nil && samePath(wrapperPath, codexPath) {
		fmt.Fprintf(os.Stderr, "[codex-debug] error=resolved codex path points to wrapper: %s\n", SanitizeValue(codexPath))
		return 2
	}

	printStartup(codexPath, len(codexArgs))

	cmd := exec.Command(codexPath, codexArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	stderrTail := newTailBuffer(maxStderr)
	cmd.Stderr = io.MultiWriter(os.Stderr, stderrTail)

	err = cmd.Run()
	if err != nil {
		printResumeDiagnostics(stderrTail.String())
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "[codex-debug] exec_error=%s\n", SanitizeValue(err.Error()))
		return 1
	}
	return 0
}

func ParseArgs(args []string) (string, []string, error) {
	for i, arg := range args {
		if arg == "--codex" {
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" {
				return "", nil, fmt.Errorf("codex-debug-wrapper requires a value after --codex")
			}
			return args[i+1], append(append([]string(nil), args[:i]...), args[i+2:]...), nil
		}
		if strings.HasPrefix(arg, "--codex=") {
			codexPath := strings.TrimPrefix(arg, "--codex=")
			if strings.TrimSpace(codexPath) == "" {
				return "", nil, fmt.Errorf("codex-debug-wrapper requires a non-empty --codex value")
			}
			return codexPath, append(append([]string(nil), args[:i]...), args[i+1:]...), nil
		}
	}
	return ResolveDefaultCodexPath(), append([]string(nil), args...), nil
}

func ResolveDefaultCodexPath() string {
	for _, name := range []string{"AGENTSERVER_CODEX_BIN", "LOOM_CODEX_REAL_BIN", "CODEX_REAL_BIN"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	if wrapperPath, err := os.Executable(); err == nil {
		if path := codexPathFromConfig(ConfigPathForExecutable(wrapperPath)); path != "" {
			return path
		}
		dir := filepath.Dir(wrapperPath)
		for _, name := range codexExecutableNames() {
			candidate := filepath.Join(dir, name)
			if _, err := os.Stat(candidate); err == nil && !samePath(candidate, wrapperPath) {
				return candidate
			}
		}
	}
	if runtime.GOOS == "windows" {
		if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
			return filepath.Join(localAppData, "agentserver-app", "bin", "codex.exe")
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if runtime.GOOS == "windows" {
			return filepath.Join(home, "AppData", "Local", "agentserver-app", "bin", "codex.exe")
		}
		return filepath.Join(home, ".agentserver-app", "bin-root", "bin", "codex")
	}
	return "codex"
}

func ConfigPathForExecutable(exePath string) string {
	dir := filepath.Dir(exePath)
	return filepath.Join(dir, "codex-debug-wrapper.json")
}

func codexPathFromConfig(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cfg wrapperConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.CodexBin)
}

func codexExecutableNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"codex.exe"}
	}
	return []string{"codex"}
}

func printStartup(codexPath string, codexArgCount int) {
	wrapperPath, _ := os.Executable()
	fmt.Fprintf(os.Stderr, "[codex-debug] wrapper=%s\n", SanitizeValue(wrapperPath))
	fmt.Fprintf(os.Stderr, "[codex-debug] codex=%s\n", SanitizeValue(codexPath))
	fmt.Fprintf(os.Stderr, "[codex-debug] codex_version=%s\n", SanitizeValue(version(codexPath)))
	fmt.Fprintf(os.Stderr, "[codex-debug] codex_home=%s\n", SanitizeValue(os.Getenv("CODEX_HOME")))
	fmt.Fprintf(os.Stderr, "[codex-debug] codex_arg_count=%d\n", codexArgCount)
}

func version(codexPath string) string {
	if strings.TrimSpace(codexPath) == "" {
		return "unavailable: empty codex path"
	}
	if wrapperPath, err := os.Executable(); err == nil && samePath(wrapperPath, codexPath) {
		return "skipped: codex path points to wrapper"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, codexPath, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "unavailable: timeout"
		}
		return "unavailable: " + err.Error()
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return "unavailable: empty version output"
	}
	return text
}

func printResumeDiagnostics(stderrText string) {
	paths := ThreadPaths(stderrText)
	if len(paths) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "[codex-debug] resume_thread_diagnostics_begin")
	for _, path := range paths {
		fmt.Fprintln(os.Stderr, SessionSummary(path))
	}
	fmt.Fprintln(os.Stderr, "[codex-debug] resume_thread_diagnostics_end")
}

func ThreadPaths(stderrText string) []string {
	markers := []string{
		"failed to read thread ",
		"rollout at ",
	}
	seen := map[string]bool{}
	var paths []string
	for _, marker := range markers {
		searchFrom := 0
		for {
			idx := strings.Index(stderrText[searchFrom:], marker)
			if idx < 0 {
				break
			}
			start := searchFrom + idx + len(marker)
			rest := stderrText[start:]
			end := strings.Index(rest, ".jsonl")
			if end < 0 {
				searchFrom = start
				continue
			}
			path := strings.Trim(rest[:end+len(".jsonl")], "`\"' \t\r\n")
			if path != "" && !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
			searchFrom = start + end + len(".jsonl")
		}
	}
	return paths
}

func SessionSummary(path string) string {
	var fields []string
	fields = append(fields, "[codex-debug] session_path="+SanitizeValue(path))

	info, err := os.Stat(path)
	if err != nil {
		fields = append(fields, "exists=false", "stat_error="+SanitizeValue(err.Error()))
		return strings.Join(fields, " ")
	}
	fields = append(fields,
		"exists=true",
		fmt.Sprintf("size=%d", info.Size()),
		"mtime_utc="+info.ModTime().UTC().Format(time.RFC3339Nano),
	)

	fullHash, firstLine, firstLineHash, err := hashes(path)
	if err != nil {
		fields = append(fields, "read_error="+SanitizeValue(err.Error()))
		return strings.Join(fields, " ")
	}
	fields = append(fields, "sha256="+fullHash, "first_line_sha256="+firstLineHash)

	var envelope struct {
		Type    string         `json:"type"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal([]byte(firstLine), &envelope); err != nil {
		fields = append(fields, "first_json_ok=false", "first_json_error="+SanitizeValue(err.Error()))
		return strings.Join(fields, " ")
	}
	fields = append(fields, "first_json_ok=true", "first_type="+SanitizeValue(envelope.Type))
	fields = appendPayloadField(fields, envelope.Payload, "session_id", "payload_session_id")
	fields = appendPayloadField(fields, envelope.Payload, "id", "payload_id")
	fields = appendPayloadField(fields, envelope.Payload, "cli_version", "payload_cli_version")
	fields = appendPayloadField(fields, envelope.Payload, "cwd", "payload_cwd")
	return strings.Join(fields, " ")
}

func hashes(path string) (string, string, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", "", "", err
	}
	defer file.Close()

	full := sha256.New()
	if _, err := io.Copy(full, file); err != nil {
		return "", "", "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", "", "", err
	}
	reader := bufio.NewReader(file)
	firstLine, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", "", "", err
	}
	firstLine = strings.TrimRight(firstLine, "\r\n")
	firstHash := sha256.Sum256([]byte(firstLine))
	return hex.EncodeToString(full.Sum(nil)), firstLine, hex.EncodeToString(firstHash[:]), nil
}

func appendPayloadField(fields []string, payload map[string]any, key, fieldName string) []string {
	if payload == nil {
		return fields
	}
	value, ok := payload[key]
	if !ok {
		return fields
	}
	text, ok := value.(string)
	if !ok {
		return fields
	}
	return append(fields, fieldName+"="+SanitizeValue(text))
}

func SanitizeValue(value string) string {
	value = strings.ReplaceAll(value, "\r", "\\r")
	value = strings.ReplaceAll(value, "\n", "\\n")
	return value
}

func samePath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	aa, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	bb, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}

type tailBuffer struct {
	max int
	buf []byte
}

func newTailBuffer(max int) *tailBuffer {
	return &tailBuffer{max: max}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	if b.max <= 0 {
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-b.max:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	return string(b.buf)
}
