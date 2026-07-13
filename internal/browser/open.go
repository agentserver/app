// Package browser opens URLs in the user's default browser.
package browser

import "context"

func Open(url string) error { return OpenContext(context.Background(), url) }

func OpenContext(ctx context.Context, url string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return openPlatform(ctx, url)
}
