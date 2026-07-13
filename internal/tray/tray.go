package tray

import (
	"context"
	"strings"

	"github.com/agentserver/agentserver-pkg/internal/codexdesktop"
)

type State struct {
	Tooltip           string
	FiveHour          string
	SevenDay          string
	OpenFrontendLabel string
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

func openFrontendMenuLabel(st State) string {
	if label := strings.TrimSpace(st.OpenFrontendLabel); label != "" {
		return label
	}
	return "启动 " + codexdesktop.ShortDisplayName
}
