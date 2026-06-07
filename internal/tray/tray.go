package tray

import "context"

type State struct {
	Tooltip  string
	FiveHour string
	SevenDay string
}

type Actions struct {
	OpenDashboard    func()
	OpenFrontend     func()
	OpenSubscription func()
	Quit             func()
}

type App interface {
	Run(context.Context, Actions) error
	Update(State)
	Notify(title, message string) error
}
