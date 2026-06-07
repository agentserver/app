package tray

const (
	trayIconID = 1

	wmRButtonUp   = 0x0205
	wmContextMenu = 0x007B
	wmLButtonDbl  = 0x0203
)

func trayCallbackEvent(lParam uintptr) (uint32, bool) {
	raw := uint32(lParam)
	event := raw & 0xffff
	iconID := raw >> 16
	if iconID == 0 {
		return event, true
	}
	if iconID != trayIconID {
		return 0, false
	}
	return event, true
}
