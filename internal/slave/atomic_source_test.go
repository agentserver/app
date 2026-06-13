package slave

import (
	"strings"
	"testing"
)

func TestRegistryAndConfigWritesSyncBeforeRename(t *testing.T) {
	for _, file := range []string{"registry.go", "config.go"} {
		t.Run(file, func(t *testing.T) {
			source := readPackageSourceFile(t, file)
			if !strings.Contains(source, ".Sync()") {
				t.Fatalf("%s should fsync temporary file before rename:\n%s", file, source)
			}
			if !strings.Contains(source, "replaceFile(") {
				t.Fatalf("%s should publish through replaceFile:\n%s", file, source)
			}
		})
	}
}
