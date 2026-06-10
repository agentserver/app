package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentserver/agentserver-pkg/internal/branding"
	"github.com/agentserver/agentserver-pkg/internal/paths"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/uninstall"
)

func main() {
	silent := flag.Bool("silent", false, "no prompts")
	keepInstallDir := flag.Bool("keep-install-dir", false, "do not remove the application install directory")
	flag.Parse()

	if !*silent && !confirm() {
		fmt.Println("已取消。")
		return
	}

	p, err := paths.Default()
	if err != nil {
		die(err)
	}
	appDir := ""
	if exe, err := os.Executable(); err == nil {
		appDir = filepath.Dir(exe)
	}
	if err := uninstall.Run(uninstall.Options{
		Paths:   p,
		Secrets: secrets.New(p.SecretsFile),
		AppDir:  appDir,
	}); err != nil {
		die(err)
	}

	if !*keepInstallDir {
		if appDir != "" {
			if err := removeInstallDirLater(appDir); err != nil {
				die(err)
			}
		}
	}
	fmt.Println("卸载已完成。")
}

func confirm() bool {
	fmt.Printf("这将卸载 %s，并删除本项目的快捷方式、右键菜单、状态和凭据。继续？[y/N] ", branding.DisplayName)
	var ans string
	_, _ = fmt.Scanln(&ans)
	return ans == "y" || ans == "Y"
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "卸载失败:", err)
	os.Exit(1)
}
