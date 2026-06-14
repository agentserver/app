package slave

// ProcessExists reports whether pid currently names a live OS process.
func ProcessExists(pid int) bool {
	return osProcessExists(pid)
}
