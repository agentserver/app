//go:build !windows && !darwin

package tray

import "context"

type noopApp struct{}

func New(iconPath string) App { return noopApp{} }

func (noopApp) Run(ctx context.Context, actions Actions) error {
	<-ctx.Done()
	return ctx.Err()
}

func (noopApp) Update(State) {}

func (noopApp) Notify(string, string) error { return nil }
