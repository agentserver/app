// Package browser opens URLs in the user's default browser.
package browser

func Open(url string) error { return openPlatform(url) }
