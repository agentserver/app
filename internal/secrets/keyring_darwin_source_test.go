package secrets

import (
	"os"
	"strings"
	"testing"
)

func TestDarwinKeyringSetUsesSecurityPasswordArgument(t *testing.T) {
	body, err := os.ReadFile("keyring_darwin.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, want := range []string{
		`"add-generic-password"`,
		`"-w", value`,
		"errors.As(err, &exitErr)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("keyring_darwin.go missing %q:\n%s", want, source)
		}
	}
	for _, notWant := range []string{
		"cmd.Stdin = strings.NewReader(value)",
		`fmt.Sprintf("%T", err)`,
		"_ = exitErr",
	} {
		if strings.Contains(source, notWant) {
			t.Fatalf("keyring_darwin.go contains obsolete pattern %q:\n%s", notWant, source)
		}
	}
}
