package console

import "testing"

func TestParseUIDFromPS(t *testing.T) {
	tests := []struct {
		in   string
		uid  int
		ok   bool
	}{
		{"  501\n", 501, true},
		{"501", 501, true},
		{"", 0, false},
		{"  -  \n", 0, false},
		{"notanumber", 0, false},
	}
	for _, tt := range tests {
		uid, ok := parseUIDFromPS(tt.in)
		if ok != tt.ok || uid != tt.uid {
			t.Errorf("parseUIDFromPS(%q)=(%d,%v) want (%d,%v)", tt.in, uid, ok, tt.uid, tt.ok)
		}
	}
}

func TestBelongsToCurrentUser(t *testing.T) {
	resolver := func(want map[int]int) func(pid int) (int, bool) {
		return func(pid int) (int, bool) {
			uid, ok := want[pid]
			return uid, ok
		}
	}
	if !instanceProcessBelongsToCurrentUserWith(100, 501, resolver(map[int]int{100: 501})) {
		t.Error("pid 100 owned by 501 should belong to current 501")
	}
	if instanceProcessBelongsToCurrentUserWith(100, 501, resolver(map[int]int{100: 0})) {
		t.Error("pid 100 owned by root should NOT belong to current 501")
	}
	if instanceProcessBelongsToCurrentUserWith(100, 501, resolver(map[int]int{})) {
		t.Error("unknown pid should NOT belong to current user")
	}
	if instanceProcessBelongsToCurrentUserWith(0, 501, resolver(map[int]int{})) {
		t.Error("pid<=0 should not belong")
	}
}
