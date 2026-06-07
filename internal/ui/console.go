package ui

import (
	"context"

	"github.com/agentserver/agentserver-pkg/internal/console"
)

type ConsoleController interface {
	State(context.Context) (console.State, error)
	Refresh(context.Context) (console.State, error)
	OpenFrontend(context.Context) error
	OpenSubscription(context.Context) error
	Quit(context.Context) error
}

type noopConsoleController struct{}

func (noopConsoleController) State(context.Context) (console.State, error) {
	return console.State{}, nil
}
func (noopConsoleController) Refresh(context.Context) (console.State, error) {
	return console.State{}, nil
}
func (noopConsoleController) OpenFrontend(context.Context) error     { return nil }
func (noopConsoleController) OpenSubscription(context.Context) error { return nil }
func (noopConsoleController) Quit(context.Context) error             { return nil }
