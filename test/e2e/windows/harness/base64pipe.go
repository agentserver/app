//go:build e2e

package harness

import (
	"encoding/base64"
	"io"
)

func newBase64Pipe(r io.Reader) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		enc := base64.NewEncoder(base64.StdEncoding, pw)
		_, err := io.Copy(enc, r)
		enc.Close()
		pw.CloseWithError(err)
	}()
	return pr
}

func newBase64Reader(r io.Reader) io.Reader {
	return base64.NewDecoder(base64.StdEncoding, r)
}
