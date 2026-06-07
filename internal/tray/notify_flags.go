package tray

const (
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010
	nifShowTip = 0x00000080
)

func tooltipNotifyFlags() uint32 {
	return nifIcon | nifTip | nifShowTip
}

func addIconNotifyFlags() uint32 {
	return nifMessage | tooltipNotifyFlags()
}
