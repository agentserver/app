//go:build !windows && !darwin

package vscode

import "context"

func validateBootstrapperSignature(context.Context, string) error {
	return nil
}
