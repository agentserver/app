package protoconv

import "testing"

func TestLookupRoute(t *testing.T) {
	cases := []struct {
		model string
		want  Wire
		ok    bool
	}{
		{"gpt-5.5", WireResponses, true},
		{"deepseek-v4-pro", WireChat, true},
		{"glm-5.2", WireAnthropic, true},
		{"does-not-exist", "", false},
	}
	for _, c := range cases {
		r, ok := LookupRoute(c.model)
		if ok != c.ok {
			t.Errorf("LookupRoute(%q) ok = %v, want %v", c.model, ok, c.ok)
			continue
		}
		if ok && r.Wire != c.want {
			t.Errorf("LookupRoute(%q) wire = %q, want %q", c.model, r.Wire, c.want)
		}
	}
}

func TestKnownModels(t *testing.T) {
	got := KnownModels()
	want := map[string]bool{"gpt-5.5": true, "deepseek-v4-pro": true, "glm-5.2": true}
	if len(got) != len(want) {
		t.Fatalf("KnownModels() = %v, want %d entries", got, len(want))
	}
	for _, m := range got {
		if !want[m] {
			t.Errorf("KnownModels() unexpected entry %q", m)
		}
	}
}
