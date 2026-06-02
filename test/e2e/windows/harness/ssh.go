//go:build e2e

// Package harness wraps SSH + PowerShell + WebDriver for the Windows E2E.
package harness

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"
)

type Client struct {
	*ssh.Client
}

// Dial reads env: E2E_SSH_HOST, E2E_SSH_PORT, E2E_SSH_USER, E2E_SSH_PASSWORD.
func Dial() (*Client, error) {
	host := os.Getenv("E2E_SSH_HOST")
	port := os.Getenv("E2E_SSH_PORT")
	user := os.Getenv("E2E_SSH_USER")
	pass := os.Getenv("E2E_SSH_PASSWORD")
	if host == "" || user == "" {
		return nil, fmt.Errorf("E2E_SSH_HOST and E2E_SSH_USER required")
	}
	if port == "" {
		port = "22"
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // E2E only
		Timeout:         15 * time.Second,
	}
	c, err := ssh.Dial("tcp", host+":"+port, cfg)
	if err != nil {
		return nil, err
	}
	return &Client{c}, nil
}

// PutFile writes local path src to remote path dst via SCP-like protocol.
// Implemented as cat over an SSH session (Windows OpenSSH supports redirection).
func (c *Client) PutFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	sess, err := c.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	stdin, err := sess.StdinPipe()
	if err != nil {
		return err
	}
	cmd := fmt.Sprintf(`powershell -NoProfile -Command "[System.IO.File]::WriteAllBytes('%s', [Convert]::FromBase64String((Get-Content -Raw -Path -)))"`,
		dst)
	if err := sess.Start(cmd); err != nil {
		return err
	}
	// stream base64 of file body to stdin
	encoded := newBase64Pipe(in)
	if _, err := io.Copy(stdin, encoded); err != nil {
		return err
	}
	stdin.Close()
	return sess.Wait()
}

func (c *Client) GetFile(remoteSrc, localDst string) error {
	sess, err := c.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	var out bytes.Buffer
	sess.Stdout = &out
	if err := sess.Run(fmt.Sprintf(`powershell -NoProfile -Command "[Convert]::ToBase64String([System.IO.File]::ReadAllBytes('%s'))"`,
		remoteSrc)); err != nil {
		return err
	}
	dec := newBase64Reader(&out)
	if err := os.MkdirAll(filepath.Dir(localDst), 0o755); err != nil {
		return err
	}
	f, err := os.Create(localDst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, dec)
	return err
}
