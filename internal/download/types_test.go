package download

import "testing"

func TestProgressEventString(t *testing.T) {
	e := ProgressEvent{
		Downloaded: 1024 * 1024,
		Total:      10 * 1024 * 1024,
		SpeedBps:   2 * 1024 * 1024,
	}
	got := e.String()
	want := "1.0 MiB / 10.0 MiB @ 2.0 MiB/s"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMetaRoundtrip(t *testing.T) {
	m := Meta{URL: "https://x", ETag: `"abc"`, TotalSize: 1234, SHA256: "deadbeef"}
	b, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalMeta(b)
	if err != nil {
		t.Fatal(err)
	}
	if got != m {
		t.Errorf("roundtrip: %+v vs %+v", got, m)
	}
}
