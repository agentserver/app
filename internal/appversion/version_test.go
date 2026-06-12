package appversion

import (
	"regexp"
	"testing"
)

func TestVersionIsSemverLike(t *testing.T) {
	if Version == "" {
		t.Fatal("Version is empty")
	}
	if !regexp.MustCompile(`^v?\d+\.\d+\.\d+$`).MatchString(Version) {
		t.Fatalf("Version=%q, want v?MAJOR.MINOR.PATCH", Version)
	}
}
