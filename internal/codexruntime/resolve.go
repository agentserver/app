package codexruntime

type PackageCandidate struct {
	Version   string
	URL       string
	Integrity string
	Shasum    string
	Source    string
}

func PinnedCandidates(m Manifest) []PackageCandidate {
	out := make([]PackageCandidate, 0, len(m.Pinned.URLs)+fallbackURLCount(m.FallbackPinned))
	for _, u := range m.Pinned.URLs {
		out = append(out, PackageCandidate{
			Version:   m.PinnedVersion,
			URL:       u,
			Integrity: m.Pinned.Integrity,
			Shasum:    m.Pinned.Shasum,
			Source:    "pinned",
		})
	}
	for _, fallback := range m.FallbackPinned {
		for _, u := range fallback.URLs {
			out = append(out, PackageCandidate{
				Version:   fallback.Version,
				URL:       u,
				Integrity: fallback.Integrity,
				Shasum:    fallback.Shasum,
				Source:    "fallback-pinned",
			})
		}
	}
	return out
}

func fallbackURLCount(candidates []PinnedPackage) int {
	n := 0
	for _, candidate := range candidates {
		n += len(candidate.URLs)
	}
	return n
}
