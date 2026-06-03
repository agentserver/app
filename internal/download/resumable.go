package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

// DownloadResumable downloads url to dst, resuming a prior .part if compatible.
// On completion, the file is sha256-verified against expectedSHA256 and renamed.
// progress (if non-nil) receives periodic events.
func DownloadResumable(ctx context.Context, url, dst, expectedSHA256 string,
	progress chan<- ProgressEvent) error {

	part := dst + ".part"
	metaPath := dst + ".meta"

	// 1. HEAD to discover ETag and Content-Length.
	headReq, _ := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	headResp, err := http.DefaultClient.Do(headReq)
	if err != nil {
		return fmt.Errorf("HEAD %s: %w", url, err)
	}
	headResp.Body.Close()
	if headResp.StatusCode/100 != 2 {
		return fmt.Errorf("HEAD %s: status %d", url, headResp.StatusCode)
	}
	etag := headResp.Header.Get("ETag")
	totalSize := headResp.ContentLength
	acceptsRange := headResp.Header.Get("Accept-Ranges") == "bytes"

	// 2. Decide whether to resume.
	var offset int64
	prevMeta, _ := loadMeta(metaPath)
	partInfo, partErr := os.Stat(part)
	canResume := acceptsRange && partErr == nil &&
		prevMeta.URL == url && prevMeta.ETag == etag && etag != ""
	if canResume {
		offset = partInfo.Size()
		if offset >= totalSize && totalSize > 0 {
			offset = 0 // file already as long as expected, but missing meta; restart
		}
	} else {
		_ = os.Remove(part)
		_ = os.Remove(metaPath)
		offset = 0
	}

	// 3. Range GET.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusPartialContent:
		// good
	case resp.StatusCode == http.StatusOK:
		// server ignored Range; truncate
		offset = 0
		_ = os.Remove(part)
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		_ = os.Remove(part)
		return errors.New("server returned 416; clearing and please retry")
	default:
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}

	// 4. Write meta (so resume works on next call).
	newMeta := Meta{URL: url, ETag: etag, TotalSize: totalSize, SHA256: expectedSHA256}
	if mb, err := newMeta.Marshal(); err == nil {
		_ = os.WriteFile(metaPath, mb, 0o644)
	}

	// 5. Append-write .part with progress.
	flag := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(part, flag, 0o644)
	if err != nil {
		return fmt.Errorf("open part: %w", err)
	}
	written := offset
	start := time.Now()
	buf := make([]byte, 64*1024)
	for {
		select {
		case <-ctx.Done():
			f.Close()
			return ctx.Err()
		default:
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return werr
			}
			written += int64(n)
			if progress != nil {
				speed := int64(0)
				if d := time.Since(start).Seconds(); d > 0 {
					speed = int64(float64(written-offset) / d)
				}
				select {
				case progress <- ProgressEvent{
					Downloaded: written, Total: totalSize, SpeedBps: speed,
					Stage: "download",
				}:
				default:
				}
			}
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			f.Close()
			return fmt.Errorf("read body: %w", rerr)
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	// 6. Verify sha256.
	if expectedSHA256 != "" {
		got, err := fileSHA256(part)
		if err != nil {
			return err
		}
		if got != expectedSHA256 {
			_ = os.Remove(part)
			_ = os.Remove(metaPath)
			return fmt.Errorf("sha256 mismatch: got %s want %s", got, expectedSHA256)
		}
	}

	// 7. Rename .part → dst, cleanup .meta.
	if err := os.Rename(part, dst); err != nil {
		return err
	}
	_ = os.Remove(metaPath)
	return nil
}

func loadMeta(path string) (Meta, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Meta{}, err
	}
	return UnmarshalMeta(b)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// noUnusedStrconvImport ensures strconv stays referenced if we trim above.
var _ = strconv.Itoa
