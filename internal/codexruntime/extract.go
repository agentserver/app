package codexruntime

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type ExtractOptions struct {
	StripPrefix   string
	RequiredFiles []string
}

func ExtractRuntime(r io.Reader, destRoot string, opts ExtractOptions) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		rel, ok, err := stripPackageRuntimePrefix(h.Name, opts.StripPrefix)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return fmt.Errorf("unsupported tar entry %s type %d", h.Name, h.Typeflag)
		}
		dst, err := safeJoin(destRoot, rel)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		tmp := dst + ".tmp"
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		if err := os.Rename(tmp, dst); err != nil {
			return err
		}
	}
	for _, req := range opts.RequiredFiles {
		dst, err := safeJoin(destRoot, req)
		if err != nil {
			return err
		}
		if _, err := os.Stat(dst); err != nil {
			return fmt.Errorf("Codex npm package missing required file %s: %w", req, err)
		}
	}
	return nil
}

func stripPackageRuntimePrefix(name, stripPrefix string) (string, bool, error) {
	raw := strings.TrimPrefix(name, "package/")
	if raw == "" || strings.HasPrefix(raw, "/") {
		return "", false, fmt.Errorf("unsafe runtime path %q", name)
	}
	for _, part := range strings.Split(raw, "/") {
		if part == ".." {
			return "", false, fmt.Errorf("unsafe runtime path %q", name)
		}
	}
	cleaned := path.Clean(raw)
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", false, fmt.Errorf("unsafe runtime path %q", name)
	}
	if !strings.HasPrefix(cleaned, stripPrefix) {
		return "", false, nil
	}
	rel := strings.TrimPrefix(cleaned, stripPrefix)
	return rel, rel != "" && rel != ".", nil
}

func safeJoin(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("unsafe runtime path %q", rel)
	}
	cleaned := filepath.Clean(filepath.FromSlash(rel))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe runtime path %q", rel)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(rootAbs, cleaned)
	if dst != rootAbs && !strings.HasPrefix(dst, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe runtime path %q", rel)
	}
	return dst, nil
}
