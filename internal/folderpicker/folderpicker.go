package folderpicker

import "context"

func Select(ctx context.Context) (string, error) {
	return selectFolder(ctx)
}
