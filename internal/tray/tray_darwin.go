//go:build darwin

package tray

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"fyne.io/systray"
)

type darwinApp struct {
	iconPath string

	mu    sync.Mutex
	items *menuItems
}

type menuItems struct {
	dashboard    *systray.MenuItem
	frontend     *systray.MenuItem
	subscription *systray.MenuItem
	fiveHour     *systray.MenuItem
	sevenDay     *systray.MenuItem
	quit         *systray.MenuItem
}

func New(iconPath string) App { return &darwinApp{iconPath: iconPath} }

func (a *darwinApp) Run(ctx context.Context, actions Actions) error {
	go func() {
		<-ctx.Done()
		systray.Quit()
	}()
	onReady := func() { a.buildMenu(actions) }
	systray.Run(onReady, func() {})
	return ctx.Err()
}

func (a *darwinApp) buildMenu(actions Actions) {
	if b := a.templateIconBytes(); b != nil {
		systray.SetTemplateIcon(b, b)
	} else if b := a.iconBytes("icon.png"); b != nil {
		systray.SetIcon(b)
	}
	systray.SetTitle("")
	systray.SetTooltip("星池指挥官")

	it := &menuItems{}
	it.dashboard = systray.AddMenuItem("打开控制台", "打开本地控制台")
	it.frontend = systray.AddMenuItem("启动 Codex Desktop", "启动前端")
	it.subscription = systray.AddMenuItem("打开订阅页", "打开订阅管理")
	systray.AddSeparator()
	it.fiveHour = systray.AddMenuItem("近 5 小时额度：—", "")
	it.fiveHour.Disable()
	it.sevenDay = systray.AddMenuItem("近 7 天额度：—", "")
	it.sevenDay.Disable()
	systray.AddSeparator()
	it.quit = systray.AddMenuItem("退出星池指挥官", "退出")

	a.mu.Lock()
	a.items = it
	a.mu.Unlock()

	go a.handleClicks(it, actions)
}

func (a *darwinApp) handleClicks(it *menuItems, actions Actions) {
	for {
		select {
		case <-it.dashboard.ClickedCh:
			if actions.OpenDashboard != nil {
				actions.OpenDashboard()
			}
		case <-it.frontend.ClickedCh:
			if actions.OpenFrontend != nil {
				actions.OpenFrontend()
			}
		case <-it.subscription.ClickedCh:
			if actions.OpenSubscription != nil {
				actions.OpenSubscription()
			}
		case <-it.quit.ClickedCh:
			if actions.Quit != nil {
				actions.Quit()
			}
			systray.Quit()
			return
		}
	}
}

func (a *darwinApp) Update(st State) {
	a.mu.Lock()
	it := a.items
	a.mu.Unlock()
	if it == nil {
		return
	}
	if st.Tooltip != "" {
		systray.SetTooltip(st.Tooltip)
	}
	if st.FiveHour != "" {
		it.fiveHour.SetTitle("近 5 小时额度：" + st.FiveHour)
	}
	if st.SevenDay != "" {
		it.sevenDay.SetTitle("近 7 天额度：" + st.SevenDay)
	}
}

func (a *darwinApp) Notify(title, message string) error {
	script := fmt.Sprintf(`display notification %q with title %q sound name "Glass"`, message, title)
	return exec.Command("osascript", "-e", script).Run()
}

func (a *darwinApp) templateIconBytes() []byte {
	return a.iconBytes("icon-template.png")
}

func (a *darwinApp) iconBytes(name string) []byte {
	candidates := []string{
		filepath.Join(filepath.Dir(a.iconPath), name),
		filepath.Join(filepath.Dir(a.iconPath), "..", "Resources", name),
	}
	for _, p := range candidates {
		if b, err := os.ReadFile(p); err == nil {
			return b
		}
	}
	return nil
}
