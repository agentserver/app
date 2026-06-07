package codexdesktop

import (
	"context"
	"net/url"

	"github.com/agentserver/agentserver-pkg/internal/browser"
)

type Opener func(string) error

func ThreadURL(folder string) string {
	if folder == "" {
		return "codex://threads/new"
	}
	q := url.Values{}
	q.Set("path", folder)
	return "codex://threads/new?" + q.Encode()
}

func Launch(ctx context.Context, folder string, opener Opener) error {
	_ = ctx
	if opener == nil {
		opener = browser.Open
	}
	return opener(ThreadURL(folder))
}
