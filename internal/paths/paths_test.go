package paths

import "testing"

func TestPathsConsistent(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if p.UserHome == "" {
		t.Errorf("UserHome empty")
	}
	if p.StateFile == "" || p.SecretsFile == "" {
		t.Errorf("missing state/secrets path")
	}
	if p.CacheDir == "" {
		t.Errorf("missing cache dir")
	}
}
