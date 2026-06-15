//go:build darwin

package updater

// DefaultManifestURLForPlatform returns the macOS update manifest URL. Requires
// a server-side macos/latest.json (version/url/sha256/size; url → universal dmg).
func DefaultManifestURLForPlatform() string {
	return "https://assets.agent.cs.ac.cn/agentserver-app/macos/latest.json"
}
