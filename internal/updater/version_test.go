package updater

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"0.1.2", "0.1.1", 1},
		{"0.1.10", "0.1.2", 1},
		{"v0.2.0", "0.1.9", 1},
		{"0.1.1", "0.1.1", 0},
		{"0.1.1", "0.1.2", -1},
	}
	for _, tt := range tests {
		got, err := CompareVersions(tt.a, tt.b)
		if err != nil {
			t.Fatalf("CompareVersions(%q,%q): %v", tt.a, tt.b, err)
		}
		if got != tt.want {
			t.Fatalf("CompareVersions(%q,%q)=%d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestCompareVersionsRejectsInvalidVersion(t *testing.T) {
	if _, err := CompareVersions("latest", "0.1.1"); err == nil {
		t.Fatal("expected invalid version error")
	}
}
