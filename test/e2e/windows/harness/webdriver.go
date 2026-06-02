//go:build e2e

package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type WebDriver struct {
	base    string
	session string
}

// NewWebDriver expects a chromedriver listening at base (e.g. http://127.0.0.1:9515).
// Caller is responsible for launching chromedriver on the Windows host
// (e.g. via PowerShell: Start-Process -FilePath chromedriver.exe).
func NewWebDriver(base string) (*WebDriver, error) {
	w := &WebDriver{base: base}
	resp, err := w.post("/session", map[string]any{
		"capabilities": map[string]any{
			"alwaysMatch": map[string]any{
				"browserName": "chrome",
				"goog:chromeOptions": map[string]any{
					"args": []string{"--no-sandbox", "--disable-gpu"},
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Value struct {
			SessionID string `json:"sessionId"`
		} `json:"value"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, err
	}
	w.session = out.Value.SessionID
	return w, nil
}

func (w *WebDriver) Close() {
	if w.session != "" {
		_, _ = w.do("DELETE", "/session/"+w.session, nil)
	}
}

func (w *WebDriver) Go(url string) error {
	_, err := w.post("/session/"+w.session+"/url", map[string]string{"url": url})
	return err
}

func (w *WebDriver) FindAndType(cssSelector, text string) error {
	id, err := w.findElement(cssSelector)
	if err != nil {
		return err
	}
	_, err = w.post(fmt.Sprintf("/session/%s/element/%s/value", w.session, id),
		map[string]any{"text": text})
	return err
}

func (w *WebDriver) Click(cssSelector string) error {
	id, err := w.findElement(cssSelector)
	if err != nil {
		return err
	}
	_, err = w.post(fmt.Sprintf("/session/%s/element/%s/click", w.session, id), map[string]any{})
	return err
}

func (w *WebDriver) WaitForTitle(substr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		t, err := w.title()
		if err == nil && containsCI(t, substr) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("title %q not seen", substr)
}

func (w *WebDriver) title() (string, error) {
	resp, err := w.do("GET", "/session/"+w.session+"/title", nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return "", err
	}
	return out.Value, nil
}

func (w *WebDriver) findElement(selector string) (string, error) {
	resp, err := w.post("/session/"+w.session+"/element",
		map[string]string{"using": "css selector", "value": selector})
	if err != nil {
		return "", err
	}
	var out struct {
		Value map[string]string `json:"value"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return "", err
	}
	for k, v := range out.Value {
		if k != "ELEMENT" && k[0] != 'e' { // shape: {"element-6066-11e4-a52e-4f735466cecf": "..."}
			continue
		}
		return v, nil
	}
	return "", fmt.Errorf("no element id in response: %s", resp)
}

func (w *WebDriver) post(path string, body any) ([]byte, error) {
	return w.do("POST", path, body)
}

func (w *WebDriver) do(method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, w.base+path, rdr)
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return out, fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, out)
	}
	return out, nil
}

func containsCI(haystack, needle string) bool {
	// crude case-insensitive contains
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if equalFold(haystack[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if lower(a[i]) != lower(b[i]) {
			return false
		}
	}
	return true
}

func lower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 32
	}
	return b
}
