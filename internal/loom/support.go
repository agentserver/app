package loom

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	loomPromptStartMarker = "<!-- agentserver-app loom driver prompt:start -->"
	loomPromptEndMarker   = "<!-- agentserver-app loom driver prompt:end -->"
)

type DriverSupportInput struct {
	UserHome                    string
	SkillsArchivePath           string
	SuperpowerSkillsArchivePath string
	CodexPromptsArchivePath     string
}

type skillsManifest struct {
	Version int                  `json:"version"`
	Files   []skillsManifestFile `json:"files"`
}

type skillsManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func InstallDriverSupport(in DriverSupportInput) error {
	if strings.TrimSpace(in.UserHome) == "" {
		return nil
	}
	if in.SkillsArchivePath != "" {
		if err := installSkillsArchive(in.UserHome, in.SkillsArchivePath); err != nil {
			return err
		}
	}
	if in.SuperpowerSkillsArchivePath != "" {
		if err := installSkillsArchive(in.UserHome, in.SuperpowerSkillsArchivePath); err != nil {
			return err
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

func installSkillsArchive(userHome, archivePath string) error {
	exists, err := fileExists(archivePath)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	for _, root := range []string{
		filepath.Join(userHome, ".agents", "skills"),
		filepath.Join(userHome, ".codex", "skills"),
	} {
		if err := extractSkillsArchive(archivePath, root); err != nil {
			return err
		}
	}
	return nil
}

func extractSkillsArchive(archivePath, destRoot string) error {
	manifest, err := loadSkillsManifest(destRoot)
	if err != nil {
		return err
	}
	if err := walkTarGz(archivePath, func(h *tar.Header, r io.Reader) error {
		cleanName, err := cleanTarName(h.Name)
		if err != nil {
			return err
		}
		rel, ok := strings.CutPrefix(cleanName, "skills/")
		if !ok {
			rel = cleanName
		}
		if rel == "" || rel == "skills" {
			return nil
		}
		target, err := safeJoin(destRoot, rel)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			return os.MkdirAll(target, 0o755)
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			data, err := io.ReadAll(r)
			if err != nil {
				return fmt.Errorf("read tar entry %s: %w", h.Name, err)
			}
			return installManagedSkillFile(target, rel, data, h.FileInfo().Mode().Perm(), manifest)
		default:
			return fmt.Errorf("unsupported tar entry %s type %d", h.Name, h.Typeflag)
		}
	}); err != nil {
		return err
	}
	return saveSkillsManifest(destRoot, manifest)
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
		if h.Typeflag != tar.TypeReg {
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

func skillsManifestPath(destRoot string) string {
	return filepath.Join(filepath.Dir(destRoot), ".agentserver-managed-skills.json")
}

func loadSkillsManifest(destRoot string) (map[string]string, error) {
	out := map[string]string{}
	b, err := os.ReadFile(skillsManifestPath(destRoot))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	var manifest skillsManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return nil, fmt.Errorf("read managed skills manifest: %w", err)
	}
	for _, f := range manifest.Files {
		rel := strings.TrimSpace(filepath.ToSlash(f.Path))
		sum := strings.ToLower(strings.TrimSpace(f.SHA256))
		if rel != "" && sum != "" {
			out[rel] = sum
		}
	}
	return out, nil
}

func saveSkillsManifest(destRoot string, files map[string]string) error {
	path := skillsManifestPath(destRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	manifest := skillsManifest{Version: 1, Files: make([]skillsManifestFile, 0, len(keys))}
	for _, k := range keys {
		manifest.Files = append(manifest.Files, skillsManifestFile{Path: k, SHA256: files[k]})
	}
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func installManagedSkillFile(path, rel string, data []byte, mode os.FileMode, manifest map[string]string) error {
	rel = filepath.ToSlash(rel)
	nextHash := sha256Hex(data)
	current, err := os.ReadFile(path)
	if err == nil {
		currentHash := sha256Hex(current)
		oldHash := manifest[rel]
		if oldHash == "" && currentHash == nextHash {
			manifest[rel] = nextHash
			return nil
		}
		if oldHash == "" || currentHash != oldHash {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeFile(path, data, mode); err != nil {
		return err
	}
	manifest[rel] = nextHash
	return nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := f.Write(data)
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
