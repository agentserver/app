//go:build !windows

package vscode

import "context"

func validateBootstrapperSignature(context.Context, string) error {
	return nil
}
