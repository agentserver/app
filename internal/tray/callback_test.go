package tray

import "testing"

func TestTrayCallbackEventAcceptsLegacyPlainLParam(t *testing.T) {
	event, ok := trayCallbackEvent(uintptr(wmContextMenu))
	if !ok {
		t.Fatal("legacy callback should be accepted")
	}
	if event != wmContextMenu {
		t.Fatalf("event=%#x, want %#x", event, wmContextMenu)
	}
}

func TestTrayCallbackEventAcceptsV4LParamForTrayIcon(t *testing.T) {
	event, ok := trayCallbackEvent(uintptr((trayIconID << 16) | wmContextMenu))
	if !ok {
		t.Fatal("v4 callback for tray icon should be accepted")
	}
	if event != wmContextMenu {
		t.Fatalf("event=%#x, want %#x", event, wmContextMenu)
	}
}

func TestTrayCallbackEventRejectsMismatchedIconID(t *testing.T) {
	_, ok := trayCallbackEvent(uintptr(((trayIconID + 1) << 16) | wmContextMenu))
	if ok {
		t.Fatal("v4 callback for another icon should be rejected")
	}
}
