//go:build !windows

package codexdesktop

import (
	"context"

	"github.com/agentserver/agentserver-pkg/internal/browser"
)

var openOtherProtocol = browser.OpenContext

func Launch(ctx context.Context, folder string) error {
	return openOtherProtocol(ctx, ThreadURL(folder))
}
