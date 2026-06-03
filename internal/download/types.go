// Package download implements resumable HTTP file downloads.
package download

import (
	"encoding/json"
	"fmt"
)

// ProgressEvent is pushed on the progress channel during download.
type ProgressEvent struct {
	Downloaded int64
	Total      int64
	SpeedBps   int64
	Stage      string // e.g. "head", "download", "verify"
	Msg        string
}

func (e ProgressEvent) String() string {
	return fmt.Sprintf("%s / %s @ %s/s",
		humanBytes(e.Downloaded), humanBytes(e.Total), humanBytes(e.SpeedBps))
}

// Meta accompanies a .part file so we can verify resumability later.
type Meta struct {
	URL       string `json:"url"`
	ETag      string `json:"etag"`
	TotalSize int64  `json:"total_size"`
	SHA256    string `json:"sha256"`
}

func (m Meta) Marshal() ([]byte, error) { return json.MarshalIndent(m, "", "  ") }

func UnmarshalMeta(b []byte) (Meta, error) {
	var m Meta
	err := json.Unmarshal(b, &m)
	return m, err
}

func humanBytes(n int64) string {
	const k = 1024.0
	if n < int64(k) {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	v := float64(n) / k
	for _, u := range units {
		if v < k {
			return fmt.Sprintf("%.1f %s", v, u)
		}
		v /= k
	}
	return fmt.Sprintf("%.1f PiB", v)
}
