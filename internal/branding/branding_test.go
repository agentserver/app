package branding

import "testing"

func TestProductFacingLabelsArePlainChinese(t *testing.T) {
	if DisplayName != "星池指挥官" {
		t.Fatalf("DisplayName=%q, want 星池指挥官", DisplayName)
	}
	if ContextMenuLabel != "用星池指挥官打开" {
		t.Fatalf("ContextMenuLabel=%q, want 用星池指挥官打开", ContextMenuLabel)
	}
}
