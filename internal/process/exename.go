package process

import "runtime"

// ExeName returns the platform-correct file name for a sibling executable.
// On Windows it appends ".exe"; on macOS/Linux the name is returned unchanged.
func ExeName(name string) string {
	return exeNameFor(runtime.GOOS, name)
}

func exeNameFor(goos, name string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}
