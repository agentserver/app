package tray

import "testing"

func TestTooltipNotifyFlagsIncludeShowTipForVersion4(t *testing.T) {
	got := tooltipNotifyFlags()
	for _, want := range []uint32{nifIcon, nifTip, nifShowTip} {
		if got&want == 0 {
			t.Fatalf("tooltipNotifyFlags()=%#x missing %#x", got, want)
		}
	}
}

func TestAddIconNotifyFlagsIncludeMessageAndTooltipFlags(t *testing.T) {
	got := addIconNotifyFlags()
	for _, want := range []uint32{nifMessage, nifIcon, nifTip, nifShowTip} {
		if got&want == 0 {
			t.Fatalf("addIconNotifyFlags()=%#x missing %#x", got, want)
		}
	}
}
