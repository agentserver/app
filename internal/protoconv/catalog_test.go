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

func TestCatalogHasDisplayNamesAndIsACopy(t *testing.T) {
	got := Catalog()
	if len(got) != 3 {
		t.Fatalf("Catalog() len = %d, want 3", len(got))
	}
	wantDisplay := map[string]string{
		"gpt-5.5":         "GPT-5.5",
		"deepseek-v4-pro": "DeepSeek v4 Pro",
		"glm-5.2":         "智谱 GLM-5.2",
	}
	for _, r := range got {
		if r.DisplayName != wantDisplay[r.Model] {
			t.Errorf("Catalog() %q display = %q, want %q", r.Model, r.DisplayName, wantDisplay[r.Model])
		}
	}
	// Mutation by caller must not leak into the package table.
	got[0].Model = "mutated"
	if Catalog()[0].Model == "mutated" {
		t.Errorf("Catalog() returned a shared slice; caller mutation leaked back")
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
