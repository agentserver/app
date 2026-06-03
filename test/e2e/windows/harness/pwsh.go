//go:build e2e

package harness

import (
	"bytes"
	"fmt"
	"strings"
)

// Pwsh runs `powershell -NoProfile -Command <script>` and returns stdout
// (or stderr on non-zero exit) along with the exit code.
func (c *Client) Pwsh(script string) (stdout string, exitCode int, err error) {
	sess, err := c.NewSession()
	if err != nil {
		return "", -1, err
	}
	defer sess.Close()
	var outB, errB bytes.Buffer
	sess.Stdout = &outB
	sess.Stderr = &errB
	full := fmt.Sprintf("powershell -NoProfile -Command %q", script)
	runErr := sess.Run(full)
	if runErr == nil {
		return outB.String(), 0, nil
	}
	if ee, ok := runErr.(sshExitError); ok {
		return strings.TrimSpace(outB.String() + errB.String()), ee.ExitStatus(), nil
	}
	return strings.TrimSpace(outB.String() + errB.String()), -1, runErr
}

// sshExitError is just *ssh.ExitError aliased to avoid leaking import.
type sshExitError interface {
	ExitStatus() int
}
