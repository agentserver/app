package codexruntime

type PackageCandidate struct {
	Version   string
	URL       string
	Integrity string
	Shasum    string
	Source    string
}

func PinnedCandidates(m Manifest) []PackageCandidate {
	out := make([]PackageCandidate, 0, len(m.Pinned.URLs))
	for _, u := range m.Pinned.URLs {
		out = append(out, PackageCandidate{
			Version:   m.PinnedVersion,
			URL:       u,
			Integrity: m.Pinned.Integrity,
			Shasum:    m.Pinned.Shasum,
			Source:    "pinned",
		})
	}
	return out
}
