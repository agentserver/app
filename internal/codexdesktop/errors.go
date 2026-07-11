package codexdesktop

type safeError struct {
	message string
	causes  []error
}

func (e *safeError) Error() string {
	return e.message
}

func (e *safeError) Unwrap() []error {
	return e.causes
}

func newSafeError(message string, causes ...error) error {
	filtered := make([]error, 0, len(causes))
	for _, cause := range causes {
		if cause != nil {
			filtered = append(filtered, cause)
		}
	}
	return &safeError{message: message, causes: filtered}
}
