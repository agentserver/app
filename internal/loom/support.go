package loom

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	loomPromptStartMarker = "<!-- agentserver-app loom driver prompt:start -->"
	loomPromptEndMarker   = "<!-- agentserver-app loom driver prompt:end -->"
)

type DriverSupportInput struct {
	UserHome                string
	SkillsArchivePath       string
	CodexPromptsArchivePath string
}

func InstallDriverSupport(in DriverSupportInput) error {
	if strings.TrimSpace(in.UserHome) == "" {
		return nil
	}
	if in.SkillsArchivePath != "" {
		exists, err := fileExists(in.SkillsArchivePath)
		if err != nil {
			return err
		}
		if exists {
			for _, root := range []string{
				filepath.Join(in.UserHome, ".agents", "skills"),
				filepath.Join(in.UserHome, ".codex", "skills"),
			} {
				if err := extractArchivePrefix(in.SkillsArchivePath, "skills", root); err != nil {
					return err
				}
			}
		}
	}
	if in.CodexPromptsArchivePath != "" {
		exists, err := fileExists(in.CodexPromptsArchivePath)
		if err != nil {
			return err
		}
		if exists {
			prompt, ok, err := readArchiveFile(in.CodexPromptsArchivePath, "prompts-codex/AGENTS.md")
			if err != nil {
				return err
			}
			if ok {
				if err := mergeCodexAgents(filepath.Join(in.UserHome, ".codex", "AGENTS.md"), string(prompt)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func extractArchivePrefix(archivePath, prefix, destRoot string) error {
	return walkTarGz(archivePath, func(h *tar.Header, r io.Reader) error {
		cleanName, err := cleanTarName(h.Name)
		if err != nil {
			return err
		}
		rel, ok := strings.CutPrefix(cleanName, prefix+"/")
		if !ok || rel == "" {
			return nil
		}
		target, err := safeJoin(destRoot, rel)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			return os.MkdirAll(target, 0o755)
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return writeFileFromReader(target, r, h.FileInfo().Mode().Perm())
		default:
			return fmt.Errorf("unsupported tar entry %s type %d", h.Name, h.Typeflag)
		}
	})
}

func readArchiveFile(archivePath, want string) ([]byte, bool, error) {
	var out []byte
	found := false
	err := walkTarGz(archivePath, func(h *tar.Header, r io.Reader) error {
		cleanName, err := cleanTarName(h.Name)
		if err != nil {
			return err
		}
		if cleanName != want {
			return nil
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return fmt.Errorf("tar entry %s is not a regular file", h.Name)
		}
		out, err = io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("read tar entry %s: %w", h.Name, err)
		}
		found = true
		return nil
	})
	return out, found, err
}

func walkTarGz(archivePath string, fn func(*tar.Header, io.Reader) error) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip archive %s: %w", archivePath, err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar archive %s: %w", archivePath, err)
		}
		if err := fn(h, tr); err != nil {
			return err
		}
	}
}

func cleanTarName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty tar entry name")
	}
	slashName := strings.ReplaceAll(name, "\\", "/")
	if path.IsAbs(slashName) {
		return "", fmt.Errorf("tar entry must be relative: %s", name)
	}
	for _, part := range strings.Split(slashName, "/") {
		if part == ".." {
			return "", fmt.Errorf("tar entry escapes destination: %s", name)
		}
	}
	clean := path.Clean(slashName)
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("tar entry escapes destination: %s", name)
	}
	return clean, nil
}

func safeJoin(root, rel string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(rel))
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	back, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", err
	}
	if back == "." || strings.HasPrefix(back, ".."+string(os.PathSeparator)) || back == ".." {
		return "", fmt.Errorf("target escapes destination: %s", rel)
	}
	return target, nil
}

func writeFileFromReader(path string, r io.Reader, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	return errors.Join(copyErr, closeErr)
}

func mergeCodexAgents(path string, prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	existingBytes, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	next, err := mergeManagedPromptBlock(string(existingBytes), prompt)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(next), 0o644)
}

func mergeManagedPromptBlock(existing string, prompt string) (string, error) {
	block := loomPromptStartMarker + "\n" + strings.TrimRight(prompt, "\n") + "\n" + loomPromptEndMarker
	if strings.TrimSpace(existing) == "" {
		return block + "\n", nil
	}
	start := strings.Index(existing, loomPromptStartMarker)
	end := strings.Index(existing, loomPromptEndMarker)
	if start >= 0 || end >= 0 {
		if start < 0 || end < 0 || end < start {
			return "", fmt.Errorf("existing Codex AGENTS.md has malformed Loom managed block")
		}
		end += len(loomPromptEndMarker)
		return existing[:start] + block + existing[end:], nil
	}
	return strings.TrimRight(existing, "\n") + "\n\n" + block + "\n", nil
}
