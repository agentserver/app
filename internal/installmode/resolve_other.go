//go:build !darwin

package installmode

// Path resolves the install-mode.json location for the running binary.
// Windows/Linux: next to the executable.
func Path() (string, error) { return PathFromExecutable() }
