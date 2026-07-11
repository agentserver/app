package codexdesktop

func hresultFailed(hr uintptr) bool {
	return uint32(hr)&0x80000000 != 0
}
