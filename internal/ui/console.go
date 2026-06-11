package ui

import (
	"context"
	"errors"

	"github.com/agentserver/agentserver-pkg/internal/console"
	"github.com/agentserver/agentserver-pkg/internal/slave"
)

type ConsoleController interface {
	State(context.Context) (console.State, error)
	Refresh(context.Context) (console.State, error)
	Slaves(context.Context) (slave.Machine, []slave.Slave, error)
	CreateSlave(context.Context, slave.CreateInput) (slave.Slave, error)
	SelectFolder(context.Context) (string, error)
	RestartSlave(context.Context, string) (slave.Slave, error)
	PauseSlave(context.Context, string) (slave.Slave, error)
	OpenSlaveRemote(context.Context, string) (console.SlaveRemoteOpenResult, error)
	DeleteSlave(context.Context, string) error
	Healthy(context.Context) bool
	OpenFrontend(context.Context) error
	OpenSubscription(context.Context) error
	LogoutModelserver(context.Context) error
	Quit(context.Context) error
}

type noopConsoleController struct{}

func (noopConsoleController) State(context.Context) (console.State, error) {
	return console.State{}, nil
}
func (noopConsoleController) Refresh(context.Context) (console.State, error) {
	return console.State{}, nil
}
func (noopConsoleController) Slaves(context.Context) (slave.Machine, []slave.Slave, error) {
	return slave.Machine{}, nil, errors.New("console: slave manager unavailable")
}
func (noopConsoleController) CreateSlave(context.Context, slave.CreateInput) (slave.Slave, error) {
	return slave.Slave{}, errors.New("console: slave manager unavailable")
}
func (noopConsoleController) SelectFolder(context.Context) (string, error) {
	return "", errors.New("console: folder picker unavailable")
}
func (noopConsoleController) RestartSlave(context.Context, string) (slave.Slave, error) {
	return slave.Slave{}, errors.New("console: slave manager unavailable")
}
func (noopConsoleController) PauseSlave(context.Context, string) (slave.Slave, error) {
	return slave.Slave{}, errors.New("console: slave manager unavailable")
}
func (noopConsoleController) OpenSlaveRemote(context.Context, string) (console.SlaveRemoteOpenResult, error) {
	return console.SlaveRemoteOpenResult{}, errors.New("console: slave manager unavailable")
}
func (noopConsoleController) DeleteSlave(context.Context, string) error {
	return errors.New("console: slave manager unavailable")
}
func (noopConsoleController) Healthy(context.Context) bool           { return false }
func (noopConsoleController) OpenFrontend(context.Context) error     { return nil }
func (noopConsoleController) OpenSubscription(context.Context) error { return nil }
func (noopConsoleController) LogoutModelserver(context.Context) error {
	return nil
}
func (noopConsoleController) Quit(context.Context) error { return nil }
