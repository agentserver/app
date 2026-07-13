package codexdesktop

const protocolActivationContractUnsupportedHRESULT uintptr = 0x80270254

func hresultFailed(hr uintptr) bool {
	return uint32(hr)&0x80000000 != 0
}

func protocolActivationNeedsAppActivationFallback(hr uintptr) bool {
	return uint32(hr) == uint32(protocolActivationContractUnsupportedHRESULT)
}
