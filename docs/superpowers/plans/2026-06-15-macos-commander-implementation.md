# 星池指挥官 macOS 版本实现计划 (Implementation Plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 Windows 版「星池指挥官」桌面体验完整移植到 macOS（universal binary，DMG 分发），复用现有跨平台运行时，只补 darwin 平台层 + 打包。

**Architecture:** Windows 运行时本就跨平台（本地控制台是 `127.0.0.1` 上的 Vue Web 仪表盘，IPC 全走 loopback HTTP + 文件锁）。移植集中在三处：(1) 把落到 `*_other.go`/`*_unix.go` 桩里的平台函数补上真正的 darwin 实现；(2) 兄弟二进制去 `.exe` 命名 + install-mode 可写路径；(3) `.app` bundle + DMG + Finder Quick Action + universal 构建。菜单栏用 `fyne.io/systray`（CGo，Cocoa），其余原生能力用 macOS 自带命令行（`osascript`/`ps`/`defaults`/`codesign`/`spctl`/`hdiutil`），避免手写 CGo。

**Tech Stack:** Go（CGO_ENABLED=1 on darwin），`fyne.io/systray`（菜单栏），macOS 原生命令行工具，Vue 前端（`go:embed`，复用），Info.plist + Automator `.workflow`，DMG（`hdiutil`）。

**Spec:** `docs/superpowers/specs/2026-06-15-macos-commander-design.md`（本计划是其细化实现，章节号一一对应）。

---

## 全局实现约定（每个任务都遵守）

**1. darwin 平台三件套（build-tag triage）。** 对每个需要 darwin 专属行为的函数：

- 新增 `*_darwin.go`，首行 `//go:build darwin`。
- 把现有 `*_other.go`（`//go:build !windows`）**收窄**为 `//go:build !windows && !darwin`，使其只作为 linux/其余平台回退。
- 凡含 `runtime.GOOS == "linux"` 分支的共享函数，拆成 linux 专属（`//go:build linux`）+ darwin 专属（`//go:build darwin`）+ 通用回退（`//go:build !windows && !linux && !darwin` 或合并进 darwin/linux 之外）。

**2. 约束：Windows 与 Linux headless 零改动。** 所有共享包的平台分派以「不影响 Windows/Linux」为约束。每个改动 Windows/Linux 行为的任务**必须**附带回归保护（见测试策略）。

**3. 兄弟二进制命名。** 所有 `joinExe(dir, "X.exe")` 改为 `joinExe(dir, process.ExeName("X"))`（见 Phase 2）。跨平台资源（`.vsix`/`.tar.gz`/`codex-manifest*.json`）命名不变。

**4. 可写数据位置不变。** 全部仍放 `~/.agentserver-app/`（`state.json`、`secrets.json`、`console-port.json`、`bin-root/bin/codex`、`proxy-token`、`token-refresher.lock`、`logs/`、`vscode-data/`、`vscode-extensions/`、`install-mode.json`），与 Windows/Linux 一致。`.app` bundle 签名后只读，所有可写状态都在 bundle 外。

**5. 提交节奏。** 每个任务结束提交一次，提交信息以 `feat(macos):`/`fix(macos):`/`build(macos):`/`docs(macos):` 开头。

---

## 测试策略（对应 spec §7）

**Linux（本机，CI 可跑）能验证的：**

- 纯 Go 逻辑单测：`go test -race -count=1 ./...`（当前基线：27 包全过，必须保持全绿）。
- darwin 平台逻辑的可测性设计：**把解析/判定逻辑抽到共享（无构建标签）文件、把 OS 调用薄壳放进 `_darwin.go`，并注入「命令执行器」接口**，使共享逻辑的 `*_test.go` 在 linux 上可跑。凡平台函数依赖外部命令（`ps`/`osascript`/`codesign`/`defaults`/`launchctl`），都用一个 `type cmdRunner func(ctx, name, args...) ([]byte, error)` 接口参数化。
- 交叉编译检查：`GOOS=darwin GOARCH=arm64 go vet ./...` 与 `GOOS=darwin GOARCH=arm64 go build ./...`（非 CGo 部分在 linux 可验证编译；CGo 部分 `tray_darwin.go` 只在 Mac 上验证，故其任务标注「Mac-only」）。

**仅 Mac 上验证的：** universal `.app` 与 DMG 构建、onboarding 全流程、菜单栏、Quick Action、两种前端模式、本机 slave 增删停、token refresher 守护、自更新替换、卸载。这些任务标注「Mac-only」，给出确切命令与期望现象，不在 linux 跑单测。

**当前测试基线命令（改动前/后都跑，确保不回归）：**
```bash
go test -race -count=1 ./...
GOOS=darwin GOARCH=arm64 go vet ./...
```

---

## 文件结构（Create / Modify 全景图）

**新增文件（Create）：**

打包/构建：
- `packaging/macos/Info.plist` — bundle 描述（CFBundleName=星池指挥官, CFBundleExecutable=launcher, CFBundleIconFile=icon.icns, LSMinimumSystemVersion=11.0, CFBundleIdentifier=cn.cs.agentserver.app, AppleEvents usage description）。
- `packaging/macos/icon.icns` — 由 `image/icon.png` 经 `iconutil` 生成（Mac-only）。
- `packaging/macos/icon-template.png` — 菜单栏 template image（黑+透明，~22px）。
- `packaging/macos/icon.png` — 菜单栏普通图标（彩色）。
- `packaging/macos/codex-manifest-darwin-arm64.json` — codex 运行时 manifest（mirror linux 结构，darwin-arm64 vendor 前缀）。
- `packaging/macos/codex-manifest-darwin-amd64.json` — 同上 amd64。
- `packaging/macos/open-folder-action.workflow`（或 `用星池指挥官打开.workflow`）— Finder Quick Action 模板（Automator，接收文件或文件夹，运行 shell 调 `open-folder`）。
- `packaging/macos/LICENSE.zh.txt` — 复用 windows 版。
- `scripts/package-macos.sh` — 分阶段打包脚本（mirror `package-linux.sh` 风格）。

Go 源（平台层）：
- `internal/process/exename.go` + `exename_test.go` — `ExeName(name)` 兄弟命名 helper。
- `cmd/launcher/iconpath_darwin.go`（`//go:build darwin`）+ `iconpath_other.go`（`//go:build !darwin`）— `preferredIconPath` 的平台分派（`.icns` vs `.ico`）。
- `cmd/launcher/runapp_darwin.go`（`//go:build darwin`）+ `runapp_other.go`（`//go:build !darwin`）— `runMainLoop` 平台分派（tray 主线程 vs server 主线程）。
- `internal/console/instance_process_linux.go`（`//go:build linux`）— `/proc/<pid>/status` Uid 归属。
- `internal/console/instance_process_darwin.go`（`//go:build darwin`）— `ps -o uid=` 归属。
- `internal/console/process_exec.go`（无标签）+ `process_exec_test.go`— 命令执行器接口 + 共享判定逻辑（linux 可测）。
- `internal/slave/process_liveness_linux.go`（`//go:build linux`）— `/proc/<pid>/exe` 核验。
- `internal/slave/process_liveness_darwin.go`（`//go:build darwin`）— `ps -o comm=` 核验。
- `internal/slave/process_liveness_shared.go`（无标签）+ `process_liveness_shared_test.go`— 共享判定逻辑（linux 可测）。
- `internal/env/persist_darwin.go`（`//go:build darwin`）— `launchctl setenv` + rc 受管块。
- `internal/folderpicker/folderpicker_darwin.go`（`//go:build darwin`）— `osascript choose folder`。
- `internal/shortcut/shortcut_darwin.go`（`//go:build darwin`）— Finder alias + Quick Action 安装/卸载。
- `internal/vscode/detect_darwin.go`（`//go:build darwin`）— `/Applications/Visual Studio Code.app/...` + `defaults` 版本。
- `internal/vscode/install_darwin.go`（`//go:build darwin`）— `unzip -o` 到 `/Applications` + `codesign`/`spctl`/`xattr`。
- `internal/vscode/install_authenticode_darwin.go`（`//go:build darwin`）— `codesign --verify` + `spctl`。
- `internal/codexdesktop/detect_darwin.go`（`//go:build darwin`）— `Codex.app` Info.plist + `codex://` scheme。
- `internal/codexdesktop/install_darwin.go`（`//go:build darwin`）— dmg/zip 下载安装 + 校验。
- `internal/updater/installer_darwin.go`（`//go:build darwin`）— zip/dmg 解压替换。
- `internal/updater/replace_darwin.go`（`//go:build darwin`）— `.app` 目录 rename-old → 入位 → 重启。
- `internal/uninstall/process_stop_darwin.go`（`//go:build darwin`）— `ps`/`pgrep` 枚举 + SIGKILL + 轮询。
- `internal/tray/tray_darwin.go`（`//go:build darwin`，**Mac-only CGo**）— `fyne.io/systray` 实现。

文档：
- `README.md`（或项目描述文档）macOS 章节、打包/签名说明（见 Phase 7）。

**修改文件（Modify）：**

- `Makefile` — 新增 `cross-darwin`/`macos-icon`/`package-macos` 目标。
- `go.mod` / `go.sum` — 新增 `fyne.io/systray`（CGo 依赖）。
- `cmd/launcher/main.go` — `joinExe("X.exe")` → `process.ExeName("X")`；`preferredIconPath` 平台化；serve 路径改用 `runMainLoop`（§5.1）。
- `cmd/open-folder/main.go` — 兄弟二进制命名去 `.exe`（如 `launcher.exe` → `process.ExeName("launcher")`）。
- `cmd/agentctl`（若有 `--manifest` 路径推算）— darwin 用 `os.Executable()` 推算 manifest 路径。
- `internal/console/instance_process_unix.go` → 收窄 + 拆分（删除内联 linux 分支，留通用回退）。
- `internal/slave/process_liveness_unix.go` → 收窄 + 拆分。
- `internal/slave/process_liveness_darwin_test.go` → 改为「强核验」断言（不再信任任意活 PID）。
- `internal/env/persist_other.go` → 收窄为 `!windows && !darwin`。
- `internal/folderpicker/folderpicker_other.go` → 收窄。
- `internal/shortcut/shortcut_other.go` → 收窄。
- `internal/vscode/detect_other.go`/`install_other.go`/`install_authenticode_other.go` → 收窄。
- `internal/vscode/install.go` — `planInstallFor` 加 `case "darwin"`（消除 panic）。
- `internal/vscode/settings.go` — 补 `osx` terminal profile（zsh -l）+ `defaultProfile.osx`。
- `internal/vscode/install.go` `validateBootstrapperFile` — darwin zip 走 zip 魔数分支。
- `internal/codexdesktop/detect_other.go`/`install.go`/`winget.go` → `EnsureInstalled` 平台分派（darwin 走 darwin 安装）。
- `internal/updater/service.go` — manifest URL 平台化（darwin 用 macos/latest.json）；`installerCachePath` 扩展名平台化。
- `internal/updater/installer_other.go`/`replace_other.go` → 收窄。
- `internal/uninstall/process_stop_other.go`/`registry_other.go` → 收窄（registry 保持 no-op）。
- `internal/installmode/installmode.go` — 新增可写路径入口（darwin 用 `paths.InstallRoot`）。
- `internal/codex/config.go` — `[windows] sandbox` 仅 windows 写（§4.14）。
- `internal/tray/tray_other.go` → 收窄为 `!windows && !darwin`（macOS 走 `tray_darwin.go`）。

---

## Phase 1：脚手架与构建（先能产出可运行的 `.app`，平台层可暂时是桩）

> 对应 spec §6、§8.1。目标：在 Mac 上跑 `make package-macos` 能产出可拖拽的 `星池指挥官.app` + DMG。Linux 上只验证 Go 交叉编译（非 CGo 部分编译通过）。

### Task 1.1：新增 `packaging/macos/` 目录骨架与 Info.plist

**Files:**
- Create: `packaging/macos/Info.plist`
- Create: `packaging/macos/LICENSE.zh.txt`（从 `packaging/windows/LICENSE.zh.txt` 复制）

- [ ] **Step 1: 创建 `packaging/macos/Info.plist`**

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key>
    <string>星池指挥官</string>
    <key>CFBundleDisplayName</key>
    <string>星池指挥官</string>
    <key>CFBundleIdentifier</key>
    <string>cn.cs.agentserver.app</string>
    <key>CFBundleVersion</key>
    <string>1</string>
    <key>CFBundleShortVersionString</key>
    <string>1.0.0</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleExecutable</key>
    <string>launcher</string>
    <key>CFBundleIconFile</key>
    <string>icon.icns</string>
    <key>LSMinimumSystemVersion</key>
    <string>11.0</string>
    <key>LSUIElement</key>
    <true/>
    <key>NSAppleEventsUsageDescription</key>
    <string>星池指挥官需要使用 AppleEvents 来选择文件夹、创建桌面快捷方式与显示 Finder 右键菜单。</string>
    <key>NSAppleScriptEnabled</key>
    <true/>
    <key>CFBundleURLTypes</key>
    <array>
        <dict>
            <key>CFBundleURLName</key>
            <string>cn.cs.agentserver.app</string>
            <key>CFBundleURLSchemes</key>
            <array>
                <string>codex</string>
            </array>
        </dict>
    </array>
</dict>
</plist>
```

说明：`LSUIElement=true` 使应用不显示在 Dock（常驻菜单栏型应用），与 spec §1「常驻菜单栏」一致。版本号 `1.0.0` 为占位，打包脚本从 `appversion.Version` 注入（见 Task 1.5）。

- [ ] **Step 2: 复制 LICENSE**

```bash
cp packaging/windows/LICENSE.zh.txt packaging/macos/LICENSE.zh.txt
```

- [ ] **Step 3: Commit**

```bash
git add packaging/macos/Info.plist packaging/macos/LICENSE.zh.txt
git commit -m "build(macos): add Info.plist and license for .app bundle"
```

### Task 1.2：图标资源与 `macos-icon` 生成（Mac-only）

**Files:**
- Create: `packaging/macos/icon.icns`（生成物，不入库或入库均可；本任务用脚本生成）
- Create: `packaging/macos/icon.png`、`packaging/macos/icon-template.png`（源资源，入库）
- Source: `image/icon.png`（已存在的彩色源图）

- [ ] **Step 1: 准备菜单栏图标资源**

把彩色图标拷为 `icon.png`；为菜单栏 template image 制作黑+透明单色图（约 22×22 / 44×44 @2x）。template image 要求：纯黑像素 + alpha 透明，macOS 会按深浅色菜单栏自动着色。

```bash
# 在 Mac 上（需要 sips 或手动导出）
cp image/icon.png packaging/macos/icon.png
# icon-template.png 需用图像工具转成「黑色形状 + alpha 透明」。
# 若暂无工具，先用占位（Phase 5 菜单栏任务会回退到 icon.png，不阻塞构建）：
cp image/icon.png packaging/macos/icon-template.png
```

- [ ] **Step 2: 新增 `scripts/make-icns.sh`（Mac-only，由 Makefile 调用）**

```bash
#!/usr/bin/env bash
# 把 image/icon.png 转成 packaging/macos/icon.icns（需 macOS iconutil）。
set -euo pipefail
SRC="${1:-image/icon.png}"
OUT="packaging/macos/icon.icns"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
ICONSET="$TMP/icon.iconset"
mkdir -p "$ICONSET"
for size in 16 32 64 128 256 512 1024; do
  half=$((size / 2))
  sips -z "$size" "$size" "$SRC" --out "$ICONSET/icon_${half}x${half}.png" >/dev/null
  sips -z "$half" "$half" "$SRC" --out "$ICONSET/icon_${half}x${half}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$OUT"
echo "wrote $OUT"
```

```bash
chmod +x scripts/make-icns.sh
```

- [ ] **Step 3: 验证生成（Mac-only）**

Run: `bash scripts/make-icns.sh`
Expected: 输出 `wrote packaging/macos/icon.icns`，`file packaging/macos/icon.icns` 显示 `Mac OS X icon`。

- [ ] **Step 4: Commit**

```bash
git add scripts/make-icns.sh packaging/macos/icon.png packaging/macos/icon-template.png
git commit -m "build(macos): add icon assets and icns generator"
```

### Task 1.3：新增 darwin codex 运行时 manifest（mirror linux 结构）

**Files:**
- Create: `packaging/macos/codex-manifest-darwin-arm64.json`
- Create: `packaging/macos/codex-manifest-darwin-amd64.json`
- Reference: `packaging/linux/codex-manifest-linux-arm64.json`（结构来源）

- [ ] **Step 1: 创建 `packaging/macos/codex-manifest-darwin-arm64.json`**

> **填充说明（非占位）：** `pinned_version` / `integrity` / `shasum` / `urls` 必须从实际 pin 的 `@openai/codex` darwin-arm64 包计算。复用现有 pin 流程：对目标版本运行 `npm pack @openai/codex@<ver>`（或镜像下载），用仓库现有 pinning 脚本生成 `integrity`（sha512-base64，与 npm `integrity` 字段一致）与 `shasum`（sha1-hex，git blob sha1，与 `npm view ... .dist.shasum` 一致）。下方字段结构是确定的，仅哈希值待填。

```json
{
  "package": "@openai/codex",
  "platform": "darwin-arm64",
  "pinned_version": "<FILL: e.g. 0.139.0-darwin-arm64>",
  "strip_prefix": "vendor/aarch64-apple-darwin/",
  "codex_exe": "bin/codex",
  "required_files": [
    "bin/codex",
    "codex-path/rg"
  ],
  "pinned": {
    "integrity": "<FILL: sha512-... base64>",
    "shasum": "<FILL: 40-hex git blob sha1>",
    "urls": [
      "https://registry.npmmirror.com/@openai/codex/-/codex-<ver>.tgz",
      "https://npmreg.proxy.ustclug.org/@openai/codex/-/codex-<ver>.tgz",
      "https://registry.npmjs.org/@openai/codex/-/codex-<ver>.tgz"
    ]
  }
}
```

校验：`strip_prefix` 对应 npm 包内 `vendor/aarch64-apple-darwin/`（arm64）；`codex_exe` 为解 strip 后的相对路径 `bin/codex`（与 linux 一致，不带 `.exe`）。`required_files` 至少含 `bin/codex` 与 `codex-path/rg`；若该版本 darwin 包含其它必需文件（如 sandbox helper），按实际追加。

- [ ] **Step 2: 创建 `packaging/macos/codex-manifest-darwin-amd64.json`**

同上，`platform`=`darwin-amd64`，`strip_prefix`=`vendor/x86_64-apple-darwin/`，哈希/版本为 amd64 包的。

- [ ] **Step 3: 用 codexruntime 校验结构（Linux 可跑）**

写一个一次性校验（不必入库）：

```go
// 临时文件 /tmp/check_manifest.go
package main

import (
	"fmt"
	"github.com/agentserver/agentserver-pkg/internal/codexruntime"
)

func main() {
	for _, p := range []string{
		"packaging/macos/codex-manifest-darwin-arm64.json",
		"packaging/macos/codex-manifest-darwin-amd64.json",
	} {
		m, err := codexruntime.LoadManifest(p)
		fmt.Printf("%s: err=%v platform=%s strip=%s\n", p, err, m.Platform, m.StripPrefix)
	}
}
```

Run: `go run /tmp/check_manifest.go`
Expected: 两个文件都 `err=<nil>`（哈希填好后）。若哈希未填，`Validate` 会报 `pinned.integrity` / `pinned.urls` 缺失——这是预期的，提示需填实际值。

- [ ] **Step 4: Commit**

```bash
git add packaging/macos/codex-manifest-darwin-*.json
git commit -m "build(macos): add darwin codex runtime manifests"
```

### Task 1.4：Finder Quick Action 模板（`.workflow`）

**Files:**
- Create: `packaging/macos/用星池指挥官打开.workflow`（Automator document bundle）

- [ ] **Step 1: 在 Mac 上用 Automator 制作模板**

打开「自动操作」→ 新建「快速操作」→ 配置：
- 工作流程接收当前：「文件或文件夹」（位于：「访达」）。
- 添加「运行 Shell 脚本」动作：Shell=/bin/zsh，传递输入「作为自变量」，内容：

```bash
# agentserver-managed:open-folder
APP="/Applications/星池指挥官.app"
OPEN_FOLDER="$APP/Contents/MacOS/open-folder"
for f in "$@"; do
  "$OPEN_FOLDER" "$f"
done
```

> 注：`/Applications/星池指挥官.app` 路径在打包期/首启时需可被改写（非 `/Applications` 安装则用 `os.Executable()` 推算）。模板里先写默认 `/Applications`，安装期由 `shortcut_darwin.go` 在拷到 `~/Library/Services/` 前按实际安装位置替换（见 Phase 4 shortcut 任务）。占位用字面量字符串以便脚本 sed 替换。

导出为 `packaging/macos/用星池指挥官打开.workflow`。

- [ ] **Step 2: 验证 workflow 结构**

Run: `ls -R "packaging/macos/用星池指挥官打开.workflow"`
Expected: 含 `Contents/Info.plist` 与 `Contents/document.wflow`。

- [ ] **Step 3: Commit**

```bash
git add "packaging/macos/用星池指挥官打开.workflow"
git commit -m "build(macos): add Finder Quick Action workflow template"
```

### Task 1.5：`scripts/package-macos.sh`（Mac-only，分阶段）

**Files:**
- Create: `scripts/package-macos.sh`

- [ ] **Step 1: 创建脚本**

```bash
#!/usr/bin/env bash
# 组装 星池指挥官.app 并生成 DMG。仅在 macOS 上运行（依赖 CGo + iconutil/hdiutil/codesign）。
set -euo pipefail

VERSION="${VERSION:-1.0.0}"
APP_NAME="星池指挥官"
APP_INTERNAL="星池指挥官.app"
STAGE="dist/macos/stage"
DMG="dist/macos/${APP_NAME}-${VERSION}-universal.dmg"
MACOS_DIR="${STAGE}/${APP_INTERNAL}/Contents/MacOS"
RES_DIR="${STAGE}/${APP_INTERNAL}/Contents/Resources"

echo "==> [1/8] build universal binaries"
mkdir -p dist/macos/bin
for cmd in launcher open-folder token-refresher agentctl uninstall; do
  echo "  building $cmd (universal)"
  GOARCH=arm64 CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o "dist/macos/bin/${cmd}.arm64" ./cmd/$cmd
  GOARCH=amd64 CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o "dist/macos/bin/${cmd}.amd64" ./cmd/$cmd
  lipo -create -output "dist/macos/bin/$cmd" "dist/macos/bin/${cmd}.arm64" "dist/macos/bin/${cmd}.amd64"
  rm "dist/macos/bin/${cmd}.arm64" "dist/macos/bin/${cmd}.amd64"
done

echo "==> [2/8] fetch driver-agent / slave-agent (darwin, lipo universal)"
# 复用 scripts/linux-package-common.sh 的 SHA 校验思路下载 Loom darwin 二进制并 lipo。
# 具体 SHA 由 Loom v0.0.5 darwin assets 提供（与 linux 流程镜像）。
bash scripts/fetch-loom-darwin.sh   # 见 Step 2

echo "==> [3/8] assemble .app layout"
rm -rf "${STAGE}"
mkdir -p "${MACOS_DIR}" "${RES_DIR}"
install -m 0755 dist/macos/bin/{launcher,open-folder,token-refresher,agentctl,uninstall} "${MACOS_DIR}/"
install -m 0755 dist/macos/bin/{driver-agent,slave-agent} "${MACOS_DIR}/"
cp packaging/macos/Info.plist "${STAGE}/${APP_INTERNAL}/Contents/Info.plist"
cp packaging/macos/icon.icns "${RES_DIR}/icon.icns"
cp packaging/macos/icon.png "${RES_DIR}/icon.png"
cp packaging/macos/icon-template.png "${RES_DIR}/icon-template.png"
cp packaging/macos/LICENSE.zh.txt "${RES_DIR}/"
cp packaging/macos/codex-manifest-darwin-arm64.json "${RES_DIR}/"
cp packaging/macos/codex-manifest-darwin-amd64.json "${RES_DIR}/"
# 复用 windows 包里的 vsix 与跨平台 skills tarball（构建期产物，由 ext-build / linux 流程产出）
install -m 0644 dist/agentserver-app.vsix "${RES_DIR}/agentserver-app.vsix" || true
install -m 0644 dist/cache/loom/v0.0.5/driver-skills.tar.gz "${RES_DIR}/" || true
install -m 0644 dist/cache/loom/v0.0.5/driver-superpower-skills.tar.gz "${RES_DIR}/" || true
install -m 0644 dist/cache/loom/v0.0.5/driver-codex-prompts.tar.gz "${RES_DIR}/" || true
cp -R "packaging/macos/用星池指挥官打开.workflow" "${RES_DIR}/"

echo "==> [4/8] write initial install-mode.json"
printf '{"frontend_mode":"codex_desktop"}\n' > "${MACOS_DIR}/install-mode.json"

echo "==> [5/8] sign (ad-hoc by default; set MACOS_SIGN_IDENTITY for Developer ID)"
if [[ -n "${MACOS_SIGN_IDENTITY:-}" ]]; then
  codesign --deep --force --options runtime --sign "$MACOS_SIGN_IDENTITY" "${STAGE}/${APP_INTERNAL}"
  xcrun notarytool submit "${STAGE}/${APP_INTERNAL}" --keychain-profile "${MACOS_NOTARY_PROFILE:-}" --wait || true
  xcrun stapler staple "${STAGE}/${APP_INTERNAL}" || true
else
  codesign --deep --force --sign - "${STAGE}/${APP_INTERNAL}"
fi

echo "==> [6/8] build DMG (drag-to-Applications layout)"
mkdir -p dist/macos/dmg
cp -R "${STAGE}/${APP_INTERNAL}" dist/macos/dmg/
ln -sf /Applications dist/macos/dmg/Applications
rm -f "${DMG}"
hdiutil create -volname "${APP_NAME}" -srcfolder dist/macos/dmg -fs HFS+ -format UDZO "${DMG}"
if [[ -n "${MACOS_SIGN_IDENTITY:-}" ]]; then
  codesign --sign "$MACOS_SIGN_IDENTITY" "${DMG}"
  xcrun notarytool submit "${DMG}" --keychain-profile "${MACOS_NOTARY_PROFILE:-}" --wait || true
  xcrun stapler staple "${DMG}" || true
fi

echo "==> [7/8] sha256 sidecar"
shasum -a 256 "${DMG}" | awk '{print $1}' > "${DMG}.sha256"

echo "==> [8/8] done"
ls -lh "${DMG}" "${DMG}.sha256"
```

- [ ] **Step 2: 创建 `scripts/fetch-loom-darwin.sh`（Loom darwin 二进制下载 + lipo，mirror `linux-package-common.sh` 的 SHA 校验）**

```bash
#!/usr/bin/env bash
# 下载 Loom v0.0.5 的 darwin driver-agent / slave-agent（arm64+amd64），lipo 成 universal，
# 输出到 dist/macos/bin/。SHA256 从 Loom release assets 元数据取（与 linux 流程镜像）。
set -euo pipefail
LOOM_VER="v0.0.5"
BASE="${LOOM_BASE_URL:-https://github.com/agentserver/loom/releases/download}"
OUT="dist/macos/bin"
mkdir -p "$OUT"
for kind in driver-agent slave-agent; do
  for arch in arm64 amd64; do
    url="$BASE/$LOOM_VER/${kind}.darwin-${arch}"
    cache="dist/cache/loom/$LOOM_VER/${kind}.darwin-${arch}"
    [[ -f "$cache" ]] || curl -fL --retry 3 -o "$cache" "$url"
    # SHA 校验（expected 由 release 元数据提供；此处省略具体值，需在拿到 release 后填入并校验）：
    # verify_sha256 "$cache" "$EXPECTED_SHA"
  done
  lipo -create -output "$OUT/$kind" \
    "dist/cache/loom/$LOOM_VER/${kind}.darwin-arm64" \
    "dist/cache/loom/$LOOM_VER/${kind}.darwin-amd64"
done
```

> SHA 校验：复用 `scripts/linux-package-common.sh` 的 `verify_sha256`（source 该脚本即可），在拿到 Loom darwin release 的 SHA 后启用。

- [ ] **Step 3: 赋权**

```bash
chmod +x scripts/package-macos.sh scripts/fetch-loom-darwin.sh
```

- [ ] **Step 4: 验证（Mac-only）**

Run: `make package-macos`
Expected: 产出 `dist/macos/星池指挥官-1.0.0-universal.dmg` + `.sha256`。双击挂载可见 `星池指挥官.app` 与 `/Applications` 软链。

- [ ] **Step 5: Commit**

```bash
git add scripts/package-macos.sh scripts/fetch-loom-darwin.sh
git commit -m "build(macos): add staged .app/DMG packaging scripts"
```

### Task 1.6：Makefile 目标（`cross-darwin` / `macos-icon` / `package-macos`）

**Files:**
- Modify: `Makefile`（在 `package-linux:` 目标后追加）

- [ ] **Step 1: 在 Makefile 追加 macOS 目标**

在 `package-linux` 目标定义之后、`clean:` 之前插入：

```makefile
# ---- macOS ----
# 注：CGo（systray）二进制无法在 Linux 交叉编译；以下目标仅在 macOS 上运行。
cross-darwin: ui-build
	@echo "==> cross-build darwin universal binaries (CGO, run on macOS)"
	@mkdir -p $(DIST)/macos/bin
	@for cmd in launcher open-folder token-refresher agentctl uninstall; do \
	  echo "  ==> $$cmd (arm64+amd64 → lipo)"; \
	  GOARCH=arm64 CGO_ENABLED=1 $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(DIST)/macos/bin/$$cmd.arm64 ./cmd/$$cmd ; \
	  GOARCH=amd64 CGO_ENABLED=1 $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(DIST)/macos/bin/$$cmd.amd64 ./cmd/$$cmd ; \
	  lipo -create -output $(DIST)/macos/bin/$$cmd $(DIST)/macos/bin/$$cmd.arm64 $(DIST)/macos/bin/$$cmd.amd64 ; \
	  rm $(DIST)/macos/bin/$$cmd.arm64 $(DIST)/macos/bin/$$cmd.amd64 ; \
	done

macos-icon:
	@bash scripts/make-icns.sh image/icon.png

package-macos: cross-darwin macos-icon ext-build
	@bash scripts/package-macos.sh
```

- [ ] **Step 2: 更新 `help:` 目标说明（追加三行）**

在 `help:` 的 `@echo` 列表末尾追加：

```makefile
	@echo "make cross-darwin      - build darwin universal binaries (macOS host, CGO)"
	@echo "make macos-icon        - generate packaging/macos/icon.icns from image/icon.png (macOS)"
	@echo "make package-macos     - build 星池指挥官.app + DMG (macOS host; depends on ui-build+ext-build)"
```

并把 `.PHONY` 行补上 `cross-darwin macos-icon package-macos`。

- [ ] **Step 3: 验证 Makefile 语法（Linux 可跑）**

Run: `make -n package-macos`
Expected: 打印出依赖链命令（`cross-darwin macos-icon ext-build` → `bash scripts/package-macos.sh`），无 make 语法错误。

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "build(macos): add cross-darwin / macos-icon / package-macos targets"
```

---

## Phase 2：跨切面 helper（解锁其余改动；对应 spec §4.12、§4.13）

### Task 2.1：兄弟二进制命名 helper `internal/process.ExeName`

**Files:**
- Create: `internal/process/exename.go`
- Test: `internal/process/exename_test.go`

- [ ] **Step 1: 写失败测试**

```go
// internal/process/exename_test.go
package process

import "testing"

func TestExeName(t *testing.T) {
	tests := []struct {
		goos string
		name string
		want string
	}{
		{"windows", "launcher", "launcher.exe"},
		{"windows", "open-folder", "open-folder.exe"},
		{"darwin", "launcher", "launcher"},
		{"linux", "launcher", "launcher"},
	}
	for _, tt := range tests {
		if got := exeNameFor(tt.goos, tt.name); got != tt.want {
			t.Errorf("exeNameFor(%q,%q)=%q want %q", tt.goos, tt.name, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/process/ -run TestExeName -v`
Expected: FAIL（`exeNameFor` 未定义）。

- [ ] **Step 3: 写实现**

```go
// internal/process/exename.go
package process

import "runtime"

// ExeName returns the platform-correct file name for a sibling executable.
// On Windows it appends ".exe"; on macOS/Linux the name is returned unchanged.
// Use this in place of hardcoded "launcher.exe" / "open-folder.exe" etc. so the
// same call sites resolve correctly on all platforms.
func ExeName(name string) string {
	return exeNameFor(runtime.GOOS, name)
}

func exeNameFor(goos, name string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/process/ -run TestExeName -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/process/exename.go internal/process/exename_test.go
git commit -m "feat(process): add ExeName sibling-executable naming helper"
```

### Task 2.2：在 `cmd/launcher` 替换硬编码 `.exe` 兄弟二进制名

**Files:**
- Modify: `cmd/launcher/main.go`（多处 `joinExe(..., "X.exe")`）

> 现状（已确认）：`cmd/launcher/main.go` 中 `joinExe` 私有函数（line 983）；`.exe` 调用点：`token-refresher.exe`（line 251、271、715）、`slave-agent.exe`（line 447）、`driver-agent.exe`（line 570、711、816）、`codex.exe`（line 709）、`launcher.exe`（line 713、718）、`open-folder.exe`（line 714）。跨平台资源 `agentserver-app.vsix`（707）、`codex-manifest.json`（710）、`driver-skills.tar.gz`（868）、`driver-superpower-skills.tar.gz`（869）、`driver-codex-prompts.tar.gz`（870）**不改**。

- [ ] **Step 1: 在 `cmd/launcher/main.go` 顶部 import 块加入 `internal/process`**

确认 import 路径为 `github.com/agentserver/agentserver-pkg/internal/process`。

- [ ] **Step 2: 替换各 `.exe` 调用点**

逐处把字面量改为 `process.ExeName("X")`：

| 原文 | 改为 |
|---|---|
| `joinExe(in.InstallDir, "token-refresher.exe")` | `joinExe(in.InstallDir, process.ExeName("token-refresher"))` |
| `joinExe(in.InstallDir, "slave-agent.exe")` | `joinExe(in.InstallDir, process.ExeName("slave-agent"))` |
| `joinExe(in.InstallDir, "driver-agent.exe")` | `joinExe(in.InstallDir, process.ExeName("driver-agent"))` |
| `joinExe(installDir, "codex.exe")` | `joinExe(installDir, process.ExeName("codex"))` |
| `joinExe(installDir, "launcher.exe")` | `joinExe(installDir, process.ExeName("launcher"))` |
| `joinExe(installDir, "open-folder.exe")` | `joinExe(installDir, process.ExeName("open-folder"))` |

用以下命令核对没有遗漏：

```bash
grep -n '\.exe"' cmd/launcher/main.go
```
Expected: 剩下的 `.exe"` 仅出现在字符串字面量/注释中，**无** `joinExe(..., "...exe")` 形式。

- [ ] **Step 3: 编译验证（Linux 可跑，证明 Windows 逻辑未破坏）**

Run: `go build ./cmd/launcher && go vet ./cmd/launcher`
Expected: 通过。

- [ ] **Step 4: Commit**

```bash
git add cmd/launcher/main.go
git commit -m "refactor(launcher): resolve sibling exe names via process.ExeName"
```

### Task 2.3：在 `cmd/open-folder` 替换硬编码 `.exe` 兄弟二进制名

**Files:**
- Modify: `cmd/open-folder/main.go`

- [ ] **Step 1: 定位 `launcher.exe` 引用**

```bash
grep -n 'launcher.exe\|\.exe' cmd/open-folder/main.go
```

把 `launcher.exe`（及任何其它兄弟 `.exe`）改为 `process.ExeName("launcher")`，import `internal/process`。

- [ ] **Step 2: 编译验证**

Run: `go build ./cmd/open-folder && go vet ./cmd/open-folder`
Expected: 通过。

- [ ] **Step 3: Commit**

```bash
git add cmd/open-folder/main.go
git commit -m "refactor(open-folder): resolve sibling exe names via process.ExeName"
```

### Task 2.4：`preferredIconPath` 平台分派（`.icns` vs `.ico`）

**Files:**
- Modify: `cmd/launcher/main.go`（`preferredIconPath`，line 990）
- Create: `cmd/launcher/iconpath_darwin.go`（`//go:build darwin`）
- Create: `cmd/launcher/iconpath_other.go`（`//go:build !darwin`）

> 现状（已确认）：`preferredIconPath`（line 990）glob `icon-*.ico`，回退 `icon.ico`。macOS 菜单栏/Dock 用 `.icns`，需平台化。

- [ ] **Step 1: 抽出平台相关常量到两个新文件**

`cmd/launcher/iconpath_other.go`：
```go
//go:build !darwin

package main

const (
	iconGlobSuffix    = "icon-*.ico"
	defaultIconSuffix = "icon.ico"
)
```

`cmd/launcher/iconpath_darwin.go`：
```go
//go:build darwin

package main

const (
	iconGlobSuffix    = "icon-*.icns"
	defaultIconSuffix = "icon.icns"
)
```

- [ ] **Step 2: 改写 `preferredIconPath`（main.go line 990）为通用版**

```go
func preferredIconPath(installDir string) string {
	matches, err := filepath.Glob(filepath.Join(installDir, iconGlobSuffix))
	if err == nil && len(matches) > 0 {
		sort.Strings(matches)
		return matches[len(matches)-1]
	}
	return joinExe(installDir, defaultIconSuffix)
}
```

> 注：菜单栏 template 图标的路径在 Phase 5 `tray_darwin.go` 单独处理（`icon-template.png`），不在此函数职责内——此函数只负责给 `tray.New` 一个存在的图标文件（bundle/Dock 用 `.icns`）。

- [ ] **Step 3: 交叉编译验证（Linux 可跑）**

Run: `go build ./cmd/launcher && GOOS=darwin GOARCH=arm64 go vet ./cmd/launcher`
Expected: 两者均通过（非 CGo 的 vet 在 linux 可验证 darwin 文件选择）。

- [ ] **Step 4: Commit**

```bash
git add cmd/launcher/iconpath_darwin.go cmd/launcher/iconpath_other.go cmd/launcher/main.go
git commit -m "feat(macos): platform-split preferredIconPath (.icns vs .ico)"
```

### Task 2.5：install-mode 可写路径（darwin 用 `paths.InstallRoot`）

**Files:**
- Modify: `internal/installmode/installmode.go`
- Test: `internal/installmode/installmode_test.go`（新建或追加）

> 现状（已确认）：`PathForExecutable(exe)`（line 68）返回 `<exeDir>/install-mode.json`；`PathFromExecutable()`（line 72）用 `os.Executable()`。签名后的 `.app` 里 `Contents/MacOS/` 只读 → darwin 写失败（§4.13）。

- [ ] **Step 1: 写失败测试**

```go
// internal/installmode/installmode_test.go
package installmode

import (
	"path/filepath"
	"testing"
)

func TestPathForWritableUsesGivenDir(t *testing.T) {
	got := PathForWritable(filepath.Join(t.TempDir(), "state"))
	want := filepath.Join(filepath.Join(t.TempDir(), "state"), "install-mode.json")
	_ = want
	if filepath.Base(got) != "install-mode.json" {
		t.Fatalf("PathForWritable base = %q, want install-mode.json", filepath.Base(got))
	}
	if filepath.Dir(got) == "" {
		t.Fatalf("PathForWritable returned empty dir")
	}
}

func TestRoundTripWritable(t *testing.T) {
	dir := t.TempDir()
	p := PathForWritable(dir)
	if err := Write(p, 0 /* FrontendModeCodexDesktop */); err != nil {
		t.Fatalf("Write: %v", err)
	}
	mode, err := Read(p)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if mode != 0 {
		t.Fatalf("mode=%v want 0", mode)
	}
}
```

> `state.FrontendMode` 是 `int` 别名；`FrontendModeCodexDesktop` 值为 `0`（见 `Read` 在文件不存在时的返回）。若测试 helper 需具名常量，改用 `state.FrontendModeCodexDesktop` 并 import `internal/state`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/installmode/ -run TestPathForWritable -v`
Expected: FAIL（`PathForWritable` 未定义）。

- [ ] **Step 3: 加 `PathForWritable` 入口（不改 Windows/Linux 行为）**

在 `internal/installmode/installmode.go` 末尾追加：

```go
// PathForWritable returns the install-mode.json path inside a writable directory.
// Use this on platforms where the executable's own directory is read-only (e.g. a
// code-signed .app bundle on macOS). Windows/Linux keep using PathForExecutable.
func PathForWritable(dir string) string {
	return filepath.Join(dir, "install-mode.json")
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/installmode/ -v`
Expected: PASS。

- [ ] **Step 5: 在 darwin 上让 `cmd/launcher`/`cmd/open-folder`/`cmd/agentctl` 改用可写路径**

这一步是 darwin 专属调用点改造。先定位当前调用 `installmode.PathForExecutable` / `PathFromExecutable` 的位置：

```bash
grep -rn "installmode.PathForExecutable\|installmode.PathFromExecutable" cmd/ internal/
```

对每个调用点，引入平台分派小 helper（cmd 内或 `internal/installmode` 内）。最小侵入做法：在 `internal/installmode` 新增：

```go
// installmode_resolve.go  ——  无 build tag（调用 os.Executable）保留作回退；
// 新增 darwin 专属解析文件：
```

`internal/installmode/resolve_other.go`：
```go
//go:build !darwin

package installmode

// Path resolves the install-mode.json location for the running binary.
// Windows/Linux: next to the executable.
func Path() (string, error) { return PathFromExecutable() }
```

`internal/installmode/resolve_darwin.go`：
```go
//go:build darwin

package installmode

import "github.com/agentserver/agentserver-pkg/internal/paths"

// Path resolves the install-mode.json location for the running binary.
// macOS: a signed .app's Contents/MacOS is read-only, so use the writable
// InstallRoot (~/.agentserver-app) instead.
func Path() (string, error) {
	return PathForWritable(paths.Default().InstallRoot), nil
}
```

> 若 `paths.Default()` 不存在，改用现有获取 `paths.Paths` 的入口（Phase 2 实施时 grep 确认；仓库内典型入口为 `paths.Default()` 或在 cmd 中构造）。然后把 `cmd/*` 里 `installmode.PathForExecutable(...)` / `PathFromExecutable()` 调用统一改为 `installmode.Path()`。

- [ ] **Step 6: 交叉编译 + 回归测试（Linux 可跑）**

Run: `go test ./internal/installmode/ -v && GOOS=darwin GOARCH=arm64 go vet ./internal/installmode/ ./cmd/launcher`
Expected: 测试 PASS；darwin vet 通过。

- [ ] **Step 7: Commit**

```bash
git add internal/installmode/ cmd/
git commit -m "feat(installmode): add writable Path() entry for macOS .app bundles"
```

---

## Phase 3：进程归属 / 存活核验（安全相关，先稳；对应 spec §4.3、§4.4）

> 目标：消除 darwin 上「只判活、不判归属/不核验 exe」的弱行为。共享判定逻辑抽到无标签文件并注入「命令执行器」，使 `*_test.go` 在 Linux 上可跑。

### Task 3.1：console 实例归属判断 —— 拆分 + darwin 强核验

**Files:**
- Delete: `internal/console/instance_process_unix.go`（内容拆分到下方三件套 + 共享）
- Create: `internal/console/instance_process.go`（无标签，共享逻辑 + ps 解析）
- Create: `internal/console/instance_process_linux.go`（`//go:build linux`）
- Create: `internal/console/instance_process_darwin.go`（`//go:build darwin`）
- Create: `internal/console/instance_process_fallback.go`（`//go:build !windows && !linux && !darwin`）
- Test: `internal/console/instance_process_test.go`（无标签，Linux 可跑）

> 现状（已确认，`instance_process_unix.go`）：linux 走 `/proc/<pid>/status` 的 `Uid:`；非 linux（含 darwin）退化为 `syscall.Kill(pid,0)`（只判活，不判归属）。§4.3 要求 darwin 用 `ps -o uid= -p <pid>` 比对 `os.Getuid()`。

- [ ] **Step 1: 写失败测试（共享 ps 解析 + 归属判定，Linux 可跑）**

```go
// internal/console/instance_process_test.go
package console

import "testing"

func TestParseUIDFromPS(t *testing.T) {
	tests := []struct {
		in   string
		uid  int
		ok   bool
	}{
		{"  501\n", 501, true},
		{"501", 501, true},
		{"", 0, false},
		{"  -  \n", 0, false},
		{"notanumber", 0, false},
	}
	for _, tt := range tests {
		uid, ok := parseUIDFromPS(tt.in)
		if ok != tt.ok || uid != tt.uid {
			t.Errorf("parseUIDFromPS(%q)=(%d,%v) want (%d,%v)", tt.in, uid, ok, tt.uid, tt.ok)
		}
	}
}

func TestBelongsToCurrentUser(t *testing.T) {
	// resolve 返回 pid 对应的 uid；ok=false 表示无法判定。
	resolver := func(want map[int]int) func(pid int) (int, bool) {
		return func(pid int) (int, bool) {
			uid, ok := want[pid]
			return uid, ok
		}
	}
	if !instanceProcessBelongsToCurrentUserWith(100, 501, resolver(map[int]int{100: 501})) {
		t.Error("pid 100 owned by 501 should belong to current 501")
	}
	if instanceProcessBelongsToCurrentUserWith(100, 501, resolver(map[int]int{100: 0})) {
		t.Error("pid 100 owned by root (0) should NOT belong to current 501")
	}
	if instanceProcessBelongsToCurrentUserWith(100, 501, resolver(map[int]int{})) {
		t.Error("unknown pid should NOT belong to current user")
	}
	if instanceProcessBelongsToCurrentUserWith(0, 501, resolver(map[int]int{})) {
		t.Error("pid<=0 should not belong")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/console/ -run 'TestParseUIDFromPS|TestBelongsToCurrentUser' -v`
Expected: FAIL（函数未定义）。

- [ ] **Step 3: 写共享逻辑文件 `internal/console/instance_process.go`**

```go
package console

import (
	"os"
	"strconv"
	"strings"
)

// parseUIDFromPS parses the uid emitted by `ps -o uid= -p <pid>` (whitespace-padded).
// Returns ok=false when output is empty or non-numeric.
func parseUIDFromPS(out string) (int, bool) {
	s := strings.TrimSpace(out)
	if s == "" {
		return 0, false
	}
	uid, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return uid, true
}

// instanceProcessBelongsToCurrentUserWith is the platform-agnostic core: it asks
// the resolver for the owning uid of `pid` and compares against currentUID.
// Platforms wire a real resolver (linux: /proc; darwin: ps).
func instanceProcessBelongsToCurrentUserWith(pid, currentUID int, resolve func(pid int) (int, bool)) bool {
	if pid <= 0 {
		return false
	}
	uid, ok := resolve(pid)
	if !ok {
		return false
	}
	return uid == currentUID
}

// currentUID returns os.Getuid(); kept here so platforms/tests can reference it.
func currentUID() int { return os.Getuid() }
```

- [ ] **Step 4: 写 linux 文件 `internal/console/instance_process_linux.go`**

```go
//go:build linux

package console

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func instanceProcessBelongsToCurrentUser(pid int) bool {
	return instanceProcessBelongsToCurrentUserWith(pid, currentUID(), func(pid int) (int, bool) {
		body, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
		if err != nil {
			return 0, false
		}
		for _, line := range strings.Split(string(body), "\n") {
			if !strings.HasPrefix(line, "Uid:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0, false
			}
			uid, err := strconv.Atoi(fields[1])
			return uid, err == nil
		}
		return 0, false
	})
}
```

- [ ] **Step 5: 写 darwin 文件 `internal/console/instance_process_darwin.go`**

```go
//go:build darwin

package console

import (
	"fmt"
	"os/exec"
	"strconv"
)

func instanceProcessBelongsToCurrentUser(pid int) bool {
	return instanceProcessBelongsToCurrentUserWith(pid, currentUID(), func(pid int) (int, bool) {
		out, err := exec.Command("ps", "-o", "uid=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return 0, false
		}
		return parseUIDFromPS(string(out))
	})
}

// 保留给后续若需直接调用（当前未用，避免 unused import 误判）。
var _ = fmt.Sprintf
```

> 若 `fmt` 未被实际使用会编译失败——删除该 `var _` 行与 `fmt` import（这里仅为提示）。最终版只 import `os/exec` 与 `strconv`。

- [ ] **Step 6: 写回退文件 `internal/console/instance_process_fallback.go`**

```go
//go:build !windows && !linux && !darwin

package console

import "syscall"

// 非 linux/darwin 的 unix 回退：仅判活（与历史行为一致；本仓库不实际命中）。
func instanceProcessBelongsToCurrentUser(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
```

- [ ] **Step 7: 删除旧文件**

```bash
git rm internal/console/instance_process_unix.go
```

- [ ] **Step 8: 回归 + 交叉编译（Linux 可跑）**

Run: `go test ./internal/console/ -v && GOOS=darwin GOARCH=arm64 go vet ./internal/console/ && GOOS=linux GOARCH=amd64 go vet ./internal/console/`
Expected: 测试 PASS；darwin/linux vet 通过（三者各自只编译命中平台的一个 `instanceProcessBelongsToCurrentUser`）。

- [ ] **Step 9: Commit**

```bash
git add internal/console/
git commit -m "feat(console): split instance-process ownership; darwin uses ps uid"
```

### Task 3.2：slave 进程存活 + 可执行文件核验 —— 拆分 + darwin 强核验

**Files:**
- Delete: `internal/slave/process_liveness_unix.go`（拆分）
- Create: `internal/slave/process_liveness_shared.go`（无标签：liveness + exe 决策 + sameExecutable + terminate/wait）
- Create: `internal/slave/process_liveness_linux.go`（`//go:build linux`）
- Create: `internal/slave/process_liveness_darwin.go`（`//go:build darwin`）
- Test: `internal/slave/process_liveness_shared_test.go`（无标签，Linux 可跑）
- Modify: `internal/slave/process_liveness_darwin_test.go`（改为强核验断言）

> 现状（已确认，`process_liveness_unix.go`）：`inspectOSProcess` 在 `runtime.GOOS != "linux"` 时直接 `return processMatch`（信任任意活 PID）；仅 linux 读 `/proc/<pid>/exe` 核验。§4.4 要求 darwin 用 `ps -o comm= -p <pid>` 取真实 exe 路径，与期望 exe 比对（`os.SameFile`）。

- [ ] **Step 1: 写失败测试（共享决策逻辑，Linux 可跑）**

```go
// internal/slave/process_liveness_shared_test.go
package slave

import "testing"

// inspectOSProcessWith 把 exe 解析注入，使决策逻辑在 linux 可测。
func TestInspectDecisionLogic(t *testing.T) {
	// resolver 返回 pid 的真实 exe 路径。
	matching := func(pid int) (string, error)  { return "/opt/app/slave-agent", nil }
	mismatch := func(pid int) (string, error)  { return "/sbin/launchd", nil }

	// 期望 exe 与 resolver 返回匹配 → processMatch
	got, err := inspectOSProcessWith(100, "/opt/app/slave-agent", matching)
	if err != nil || got != processMatch {
		t.Errorf("matching exe: got=%v err=%v want processMatch", got, err)
	}
	// 不匹配 → processMismatch（不再信任活 PID）
	got, err = inspectOSProcessWith(100, "/opt/app/slave-agent", mismatch)
	if err != nil || got != processMismatch {
		t.Errorf("mismatched exe: got=%v err=%v want processMismatch", got, err)
	}
	// 期望 exe 为空 → 仅判活 → processMatch（保持原语义）
	got, err = inspectOSProcessWith(100, "", mismatch)
	if err != nil || got != processMatch {
		t.Errorf("empty expected: got=%v err=%v want processMatch", got, err)
	}
	// pid<=0 → processMissing
	got, _ = inspectOSProcessWith(0, "/opt/app/slave-agent", matching)
	if got != processMissing {
		t.Errorf("pid<=0: got=%v want processMissing", got)
	}
}
```

> 注：`inspectOSProcessWith` 是测试用的注入入口；`inspectOSProcess` 内部调用 `resolveProcessExe(pid)`（平台实现）并转调 `inspectOSProcessWith`。`processMissing/processMatch/processMismatch/processUnknown` 为包内已有常量（见现状代码）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/slave/ -run TestInspectDecisionLogic -v`
Expected: FAIL（`inspectOSProcessWith` 未定义）。

- [ ] **Step 3: 写共享文件 `internal/slave/process_liveness_shared.go`**

```go
package slave

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// resolveProcessExe returns the real executable path of a running pid.
// Implemented per-platform: linux reads /proc/<pid>/exe; darwin parses `ps`.
var resolveProcessExe = func(pid int) (string, error) {
	return "", errors.New("resolveProcessExe not wired for this platform")
}

func osProcessExists(pid int) bool {
	inspection, err := inspectOSProcess(pid, "")
	return err == nil && inspection == processMatch
}

func inspectOSProcess(pid int, expectedExe string) (processInspection, error) {
	return inspectOSProcessWith(pid, expectedExe, resolveProcessExe)
}

// inspectOSProcessWith is the platform-agnostic core. resolve is injected so the
// decision logic is unit-testable on Linux.
func inspectOSProcessWith(pid int, expectedExe string, resolve func(int) (string, error)) (processInspection, error) {
	if pid <= 0 {
		return processMissing, nil
	}
	if err := syscall.Kill(pid, 0); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return processMissing, nil
		}
		if !errors.Is(err, syscall.EPERM) {
			return processUnknown, err
		}
	}
	if strings.TrimSpace(expectedExe) == "" {
		return processMatch, nil
	}
	procExe, err := resolve(pid)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return processMissing, nil
		}
		return processUnknown, err
	}
	matches, err := sameExecutable(procExe, expectedExe)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return processMissing, nil
		}
		return processUnknown, err
	}
	if matches {
		return processMatch, nil
	}
	return processMismatch, nil
}

func terminateUntrackedProcess(ctx context.Context, pid int, expectedExe string, timeout time.Duration) error {
	inspection, err := inspectOSProcess(pid, expectedExe)
	if err != nil {
		return err
	}
	if inspection != processMatch {
		return fmt.Errorf("%w: %d", ErrProcessNotRunning, pid)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Kill(); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("%w: %d", ErrProcessNotRunning, pid)
		}
		return err
	}
	return waitForProcessExit(ctx, pid, timeout)
}

func sameExecutable(got, want string) (bool, error) {
	got = strings.TrimSuffix(got, " (deleted)")
	gotAbs, err := filepath.Abs(got)
	if err != nil {
		return false, err
	}
	wantAbs, err := filepath.Abs(want)
	if err != nil {
		return false, err
	}
	if gotAbs == wantAbs {
		return true, nil
	}
	gotInfo, err := os.Stat(gotAbs)
	if err != nil {
		return false, fmt.Errorf("stat got executable: %w", err)
	}
	wantInfo, err := os.Stat(wantAbs)
	if err != nil {
		return false, fmt.Errorf("stat expected executable: %w", err)
	}
	return os.SameFile(gotInfo, wantInfo), nil
}
```

> `waitForProcessExit` 与 `ErrProcessNotRunning`、`processInspection` 常量来自现有包（原 `process_liveness_unix.go` 之外的文件，保持不变）。若 `waitForProcessExit` 原本就在被删的 `*_unix.go` 中，把它也搬进此共享文件（现状该文件未出现它，故它在别处——保持不动）。

- [ ] **Step 4: 写 linux 文件 `internal/slave/process_liveness_linux.go`**

```go
//go:build linux

package slave

import (
	"fmt"
	"os"
	"path/filepath"
)

func init() {
	resolveProcessExe = func(pid int) (string, error) {
		return os.Readlink(filepath.Join("/proc", fmt.Sprint(pid), "exe"))
	}
}
```

- [ ] **Step 5: 写 darwin 文件 `internal/slave/process_liveness_darwin.go`**

```go
//go:build darwin

package slave

import (
	"fmt"
	"os/exec"
	"strings"
)

func init() {
	resolveProcessExe = func(pid int) (string, error) {
		out, err := exec.Command("ps", "-o", "comm=", "-p", fmt.Sprint(pid)).Output()
		if err != nil {
			return "", err
		}
		// ps -o comm= 给出可执行文件的绝对路径（可能截断长路径）；trim 后由
		// sameExecutable 的 os.SameFile 兜底比对。
		return strings.TrimSpace(string(out)), nil
	}
}
```

- [ ] **Step 6: 写回退文件 `internal/slave/process_liveness_fallback.go`**

```go
//go:build !windows && !linux && !darwin

package slave

// 非 linux/darwin unix：无法可靠核验 exe，退化为「不解析」（inspectOSProcess
// 仅靠 Kill 判活 + 空 exe 短路；带 expectedExe 时返回 processUnknown 由调用方处理）。
func init() {
	resolveProcessExe = func(pid int) (string, error) {
		return "", nil // nil + 空串：sameExecutable 走 os.Stat → NotExist → processMissing
	}
}
```

> 评估：回退路径返回 `("", nil)`，`sameExecutable("","x")` 会 `os.Stat("")` 报错 → `processUnknown`。若希望回退保留历史「信任活 PID」语义，可让 `resolveProcessExe` 直接令 `inspectOSProcessWith` 命中 `processMatch`——但那会重新引入弱行为。鉴于本仓库目标平台仅 windows/linux/darwin，回退不实际命中，保持「核验失败即 unknown」更安全。

- [ ] **Step 7: 删除旧文件**

```bash
git rm internal/slave/process_liveness_unix.go
```

- [ ] **Step 8: 改写 darwin 测试为强核验断言**

替换 `internal/slave/process_liveness_darwin_test.go` 全文：

```go
//go:build darwin

package slave

import "testing"

// 强核验：pid 1 在 macOS 上是 launchd（/sbin/launchd），不是 slave-agent，
// 因此带上 expectedExe 时应判为 processMismatch，而非历史弱行为 processMatch。
func TestInspectOSProcessOnDarwinVerifiesExeNotBlindly(t *testing.T) {
	got, err := inspectOSProcess(1, "/Applications/星池指挥官.app/Contents/MacOS/slave-agent")
	if err != nil {
		t.Fatalf("inspectOSProcess returned error on darwin: %v", err)
	}
	if got != processMismatch {
		t.Fatalf("inspection=%v, want processMismatch (launchd != slave-agent)", got)
	}
}
```

- [ ] **Step 9: 回归 + 交叉编译（Linux 可跑；darwin 测试在 Mac 跑）**

Run: `go test ./internal/slave/ -run TestInspectDecisionLogic -v`
Expected: PASS（共享逻辑在 linux 通过）。

Run: `GOOS=darwin GOARCH=arm64 go vet ./internal/slave/ && GOOS=linux GOARCH=amd64 go vet ./internal/slave/`
Expected: vet 通过。

> darwin-only 测试（`TestInspectOSProcessOnDarwinVerifiesExeNotBlindly`）在 Mac 上跑：`GOOS=darwin go test ./internal/slave/ -run TestInspectOSProcessOnDarwin -v` → PASS。

- [ ] **Step 10: 全量回归（确保未破坏其它包）**

Run: `go test -race -count=1 ./...`
Expected: 全 PASS（与基线一致）。

- [ ] **Step 11: Commit**

```bash
git add internal/slave/
git commit -m "feat(slave): split process liveness; darwin verifies exe via ps"
```

---

## Phase 4：平台层补齐（无 CGo 部分；对应 spec §4.2、§4.5–§4.11、§4.14）

> 约定：每个 `*_other.go` 收窄为 `!windows && !darwin`；解析/判定逻辑抽到无标签文件并注入命令执行器，使其 `*_test.go` 在 Linux 可跑。Mac-only 项给出确切命令与期望现象。

### Task 4.1：环境变量持久化 `internal/env`（§4.5）

**Files:**
- Create: `internal/env/persist_shared.go`（无标签：rc 文件受管块编辑逻辑）
- Create: `internal/env/persist_darwin.go`（`//go:build darwin`）
- Modify: `internal/env/persist_other.go`（收窄为 `//go:build !windows && !darwin`）
- Test: `internal/env/persist_shared_test.go`（无标签，Linux 可跑）

- [ ] **Step 1: 写失败测试（受管块注入/移除，Linux 可跑）**

```go
// internal/env/persist_shared_test.go
package env

import "strings"
import "testing"

func TestInjectManagedBlock(t *testing.T) {
	existing := "# my zshrc\nexport PATH=...\n"
	out := injectManagedBlock(existing, "export FOO=bar", managedStartMarker, managedEndMarker)
	if !strings.Contains(out, managedStartMarker) || !strings.Contains(out, "export FOO=bar") || !strings.Contains(out, managedEndMarker) {
		t.Errorf("block not injected:\n%s", out)
	}
}

func TestInjectManagedBlockReplacesExisting(t *testing.T) {
	existing := "header\n" + managedStartMarker + "\nexport FOO=old\n" + managedEndMarker + "\nfooter\n"
	out := injectManagedBlock(existing, "export FOO=new", managedStartMarker, managedEndMarker)
	if strings.Contains(out, "FOO=old") {
		t.Error("old block should be replaced, not duplicated")
	}
	if !strings.Contains(out, "FOO=new") {
		t.Errorf("new block missing:\n%s", out)
	}
}

func TestRemoveManagedBlock(t *testing.T) {
	existing := "header\n" + managedStartMarker + "\nexport FOO=bar\n" + managedEndMarker + "\nfooter\n"
	out := removeManagedBlock(existing, managedStartMarker, managedEndMarker)
	if strings.Contains(out, "FOO=bar") || strings.Contains(out, managedStartMarker) {
		t.Errorf("block not removed:\n%s", out)
	}
	if !strings.Contains(out, "header") || !strings.Contains(out, "footer") {
		t.Error("non-managed content should be preserved")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/env/ -v`
Expected: FAIL（函数/常量未定义）。

- [ ] **Step 3: 写共享逻辑 `internal/env/persist_shared.go`**

```go
package env

import "strings"

const (
	managedStartMarker = "# agentserver-managed:start"
	managedEndMarker   = "# agentserver-managed:end"
)

// managedBlock wraps content between the start/end managed markers.
func managedBlock(content string) string {
	var b strings.Builder
	b.WriteString(managedStartMarker)
	b.WriteByte('\n')
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(managedEndMarker)
	b.WriteByte('\n')
	return b.String()
}

// injectManagedBlock replaces any existing managed block in `content` with one
// containing `lines`, preserving everything else. Appends at end if absent.
func injectManagedBlock(content, lines, start, end string) string {
	cleaned := removeManagedBlock(content, start, end)
	cleaned = strings.TrimRight(cleaned, "\n")
	if cleaned != "" {
		cleaned += "\n"
	}
	return cleaned + start + "\n" + ensureTrailingNewline(lines) + end + "\n"
}

// removeManagedBlock strips the managed block (inclusive) from `content`.
func removeManagedBlock(content, start, end string) string {
	sIdx := strings.Index(content, start)
	if sIdx < 0 {
		return content
	}
	eIdx := strings.Index(content[sIdx:], end)
	if eIdx < 0 {
		return content
	}
	eIdx += sIdx + len(end)
	rest := content[eIdx:]
	rest = strings.TrimPrefix(rest, "\n")
	return content[:sIdx] + rest
}

func ensureTrailingNewline(s string) string {
	if s == "" {
		return ""
	}
	if !strings.HasSuffix(s, "\n") {
		return s + "\n"
	}
	return s
}
```

- [ ] **Step 4: 写 darwin 实现 `internal/env/persist_darwin.go`**

```go
//go:build darwin

package env

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// persistUserEnv makes KEY=VALUE visible to new shells and the current GUI
// session: `launchctl setenv` for the current session, plus a managed block in
// ~/.zshrc and ~/.bash_profile for new terminals / reboots.
//
// 已知局限（spec §4.5）：macOS GUI 应用不继承 rc 文件环境；launchctl setenv 只对
// 当前会话有效。对「把 key 暴露给 slave 子进程」够用（slave 由 launcher 显式 spawn）。
func persistUserEnv(key, value string) error {
	if err := exec.Command("launchctl", "setenv", key, value).Run(); err != nil {
		return fmt.Errorf("launchctl setenv %s: %w", key, err)
	}
	line := fmt.Sprintf("export %s=%q", key, value)
	for _, rc := range []string{".zshrc", ".bash_profile"} {
		if err := writeManagedRC(rc, line); err != nil {
			return err
		}
	}
	return nil
}

func deleteUserEnv(key string) error {
	_ = exec.Command("launchctl", "unsetenv", key).Run()
	for _, rc := range []string{".zshrc", ".bash_profile"} {
		if err := removeManagedRC(rc); err != nil {
			return err
		}
	}
	return nil
}

func writeManagedRC(rcName, line string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, rcName)
	content, _ := os.ReadFile(path)
	updated := injectManagedBlock(string(content), line, managedStartMarker, managedEndMarker)
	return os.WriteFile(path, []byte(updated), 0o644)
}

func removeManagedRC(rcName string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, rcName)
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	updated := removeManagedBlock(string(content), managedStartMarker, managedEndMarker)
	return os.WriteFile(path, []byte(updated), 0o644)
}
```

- [ ] **Step 5: 收窄 `internal/env/persist_other.go`**

把首行改为 `//go:build !windows && !darwin`，正文不变（仍是 no-op）。

- [ ] **Step 6: 回归 + 交叉编译（Linux 可跑）**

Run: `go test ./internal/env/ -v && GOOS=darwin GOARCH=arm64 go vet ./internal/env/`
Expected: 测试 PASS；darwin vet 通过。

- [ ] **Step 7: Commit**

```bash
git add internal/env/
git commit -m "feat(macos): env persistence via launchctl + managed rc blocks"
```

### Task 4.2：文件夹选择器 `internal/folderpicker`（§4.2）

**Files:**
- Create: `internal/folderpicker/folderpicker_darwin.go`（`//go:build darwin`）
- Modify: `internal/folderpicker/folderpicker_other.go`（收窄为 `//go:build !windows && !darwin`）

- [ ] **Step 1: 写 darwin 实现**

```go
//go:build darwin

package folderpicker

import (
	"context"
	"os/exec"
	"strings"
)

// selectFolder shows a native folder picker via AppleScript. Returns the chosen
// POSIX path, or ("", nil) when the user cancels.
func selectFolder(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx,
		"osascript", "-e",
		`POSIX path of (choose folder with prompt "选择允许被远程控制的文件夹")`)
	out, err := cmd.Output()
	if err != nil {
		// 用户取消时 osascript 退出码非 0（“User canceled”）→ 返回空路径、无错误。
		if ctx.Err() == nil {
			return "", nil
		}
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return "", nil
	}
	return p, nil
}
```

> 用 `POSIX path of (...)` 让 osascript 直接返回 POSIX 路径，免去解析 AppleScript 的 HFS `alias` 形式。`folderpicker.go`（共享）的公开 `SelectFolder` 已调用 `selectFolder`，无需改动。

- [ ] **Step 2: 收窄 `internal/folderpicker/folderpicker_other.go`**

首行改为 `//go:build !windows && !darwin`，正文（硬错误）不变。

- [ ] **Step 3: 交叉编译（Linux 可跑）**

Run: `GOOS=darwin GOARCH=arm64 go vet ./internal/folderpicker/ && GOOS=linux go vet ./internal/folderpicker/`
Expected: 通过。

- [ ] **Step 4: 验证（Mac-only）**

写一个临时 main 或在 onboarding 调用处手动触发；期望：弹出原生文件夹选择框，选择后返回 POSIX 路径，取消返回 `("", nil)`。

- [ ] **Step 5: Commit**

```bash
git add internal/folderpicker/
git commit -m "feat(macos): native folder picker via osascript"
```

### Task 4.3：桌面快捷方式 + Finder Quick Action `internal/shortcut`（§4.6）

**Files:**
- Create: `internal/shortcut/shortcut_darwin.go`（`//go:build darwin`）
- Modify: `internal/shortcut/shortcut_other.go`（收窄为 `//go:build !windows && !darwin`）

> 结构体字段（已确认）：`DesktopInput{Name, TargetExe, Args, IconPath, WorkDir}`、`ContextMenuInput{MenuLabel, HandlerExe, IconPath, RegistryKeySuffix}`。`TargetExe`/`HandlerExe` 在 darwin 上是 `…/星池指挥官.app/Contents/MacOS/{launcher,open-folder}`。

> **Quick Action 路径解耦（关键设计）：** `.workflow` 的 shell 不硬编码 `.app` 路径，而是读 `~/.agentserver-app/open-folder-path.txt`（找不到则回退 `/Applications/星池指挥官.app/Contents/MacOS/open-folder`）。因此 Task 1.4 的 workflow 模板 shell 必须是该间接版（见下方 Step 1 给出的精确 shell）。安装时只拷贝 workflow + 写该文件，**不编辑二进制 workflow**。

- [ ] **Step 1: 确认/修正 Task 1.4 workflow 模板的 shell 内容**

Task 1.4 的「运行 Shell 脚本」动作内容必须为：

```bash
# agentserver-managed:open-folder
OF="$(cat "$HOME/.agentserver-app/open-folder-path.txt" 2>/dev/null)"
[ -x "$OF" ] || OF="/Applications/星池指挥官.app/Contents/MacOS/open-folder"
for f in "$@"; do
  "$OF" "$f"
done
```

> 若 Task 1.4 已用硬编码版，在 Mac 上重新打开 Automator 改成上述内容后重新导出 workflow。

- [ ] **Step 2: 写 darwin 实现 `internal/shortcut/shortcut_darwin.go`**

```go
//go:build darwin

package shortcut

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// appBundleFromExe derives the .app bundle root from an executable path inside
// Contents/MacOS/. e.g. .../星池指挥官.app/Contents/MacOS/launcher -> .../星池指挥官.app
func appBundleFromExe(exe string) (string, error) {
	macOS := filepath.Dir(exe)          // .../Contents/MacOS
	contents := filepath.Dir(macOS)     // .../Contents
	bundle := filepath.Dir(contents)    // .../星池指挥官.app
	if filepath.Ext(bundle) != ".app" {
		return "", fmt.Errorf("shortcut: %q is not inside an .app bundle", exe)
	}
	return bundle, nil
}

func ensureDesktopShortcutPlatform(in DesktopInput) error {
	bundle, err := appBundleFromExe(in.TargetExe)
	if err != nil {
		return err
	}
	desktop, err := filepath.Abs(filepath.Join(os.Getenv("HOME"), "Desktop"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(desktop, 0o755); err != nil {
		return err
	}
	// Finder alias（保留 bundle 图标）。
	script := fmt.Sprintf(`tell application "Finder" to make alias file to (POSIX file %q) to (POSIX file %q)`, bundle, desktop)
	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		// 回退 symlink（丢失图标，但保证有入口）。
		return os.Symlink(bundle, filepath.Join(desktop, in.Name))
	}
	return nil
}

func installContextMenuPlatform(in ContextMenuInput) error {
	// 1) 记录 open-folder 真实路径，供 Quick Action 间接读取（位置无关）。
	dataRoot := filepath.Join(os.Getenv("HOME"), ".agentserver-app")
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dataRoot, "open-folder-path.txt"), []byte(in.HandlerExe+"\n"), 0o644); err != nil {
		return fmt.Errorf("write open-folder-path.txt: %w", err)
	}
	// 2) 拷贝随包 workflow 模板到 ~/Library/Services/。
	bundle, err := appBundleFromExe(in.HandlerExe)
	if err != nil {
		return err
	}
	src := filepath.Join(bundle, "Contents", "Resources", in.MenuLabel+".workflow")
	dstDir := filepath.Join(os.Getenv("HOME"), "Library", "Services")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dstDir, in.MenuLabel+".workflow")
	_ = os.RemoveAll(dst)
	return copyAll(src, dst)
}

func uninstallAllPlatform(in ContextMenuInput, desktopName string) error {
	if desktopName != "" {
		_ = os.Remove(filepath.Join(os.Getenv("HOME"), "Desktop", desktopName))
	}
	_ = os.RemoveAll(filepath.Join(os.Getenv("HOME"), "Library", "Services", in.MenuLabel+".workflow"))
	_ = os.Remove(filepath.Join(os.Getenv("HOME"), ".agentserver-app", "open-folder-path.txt"))
	return nil
}

// copyAll recursively copies a directory tree (used for the .workflow bundle).
func copyAll(src, dst string) error {
	return exec.Command("cp", "-R", src, dst).Run()
}
```

- [ ] **Step 3: 收窄 `internal/shortcut/shortcut_other.go`**

首行改为 `//go:build !windows && !darwin`，正文（硬错误）不变。

- [ ] **Step 4: 交叉编译（Linux 可跑）**

Run: `GOOS=darwin GOARCH=arm64 go vet ./internal/shortcut/`
Expected: 通过。

- [ ] **Step 5: 验证（Mac-only）**

- 桌面别名：触发 `EnsureDesktopShortcut`，期望桌面上出现「星池指挥官」别名（带 bundle 图标）。
- Quick Action：触发 `InstallContextMenu`，期望 `~/Library/Services/用星池指挥官打开.workflow` 存在；Finder 右键文件夹出现「用星池指挥官打开」，点击后对应前端打开。
- 卸载：`UninstallAll`，期望别名与 workflow 均被删除。

- [ ] **Step 6: Commit**

```bash
git add internal/shortcut/ "packaging/macos/用星池指挥官打开.workflow"
git commit -m "feat(macos): desktop alias + Finder Quick Action shortcuts"
```

### Task 4.4：VS Code 探测/安装/配置 `internal/vscode`（§4.7）

**Files:**
- Create: `internal/vscode/detect_darwin.go`（`//go:build darwin`）
- Create: `internal/vscode/install_darwin.go`（`//go:build darwin`）
- Create: `internal/vscode/install_authenticode_darwin.go`（`//go:build darwin`）
- Modify: `internal/vscode/detect_other.go`、`install_other.go`、`install_authenticode_other.go`（收窄为 `!windows && !darwin`）
- Modify: `internal/vscode/install.go`（`planInstallFor` 加 darwin 分支消除 panic；`validateBootstrapperFile` 加 zip 魔数分支）
- Modify: `internal/vscode/settings.go`（补 osx profile）

- [ ] **Step 1: 收窄三个 `*_other.go` 为 `!windows && !darwin`**

`detect_other.go`、`install_other.go`、`install_authenticode_other.go` 首行均改为 `//go:build !windows && !darwin`，正文不变。

- [ ] **Step 2: 写 `detect_darwin.go`**

```go
//go:build darwin

package vscode

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func detectPlatform() (Detected, error) {
	candidates := []string{
		"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code",
		filepath.Join(os.Getenv("HOME"), "Applications", "Visual Studio Code.app", "Contents", "Resources", "app", "bin", "code"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			if det, err := detectAt(c); err == nil {
				return det, nil
			}
		}
	}
	if p, err := exec.LookPath("code"); err == nil {
		return detectAt(p)
	}
	return Detected{Installed: false}, errors.New("VS Code not found")
}

// detectAt（共享，install.go 同包）已用 `code --version` 解析版本；darwin 候选 .app
// 内的 code 脚本同样支持 --version，无需平台定制。
var _ = strings.TrimSpace
```

> 若 `strings` 未实际使用，删除其 import 与 `var _` 行（编译期检查）。

- [ ] **Step 3: 在 `install.go` 的 `planInstallFor` 加 darwin 分支（消除 panic）**

现状（已确认，install.go line 45-47）非 windows/amd64 会 `panic`。改为：

```go
func planInstallFor(goos, goarch string) InstallPlan {
	switch goos {
	case "windows":
		if goarch != "amd64" {
			panic(fmt.Sprintf("vscode install: unsupported %s/%s in v1", goos, goarch))
		}
		return InstallPlan{
			URLs:            []string{StoreBootstrapperURL},
			URL:             StoreBootstrapperURL,
			BootstrapperURL: StoreBootstrapperURL,
			StoreProductID:  StoreProductID,
			InstallerType:   "MicrosoftStoreBootstrapper",
			FileExt:         ".exe",
		}
	case "darwin":
		arch := "darwin-universal"
		if goarch == "arm64" {
			arch = "darwin-arm64"
		} else if goarch == "amd64" {
			arch = "darwin-x64"
		}
		u := "https://update.code.visualstudio.com/latest/" + arch + "/stable"
		return InstallPlan{
			URLs:          []string{u},
			URL:           u,
			InstallerType: "MacZip",
			FileExt:       ".zip",
		}
	default:
		panic(fmt.Sprintf("vscode install: unsupported %s/%s in v1", goos, goarch))
	}
}
```

> `https://update.code.visualstudio.com/latest/{darwin-arm64|darwin-x64|darwin-universal}/stable` 是 VS Code 官方稳定版 zip 下载端点（返回 zip，内含 `Visual Studio Code.app`）。SHA256 留空（下载后由 install_authenticode_darwin 的 `codesign --verify` 校验身份，而非逐字节 hash——Mac 无类似 Windows bootstrapper 的固定 SHA）。

- [ ] **Step 4: 在 `install.go` 的 `validateBootstrapperFile` 加 zip 魔数分支**

现状（install.go line 176-197）要求 `"MZ"` PE 魔数。改为按文件类型分流（zip 魔数 `PK\x03\x04`）：

```go
func validateBootstrapperFile(ctx context.Context, path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Size() < minBootstrapperSize {
		return fmt.Errorf("VS Code installer too small: got %d bytes, want at least %d", st.Size(), minBootstrapperSize)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		return err
	}
	switch {
	case string(magic[:2]) == "MZ":
		// Windows PE bootstrapper.
		return bootstrapperSignatureValidator(ctx, path)
	case string(magic[:2]) == "PK":
		// Mac zip（PK\x03\x04）。
		return bootstrapperSignatureValidator(ctx, path)
	default:
		return fmt.Errorf("VS Code installer has unknown magic %q", magic)
	}
}
```

> darwin 走 `bootstrapperSignatureValidator`（= `validateBootstrapperSignature`，平台分派：windows 版指向 PE 校验，darwin 版指向 `codesign`/`spctl`，见 Step 6）。`minBootstrapperSize`（65536）对 zip 同样适用。

- [ ] **Step 5: 写 `install_darwin.go`（静默安装 + 清隔离）**

```go
//go:build darwin

package vscode

import (
	"context"
	"fmt"
	"os/exec"
)

// silentInstallPlatform unzips the VS Code .zip into /Applications and clears
// Gatekeeper quarantine. Mirrors Windows SilentInstall's "校验后安装"语义。
func silentInstallPlatform(ctx context.Context, path string, plan InstallPlan) error {
	if err := exec.CommandContext(ctx, "unzip", "-o", "-q", path, "-d", "/Applications").Run(); err != nil {
		return fmt.Errorf("unzip VS Code: %w", err)
	}
	// 校验签名与公证（见 install_authenticode_darwin）。
	if err := validateBootstrapperSignature(ctx, "/Applications/Visual Studio Code.app"); err != nil {
		return err
	}
	// 清隔离属性，避免首次运行 Gatekeeper 拦截。
	_ = exec.CommandContext(ctx, "xattr", "-dr", "com.apple.quarantine", "/Applications/Visual Studio Code.app").Run()
	return nil
}
```

- [ ] **Step 6: 写 `install_authenticode_darwin.go`（codesign + spctl 校验）**

```go
//go:build darwin

package vscode

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const macSignatureTimeout = 60 * time.Second

// validateBootstrapperSignature verifies the downloaded app's code signature and
// Gatekeeper assessment. Replaces the Windows Authenticode path on macOS.
func validateBootstrapperSignature(ctx context.Context, appPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c, cancel := context.WithTimeout(ctx, macSignatureTimeout)
	defer cancel()

	if out, err := exec.CommandContext(c, "codesign", "--verify", "--deep", "--strict", appPath).CombinedOutput(); err != nil {
		return fmt.Errorf("codesign verify %s: %w: %s", appPath, err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.CommandContext(c, "spctl", "--assess", "--type", "execute", "--verbose", appPath).CombinedOutput(); err != nil {
		// 未签名/未公证时 spctl 会失败；v1 接受（用户右键打开），仅记录。
		// 若要求严格，可 return fmt.Errorf(...)。
		_ = out
	}
	return nil
}
```

- [ ] **Step 7: 在 `settings.go` 补 osx terminal profile（line 91-97 windows profile 之后追加）**

在 `overrides` map 内 `terminal.integrated.profiles.windows` 之后追加：

```go
		"terminal.integrated.defaultProfile.osx": "codex",
		"terminal.integrated.profiles.osx": map[string]any{
			"codex": map[string]any{
				"path": "/bin/zsh",
				"args": []string{"-l", "-c", in.CodexAbsPath + "; exec /bin/zsh -l"},
			},
		},
```

> 说明：osx 下用 zsh 登录 shell 启动 codex，然后落回交互式 zsh（镜像 windows 的 `/k codex` 行为：开终端即起 codex，退出后留在终端）。若 codex 无需常驻，可简化为 `"args": []string{"-c", in.CodexAbsPath + "; exec /bin/zsh -l"}`。

- [ ] **Step 8: 交叉编译 + 回归（Linux 可跑）**

Run: `GOOS=darwin GOARCH=arm64 go vet ./internal/vscode/ && go test ./internal/vscode/`
Expected: vet 通过；现有测试 PASS（`planInstallFor` windows 路径不变）。

- [ ] **Step 9: 验证（Mac-only）**

- 探测：装了 VS Code 时 `Detect()` 返回 Installed=true 与版本；未装返回 not found。
- 安装：`SilentInstall` 下载 zip → `/Applications/Visual Studio Code.app` 存在，`codesign --verify` 通过。
- 设置：`WriteSettings` 产出的 settings.json 含 `terminal.integrated.defaultProfile.osx:"codex"` 与 osx profile。

- [ ] **Step 10: Commit**

```bash
git add internal/vscode/
git commit -m "feat(macos): vscode detect/install/signature/osx-profile"
```

### Task 4.5：Codex Desktop 探测/安装 `internal/codexdesktop`（§4.8）

**Files:**
- Create: `internal/codexdesktop/detect_darwin.go`（`//go:build darwin`）
- Create: `internal/codexdesktop/install_darwin.go`（`//go:build darwin`）
- Modify: `internal/codexdesktop/detect_other.go`（收窄）
- Modify: `internal/codexdesktop/install.go`（`EnsureInstalled` 安装机制平台分派）
- Modify: `internal/codexdesktop/winget.go`（`runWinget` 仅 windows，已具 runtime 守卫，无需改逻辑）

> 现状（已确认）：`EnsureInstalled` 在 `install.go` line 35 调 `run(ctx, WingetInstallArgs())`，`runWinget` 在非 windows 返回 `ErrUnsupportedPlatform`。需让 darwin 走 darwin 安装路径。`launch.go` 的 `codex://threads/new` 经 `browser.Open`（`open` 命令）触发 Launch Services——已可用，**无需改**。

- [ ] **Step 1: 收窄 `detect_other.go` 为 `!windows && !darwin`**

首行改为 `//go:build !windows && !darwin`，正文不变。

- [ ] **Step 2: 写 `detect_darwin.go`**

```go
//go:build darwin

package codexdesktop

import (
	"os"
	"path/filepath"
)

func detectPlatform() (Detected, error) {
	plist := "/Applications/Codex.app/Contents/Info.plist"
	if _, err := os.Stat(plist); err != nil {
		// 也看用户级 ~/Applications
		home := filepath.Join(os.Getenv("HOME"), "Applications", "Codex.app", "Contents", "Info.plist")
		if _, err := os.Stat(home); err != nil {
			return Detected{Installed: false}, ErrNotFound
		}
		plist = home
	}
	// 版本取 CFBundleShortVersionString；取不到则 Installed=true、Version 空。
	ver := readBundleShortVersion(plist)
	return Detected{Installed: true, Version: ver}, nil
}
```

并在同文件或 `detect_darwin.go` 补 `readBundleShortVersion`：

```go
//go:build darwin

package codexdesktop

import "os/exec"

func readBundleShortVersion(plist string) string {
	out, err := exec.Command("defaults", "read", plist, "CFBundleShortVersionString").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
```

（import `"strings"`。）

- [ ] **Step 3: 把 `EnsureInstalled` 的安装机制平台分派**

改 `install.go` 的 `EnsureInstalled`：把 `run(ctx, WingetInstallArgs())` 替换为平台安装入口 `installDesktopPlatform(ctx)`，windows 与 darwin 各自实现。

在 `install.go` 修改：
- `Options` 增加字段 `Install func(context.Context) error`（可选注入，便于测试）。
- `run`（默认）改为：windows → `runWinget`，darwin → `installDesktopDarwin`，其它 → 返回 `ErrUnsupportedPlatform`。

```go
// install.go（共享）片段替换
func EnsureInstalled(ctx context.Context, opts Options) (Detected, error) {
	detect := opts.Detect
	if detect == nil {
		detect = Detect
	}
	install := opts.Install
	if install == nil {
		install = installDesktopPlatform
	}
	det, err := detect()
	if err == nil {
		if det.Installed {
			return det, nil
		}
	} else if !errors.Is(err, ErrNotFound) {
		return Detected{}, fmt.Errorf("detect Codex Desktop: %w", err)
	}
	if err := install(ctx); err != nil {
		return Detected{}, err
	}
	det, err = detect()
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Detected{}, fmt.Errorf("codex desktop 安装后仍未检测到: %w", ErrNotFound)
		}
		return Detected{}, fmt.Errorf("codex desktop 安装后检测失败: %w", err)
	}
	if !det.Installed {
		return Detected{}, fmt.Errorf("codex desktop 安装后仍未检测到: %w", ErrNotFound)
	}
	return det, nil
}
```

新增平台分派文件：

`install_dispatch_windows.go`（`//go:build windows`）：
```go
//go:build windows

package codexdesktop

import "context"

func installDesktopPlatform(ctx context.Context) error {
	out, err := runWinget(ctx, WingetInstallArgs())
	if err != nil {
		return ClassifyWingetError(err, out)
	}
	return nil
}
```

`install_dispatch_darwin.go`（`//go:build darwin`）：见 Step 4（`installDesktopPlatform` 仅在此处 + windows 处定义，**不要**在 install_darwin.go 重复定义）。

> 原 `install.go` 中 `EnsureInstalled` 调 `ClassifyWingetError(err, out)` 的位置，现由 `installDesktopPlatform` 在 windows 分支内自行分类后返回；`EnsureInstalled` 直接 `return err`。`winget.go` 的 `ClassifyWingetError`/`WingetInstallArgs`/`RequireWinget`/`runWinget` 保留不变。

- [ ] **Step 4: 写 `install_darwin.go`（含 dispatch 入口）**

```go
//go:build darwin

package codexdesktop

import (
	"context"
	"fmt"
	"os/exec"
)

// Codex Desktop darwin 下载地址（dmg/zip）。
// 注意（spec §10）：需确认 Codex Desktop darwin 分发包的确切下载 URL/格式。
// 此处用命名常量，发布前由维护者确认实际资产地址。
const darwinCodexDesktopURL = "https://desktop.openai.com/download/codex-mac-universal.dmg"

// installDesktopPlatform 是 EnsureInstalled 的 darwin 安装入口（见 Step 3 dispatch）。
func installDesktopPlatform(ctx context.Context) error {
	return installDesktopDarwin(ctx)
}

func installDesktopDarwin(ctx context.Context) error {
	// 下载 dmg → 挂载 → 拷贝 Codex.app 到 /Applications → 校验 → 清隔离。
	cache, err := downloadToCache(ctx, darwinCodexDesktopURL)
	if err != nil {
		return fmt.Errorf("download codex desktop: %w", err)
	}
	if err := installDMGApp(ctx, cache, "Codex.app"); err != nil {
		return fmt.Errorf("install codex desktop dmg: %w", err)
	}
	_ = exec.CommandContext(ctx, "xattr", "-dr", "com.apple.quarantine", "/Applications/Codex.app").Run()
	return nil
}
```

> `installDesktopPlatform` 仅在 `install_dispatch_windows.go`（windows）与本文件（darwin）各定义一次；**不要**再单独建 `install_dispatch_darwin.go`（Step 3 的「见 Step 4」即指本处）。

> `downloadToCache` 与 `installDMGApp` 是通用 dmg 安装辅助（下载到临时目录 / hdiutil attach + cp + detach）。若仓库尚无，在本任务内实现（见 Step 5）；若 updater/codexruntime 已有类似，复用。

- [ ] **Step 5: 通用 dmg 安装辅助（若仓库无）**

新增 `internal/codexdesktop/dmg.go`（`//go:build darwin`）：

```go
//go:build darwin

package codexdesktop

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

func downloadToCache(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	tmp, err := os.MkdirTemp("", "codexdesktop-*")
	if err != nil {
		return "", err
	}
	out := filepath.Join(tmp, "codex.dmg")
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return out, nil
}

// installDMGApp mounts the dmg, copies appName (.app) into /Applications, detaches.
func installDMGApp(ctx context.Context, dmgPath, appName string) error {
	mnt, err := os.MkdirTemp("", "dmg-*")
	if err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, "hdiutil", "attach", "-nobrowse", "-mountpoint", mnt, dmgPath).CombinedOutput(); err != nil {
		return fmt.Errorf("hdiutil attach: %w: %s", err, out)
	}
	defer exec.Command("hdiutil", "detach", mnt).Run()

	src := filepath.Join(mnt, appName)
	dst := filepath.Join("/Applications", appName)
	_ = os.RemoveAll(dst)
	if out, err := exec.CommandContext(ctx, "cp", "-R", src, dst).CombinedOutput(); err != nil {
		return fmt.Errorf("cp %s: %w: %s", appName, err, out)
	}
	return nil
}
```

- [ ] **Step 6: 交叉编译 + 回归（Linux 可跑）**

Run: `GOOS=darwin GOARCH=arm64 go vet ./internal/codexdesktop/ && go test ./internal/codexdesktop/`
Expected: vet 通过；现有测试 PASS。

- [ ] **Step 7: 验证（Mac-only）**

- 探测：装了 Codex.app 时 `Detect()` Installed=true + 版本。
- 安装：`EnsureInstalled` 下载安装后 `/Applications/Codex.app` 存在。
- 启动：`Launch(ctx, folder, nil)` 用 `open` 触发 `codex://threads/new?path=...`。

- [ ] **Step 8: Commit**

```bash
git add internal/codexdesktop/
git commit -m "feat(macos): codex desktop detect/install via platform dispatch"
```

### Task 4.6：Codex 运行时 manifest 选择装配（§4.9）

**Files:**
- Create: `internal/codexruntime/bundled_darwin.go`（`//go:build darwin`）
- Modify: 调用 `codexruntime.Ensure` 的桌面流程处（launcher onboarding / `agentctl install-codex`）

> `codexruntime.Ensure` 逻辑（extract/integrity/manifest）本就跨平台。本任务只新增「按 `runtime.GOARCH` 选 darwin manifest 并由 `os.Executable()` 推算路径」。manifest 文件随包在 `.app/Contents/Resources/`（Phase 1 已放入）。

- [ ] **Step 1: 写 darwin manifest 路径解析 `internal/codexruntime/bundled_darwin.go`**

```go
//go:build darwin

package codexruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// BundledDarwinManifestPath returns the path to the architecture-matched codex
// manifest shipped inside the .app bundle (Contents/Resources/codex-manifest-darwin-<arch>.json).
func BundledDarwinManifestPath() (string, error) {
	arch := runtime.GOARCH // arm64 | amd64
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// exe = .../星池指挥官.app/Contents/MacOS/<binary>
	resources := filepath.Join(filepath.Dir(filepath.Dir(exe)), "Resources")
	p := filepath.Join(resources, fmt.Sprintf("codex-manifest-darwin-%s.json", arch))
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("bundled darwin codex manifest not found: %w", err)
	}
	return p, nil
}
```

- [ ] **Step 2: 在桌面 onboarding 流程接入（改 `cmd/launcher` 的 `CodexManifestPath`）**

桌面端 codex 安装由 onboarding orchestrator 驱动（`internal/ui/orchestrator_real.go:593`），其 manifest 路径来自 launcher 注入的 `r.d.CodexManifestPath`。该字段当前在 `cmd/launcher/main.go:710` 设为 `joinExe(installDir, "codex-manifest.json")`。darwin 上需改为按 GOARCH 选 bundle 内的 darwin manifest。

新增平台 helper（cmd/launcher 内）：

`cmd/launcher/codexmanifest_darwin.go`（`//go:build darwin`）：
```go
//go:build darwin

package main

import "github.com/agentserver/agentserver-pkg/internal/codexruntime"

// codexManifestPath resolves the codex manifest for the desktop onboarding flow.
// macOS: architecture-matched manifest inside the .app bundle (Contents/Resources).
func codexManifestPath(installDir string) (string, error) {
	return codexruntime.BundledDarwinManifestPath()
}
```

`cmd/launcher/codexmanifest_other.go`（`//go:build !darwin`）：
```go
//go:build !darwin

package main

// Windows 用打包内 codex-manifest.json（历史行为，不变）。
func codexManifestPath(installDir string) (string, error) {
	return joinExe(installDir, "codex-manifest.json"), nil
}
```

改 `cmd/launcher/main.go:710`：把结构体字面量里的 `CodexManifestPath: joinExe(installDir, "codex-manifest.json"),` 改为引用预解析的变量。在构建该结构体之前加：

```go
	codexManifestPath, err := codexManifestPath(installDir)
	if err != nil {
		return fmt.Errorf("resolve codex manifest: %w", err)
	}
```

并把字段改为 `CodexManifestPath: codexManifestPath,`（注意局部变量与函数同名——把局部变量命名为 `codexManifest` 避免遮蔽，字段写 `CodexManifestPath: codexManifest`）。

> `agentctl install-codex`（`cmd/agentctl/cmd_install_codex.go`）已要求显式 `--manifest`，由调用方传入；桌面 onboarding 不经 agentctl（直接调 `codexruntime.Ensure`），故无需改 agentctl。Linux headless（`internal/headless/runtime.go`）用自己的 manifest 路径，零改动。

- [ ] **Step 3: 隔离处理（清 quarantine）**

在 codexruntime 提取完成后（或 Ensure 返回后），darwin 上对安装出的 codex 二进制清隔离。在桌面调用点 Ensure 之后追加（darwin）：

```go
// darwin-only: 清 Gatekeeper 隔离
_ = clearQuarantine(filepath.Join(destRoot, "bin", "codex"))
```

`clearQuarantine` 放 `<pkg>/quarantine_darwin.go`（`//go:build darwin`）：`exec.Command("xattr","-dr","com.apple.quarantine",path).Run()`。

- [ ] **Step 4: 交叉编译（Linux 可跑）**

Run: `GOOS=darwin GOARCH=arm64 go vet ./internal/codexruntime/ ./cmd/...`
Expected: 通过。

- [ ] **Step 5: 验证（Mac-only）**

在 Mac 上跑 onboarding 的 codex 安装步骤：期望按当前架构下载对应 darwin 包，提取到 `~/.agentserver-app/bin-root/bin/codex`，`codex --version` 可执行，无 quarantine。

- [ ] **Step 6: Commit**

```bash
git add internal/codexruntime/ cmd/
git commit -m "feat(macos): select bundled darwin codex manifest by GOARCH"
```

### Task 4.7：codex 配置 sandbox 小清理（§4.14，低风险）

**Files:**
- Modify: `internal/codex/config.go`（line 107-112）

> 现状（已确认）：`UpdateConfig` 无条件写 `[windows] sandbox = "unelevated"`（line 107-112）。改为仅 windows 写。

- [ ] **Step 1: 写失败测试（Linux 可跑，先确认现状会失败）**

在 `internal/codex/config_test.go`（若无则新建）追加：

```go
func TestUpdateConfigNoWindowsSectionOnNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows-specific")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := UpdateConfig(path, Settings{
		Provider: "agentserver",
		BaseURL:  "http://127.0.0.1:53452/v1",
		WireAPI:  "responses",
	})
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "[windows]") {
		t.Errorf("non-windows config must not emit [windows] section:\n%s", b)
	}
}
```

（import `runtime`/`os`/`path/filepath`/`strings`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/codex/ -run TestUpdateConfigNoWindowsSectionOnNonWindows -v`
Expected: FAIL（当前无条件写 `[windows]`）。

- [ ] **Step 3: 改 `UpdateConfig`（config.go line 107-112）**

替换为：

```go
	if runtime.GOOS == "windows" {
		windows, _ := root["windows"].(map[string]any)
		if windows == nil {
			windows = map[string]any{}
		}
		windows["sandbox"] = defaultString(s.WindowsSandbox, defaultWindowsSandbox)
		root["windows"] = windows
	}
```

> `runtime` 已在 config.go import（若未 import 则补 `"runtime"`）。darwin 按 spec「省略 mac 沙箱键」处理——不写任何 mac 专属沙箱表（macOS codex 默认沙箱行为即可）。若后续确认 mac codex 需要特定键，再加 `case darwin`。

- [ ] **Step 4: 跑测试确认通过 + 全量回归**

Run: `go test ./internal/codex/ -v && go test -race -count=1 ./...`
Expected: codex 测试 PASS；全量不回归。

- [ ] **Step 5: Commit**

```bash
git add internal/codex/config.go internal/codex/config_test.go
git commit -m "fix(codex): emit [windows] sandbox section only on windows"
```

### Task 4.8：自更新 `internal/updater`（§4.10）

**Files:**
- Modify: `internal/updater/service.go`（manifest URL 平台化 + installerCachePath 扩展名平台化）
- Create: `internal/updater/manifesturl_other.go`（`//go:build !darwin`）+ `manifesturl_darwin.go`（`//go:build darwin`）
- Create: `internal/updater/installer_darwin.go`（`//go:build darwin`）
- Create: `internal/updater/replace_darwin.go`（`//go:build darwin`）
- Modify: `internal/updater/installer_other.go`（收窄为 `!windows && !darwin`）
- Modify: `internal/updater/replace_other.go`（收窄为 `!windows && !darwin`）
- Modify: `cmd/launcher/main.go`（`newCompletedUpdater` 用平台 URL）

> 现状（已确认）：`DefaultManifestURL`（service.go line 20）硬编码 `windows/latest.json`；`installerCachePath`（line 329-343）强制 `.exe`；`installer_other.go`/`replace_other.go` 为 `!windows`。

- [ ] **Step 1: manifest URL 平台化**

保留 `DefaultManifestURL` 常量（windows 向后兼容）。新增平台函数：

`internal/updater/manifesturl_other.go`（`//go:build !darwin`）：
```go
//go:build !darwin

package updater

// DefaultManifestURLForPlatform returns the update manifest URL for the current
// platform. Windows/Linux keep the historical windows/ URL (Linux headless uses
// its own flow; this is the desktop fallback).
func DefaultManifestURLForPlatform() string { return DefaultManifestURL }
```

`internal/updater/manifesturl_darwin.go`（`//go:build darwin`）：
```go
//go:build darwin

package updater

// DefaultManifestURLForPlatform returns the macOS update manifest URL.
// 服务端需新增 macos/latest.json（version/url/sha256/size，url 指向 universal dmg）。
func DefaultManifestURLForPlatform() string {
	return "https://assets.agent.cs.ac.cn/agentserver-app/macos/latest.json"
}
```

- [ ] **Step 2: `installerCachePath` 扩展名平台化（service.go line 329-343）**

把「强制 `.exe`」改为仅 windows 强制：

```go
func installerCachePath(cacheDir string, m Manifest) (string, error) {
	u, err := url.Parse(m.URL)
	if err != nil {
		return "", err
	}
	name := filepath.Base(u.Path)
	if name == "." || name == "/" || name == "" {
		name = "agentserver-app-" + m.Version + "-setup" + defaultInstallerExt()
	}
	if !strings.HasSuffix(strings.ToLower(name), defaultInstallerExt()) {
		// 仅 windows 强制 .exe；darwin 保留 dmg/zip 原扩展名。
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
	}
	name = filepath.Base(name)
	return filepath.Join(cacheDir, name), nil
}

// defaultInstallerExt 返回本平台的「缺省」安装包扩展名（无 URL 时的回退命名）。
func defaultInstallerExt() string {
	if runtime.GOOS == "darwin" {
		return ".dmg"
	}
	return ".exe"
}
```

> 加 `"runtime"` import（若未 import）。windows 行为：URL 缺扩展名时回退 `setup.exe`，且强制 `.exe`——与历史一致（历史即 `setup.exe` + 强制 `.exe`）。

- [ ] **Step 3: 改 `newCompletedUpdater`（cmd/launcher/main.go line 339-346）用平台 URL**

```go
func newCompletedUpdater(p paths.Paths) *updater.Service {
	return &updater.Service{
		CurrentVersion: appversion.Version,
		ManifestURL:    updater.DefaultManifestURLForPlatform(),
		CacheDir:       p.UpdatesCacheDir,
		State:          updater.NewStateStore(p.UpdateStateFile),
	}
}
```

> windows：`DefaultManifestURLForPlatform()` 返回 `DefaultManifestURL`（windows/latest.json）——零改动。darwin 返回 macos/latest.json。

- [ ] **Step 4: 收窄 `installer_other.go`/`replace_other.go`**

两文件首行改为 `//go:build !windows && !darwin`。

- [ ] **Step 5: 写 `replace_darwin.go`**

```go
//go:build darwin

package updater

import (
	"fmt"
	"os"
	"path/filepath"
)

// replaceFile swaps a running .app bundle: running Mach-O can't be deleted in
// place, but its bundle directory can be renamed. Old bundle → .old, new in,
// .old removed best-effort on next launch.
func replaceFile(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("replaceFile src: %w", err)
	}
	old := dst + ".old"
	_ = os.RemoveAll(old) // 残留的旧 .old 先清
	if err := os.Rename(dst, old); err != nil {
		return fmt.Errorf("rename old bundle: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		// 回滚
		_ = os.Rename(old, dst)
		return fmt.Errorf("rename new bundle: %w", err)
	}
	// .old 留待下次启动由 launcher 清理（见 Step 7）；此处尽力异步删。
	go func() { _ = os.RemoveAll(old) }()
	return nil
}

// CleanupOldBundles removes leftover *.app.old bundles next to the running app.
// Called by launcher on startup.
func CleanupOldBundles() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	// exe = .../星池指挥官.app/Contents/MacOS/launcher → bundle dir
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	dir := filepath.Dir(bundle)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) == ".old" {
			_ = os.RemoveAll(filepath.Join(dir, name))
		}
	}
}
```

- [ ] **Step 6: 写 `installer_darwin.go`**

```go
//go:build darwin

package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StartInstaller extracts the downloaded archive (zip or dmg) containing the new
// .app, swaps the running bundle, and relaunches. Aligns Windows' cmd.Start()+Release().
func StartInstaller(ctx context.Context, path string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	installedApp := filepath.Dir(filepath.Dir(filepath.Dir(exe))) // 星池指挥官.app
	appName := filepath.Base(installedApp)

	stage, err := os.MkdirTemp("", "agentserver-update-*")
	if err != nil {
		return err
	}

	var newApp string
	switch {
	case strings.HasSuffix(strings.ToLower(path), ".zip"):
		if out, err := exec.CommandContext(ctx, "unzip", "-o", "-q", path, "-d", stage).CombinedOutput(); err != nil {
			return fmt.Errorf("unzip update: %w: %s", err, out)
		}
		newApp = filepath.Join(stage, appName)
	case strings.HasSuffix(strings.ToLower(path), ".dmg"):
		newApp, err = copyAppFromDMG(ctx, path, stage, appName)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown installer archive: %s", path)
	}

	if err := replaceFile(newApp, installedApp); err != nil {
		return err
	}

	// 重启新 bundle 的 launcher（后台托管控制台），本进程随后退出。
	relaunch := filepath.Join(installedApp, "Contents", "MacOS", "launcher")
	cmd := exec.Command(relaunch, "--background")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("relaunch launcher: %w", err)
	}
	return cmd.Process.Release()
}

func copyAppFromDMG(ctx context.Context, dmg, stage, appName string) (string, error) {
	mnt, err := os.MkdirTemp("", "dmg-*")
	if err != nil {
		return "", err
	}
	if out, err := exec.CommandContext(ctx, "hdiutil", "attach", "-nobrowse", "-mountpoint", mnt, dmg).CombinedOutput(); err != nil {
		return "", fmt.Errorf("hdiutil attach: %w: %s", err, out)
	}
	defer exec.Command("hdiutil", "detach", mnt).Run()
	dst := filepath.Join(stage, appName)
	if out, err := exec.CommandContext(ctx, "cp", "-R", filepath.Join(mnt, appName), dst).CombinedOutput(); err != nil {
		return "", fmt.Errorf("cp app from dmg: %w: %s", err, out)
	}
	return dst, nil
}
```

- [ ] **Step 7: launcher 启动时清理 `*.old` 残留 bundle**

在 `cmd/launcher/main.go` 的 `run()` 开头（解析参数后、起服务前）加：

```go
	if runtime.GOOS == "darwin" {
		updater.CleanupOldBundles()
	}
```

> import `runtime`、`updater`（已 import）。非 darwin 无此函数（平台分派）——用 `//go:build darwin` 的薄封装或直接 `if runtime.GOOS=="darwin"` 调一个 darwin 文件里定义的 no-op-on-others 函数。最简：在 `internal/updater` 加 `cleanup_other.go`（`//go:build !darwin`）提供空 `func CleanupOldBundles() {}`，调用点无条件 `updater.CleanupOldBundles()`，去掉 `runtime` 判断。

- [ ] **Step 8: 交叉编译 + 回归（Linux 可跑）**

Run: `go build ./cmd/launcher && GOOS=darwin GOARCH=arm64 go vet ./internal/updater/ ./cmd/launcher && go test ./internal/updater/`
Expected: windows/linux 构建/vet/测试通过；darwin vet 通过。

- [ ] **Step 9: 验证（Mac-only）**

在 Mac 上构造一个「假新版本」dmg/zip（内容为当前 .app），触发更新流程：期望旧 `.app` → `.app.old`，新 `.app` 入位，launcher 重启接管，`.old` 被清理。

- [ ] **Step 10: Commit**

```bash
git add internal/updater/ cmd/launcher/main.go
git commit -m "feat(macos): self-update via dmg/zip replace + relaunch"
```

### Task 4.9：卸载进程清理 `internal/uninstall`（§4.11）

**Files:**
- Create: `internal/uninstall/process_stop_darwin.go`（`//go:build darwin`）
- Modify: `internal/uninstall/process_stop_other.go`（收窄为 `!windows && !darwin`）
- Modify: `internal/uninstall/registry_other.go`（收窄为 `!windows && !darwin`，保持 no-op）

> 现状（已确认）：`process_stop_other.go`（`!windows`）no-op；`registry_other.go` no-op。`stopInstallProcesses(ctx, appDir, names)` 签名。

- [ ] **Step 1: 写 `process_stop_darwin.go`**

```go
//go:build darwin

package uninstall

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// stopInstallProcesses enumerates processes whose executable lives under appDir
// (the .app/Contents/MacOS tree), SIGKILLs them, and polls until they exit.
// 对齐 Windows PowerShell Stop-Process + Wait-Process。
func stopInstallProcesses(ctx context.Context, appDir string, names []string) error {
	pids, err := pidsUnderAppDir(ctx, appDir, names)
	if err != nil {
		return err
	}
	for _, pid := range pids {
		_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
	}
	// 轮询等待退出（最多 ~5s）。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		remaining, err := pidsUnderAppDir(ctx, appDir, names)
		if err != nil {
			return err
		}
		if len(remaining) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("processes under %s did not exit within timeout", appDir)
}

// pidsUnderAppDir returns pids whose comm path is under appDir, OR whose basename
// is in names (broader fallback).
func pidsUnderAppDir(ctx context.Context, appDir string, names []string) ([]int, error) {
	out, err := exec.CommandContext(ctx, "ps", "-eo", "pid=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		comm := strings.Join(fields[1:], " ")
		matched := strings.HasPrefix(comm, appDir)
		if !matched {
			for _, n := range names {
				if filepath.Base(comm) == n {
					matched = true
					break
				}
			}
		}
		if matched {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}
```

- [ ] **Step 2: 收窄 `process_stop_other.go` 与 `registry_other.go`**

两文件首行改为 `//go:build !windows && !darwin`。`registry_other.go` 的 `removeUninstallRegistry` 保持 no-op（spec §4.11：DMG 无「添加/删除程序」注册表）。

- [ ] **Step 3: 交叉编译（Linux 可跑）**

Run: `GOOS=darwin GOARCH=arm64 go vet ./internal/uninstall/ && GOOS=linux go vet ./internal/uninstall/`
Expected: 通过。

- [ ] **Step 4: 验证（Mac-only）**

运行中的 launcher/token-refresher 下触发卸载：期望相关进程被 SIGKILL 并退出，之后 bundle/数据被清理。

- [ ] **Step 5: Commit**

```bash
git add internal/uninstall/
git commit -m "feat(macos): uninstall process cleanup via ps + SIGKILL"
```

---

## Phase 5：菜单栏 + launcher 主循环重构（CGo/库；对应 spec §4.1、§5.1）

> 最高风险阶段。关键架构差异：macOS Cocoa 要求事件循环跑在**主线程**（main goroutine）。Windows 版把消息循环放在 `runtime.LockOSThread` 的 goroutine 里，darwin 不行——必须**让主线程跑 tray，HTTP 控制台跑在 goroutine**（与 Windows 相反）。靠 `//go:build` 分派 + 回归测试保护 Windows 不破。

### Task 5.1：引入 `fyne.io/systray` 依赖

**Files:**
- Modify: `go.mod` / `go.sum`

> 选型（spec §10 已确认）：`fyne.io/systray`——Fyne 团队维护、支持 macOS 原生 runloop、去掉 Linux GTK 依赖、支持所需菜单结构（动态标题、禁用项、分隔符、点击回调）。需要 CGo（spec 已接受 `CGO_ENABLED=1`）。

- [ ] **Step 1: 添加依赖（Linux 上 `go mod tidy` 即可，tidy 会扫描所有平台的 build tag）**

```bash
go get fyne.io/systray@latest
```

> 注：systray 仅在 `//go:build darwin` 文件中被引用，故 Linux 的 `go build ./...` / `go test ./...` 不会编译它，CI 不受影响。`go mod tidy` 会因 darwin build 配置保留该依赖。

- [ ] **Step 2: 验证 Linux 回归不受影响**

Run: `go build ./... && go test -race -count=1 ./...`
Expected: 全 PASS（systray 未被 linux 编译）。

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "build(macos): add fyne.io/systray dependency for menu bar"
```

### Task 5.2：`internal/tray/tray_darwin.go` 实现

**Files:**
- Create: `internal/tray/tray_darwin.go`（`//go:build darwin`，**Mac-only CGo**）
- Modify: `internal/tray/tray_other.go`（收窄为 `//go:build !windows && !darwin`）

> 共享接口（已确认，`tray.go`）：`App{Run(ctx, Actions) error; Update(State); Notify(title,msg) error}`、`New(iconPath) App`、`State{Tooltip,FiveHour,SevenDay}`、`Actions{OpenDashboard,OpenFrontend,OpenSubscription,Quit}`。

- [ ] **Step 1: 写 `tray_darwin.go`**

```go
//go:build darwin

package tray

import (
	"context"
	"fmt"
	"os"
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
	// ctx 取消 → 退出 systray 主循环。
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
	systray.SetTitle("") // 仅图标
	systray.SetTooltip("星池指挥官")

	it := &menuItems{}
	it.dashboard = systray.AddMenuItem("打开控制台", "打开本地控制台")
	frontendTitle := "启动 Codex Desktop"
	if actions.OpenFrontend != nil {
		// 标题按 install mode 可在 Update 中调整；此处默认 Codex Desktop。
	}
	it.frontend = systray.AddMenuItem(frontendTitle, "启动前端")
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
		return // 菜单尚未就绪（onReady 未跑），跳过；下个 tick 会刷新。
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

// templateIconBytes 读取菜单栏 template 图标（同目录 icon-template.png，黑+透明）。
func (a *darwinApp) templateIconBytes() []byte {
	return a.iconBytes("icon-template.png")
}

func (a *darwinApp) iconBytes(name string) []byte {
	candidates := []string{filepath.Join(filepath.Dir(a.iconPath), name), filepath.Join(filepath.Dir(a.iconPath), "..", "Resources", name)}
	for _, p := range candidates {
		if b, err := os.ReadFile(p); err == nil {
			return b
		}
	}
	return nil
}
```

> import 需含 `"os/exec"`（Notify 用）。前端菜单标题按 install mode（Codex Desktop vs 极简）切换：可在 `Update` 中据 state 的 frontend mode 改 `it.frontend.SetTitle(...)`，或由调用方在构建 actions 前确定。本实现默认「启动 Codex Desktop」。

- [ ] **Step 2: 收窄 `internal/tray/tray_other.go`**

首行改为 `//go:build !windows && !darwin`，正文（noopApp）不变。

- [ ] **Step 3: 编译验证（Mac-only）**

Run（Mac）: `GOOS=darwin go build ./internal/tray/`
Expected: 编译通过（CGo/systray 在 Mac 上可用）。

- [ ] **Step 4: 验证（Mac-only）**

写一个临时 main 调 `tray.New(...).Run(ctx, actions)`：期望菜单栏出现图标 + 菜单项（打开控制台/启动前端/打开订阅/灰色额度行/退出），点击各项触发回调，`Notify` 弹通知，`Update` 刷新额度行文本。

- [ ] **Step 5: Commit**

```bash
git add internal/tray/tray_darwin.go internal/tray/tray_other.go
git commit -m "feat(macos): menu bar via fyne.io/systray"
```

### Task 5.3：launcher 主循环重构 —— `runMainLoop` 平台分派（§5.1）

**Files:**
- Create: `cmd/launcher/runapp_other.go`（`//go:build !darwin`）
- Create: `cmd/launcher/runapp_darwin.go`（`//go:build darwin`）
- Modify: `cmd/launcher/main.go`（serve 路径：lines 311-336）

> 现状（已确认）：`runTrayApp`（line 470）把 `app.Run` 放 goroutine；`srv.Serve(ln)` 在主 goroutine（line 332）。darwin 需反过来：tray 在主线程、server 在 goroutine。

- [ ] **Step 1: 写非 darwin 版 `cmd/launcher/runapp_other.go`（保持 Windows/Linux 现状）**

```go
//go:build !darwin

package main

import (
	"context"
	"errors"
	"log"
)

// runMainLoop keeps the existing Windows/Linux behavior: the HTTP server owns the
// main goroutine while the tray runs in a background goroutine.
func runMainLoop(trayRun func(context.Context) error, trayCtx context.Context, serverServe func() error) error {
	go func() {
		if err := trayRun(trayCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("launcher: tray run: %v", err)
		}
	}()
	return serverServe()
}
```

- [ ] **Step 2: 写 darwin 版 `cmd/launcher/runapp_darwin.go`（tray 占主线程）**

```go
//go:build darwin

package main

import (
	"context"
	"errors"
	"log"
	"net/http"
)

// runMainLoop flips the loop on macOS: Cocoa requires the event loop on the OS
// main thread, so the tray blocks the main goroutine while the HTTP server runs
// in a background goroutine. Whichever stops first cancels the other.
func runMainLoop(trayRun func(context.Context) error, trayCtx context.Context, serverServe func() error) error {
	innerCtx, cancel := context.WithCancel(trayCtx)
	defer cancel()

	serverErr := make(chan error, 1)
	go func() { serverErr <- serverServe() }()

	// server 先停 → 取消 tray，让 trayRun 在主线程返回。
	var firstServerErr error
	go func() {
		if err := <-serverErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			firstServerErr = err
			log.Printf("launcher: console server stopped: %v", err)
		}
		cancel()
	}()

	// trayRun 阻塞 OS 主线程（Cocoa/systray 要求）。
	if err := trayRun(innerCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("launcher: tray run: %v", err)
	}
	return firstServerErr
}
```

- [ ] **Step 3: 改 `cmd/launcher/main.go` serve 路径（lines 311-336）**

把：

```go
	trayDone := runTrayApp(trayCtx, trayApp, trayActions)
	defer func() {
		if !stopTrayAndWait(stopTray, trayDone, trayShutdownTimeout) {
			log.Printf("launcher: tray cleanup did not finish within %s", trayShutdownTimeout)
		}
	}()
	go runTrayStatusLoop(trayCtx, trayApp, ctrl, console.ReminderEngine{
		Store: console.NewFileReminderStore(in.Paths.ConsoleNotificationsFile),
	})

	if in.Options.OpenPage {
		runAsyncLauncherAction("open console page", func() error {
			return openBrowser(base + "/")
		})
	}
	if in.Options.OpenFrontend {
		runAsyncLauncherAction("open completed frontend", func() error {
			return ctrl.OpenFrontend(ctx)
		})
	}

	err = srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
```

替换为（保留 trayActions 构建、status loop、open-page/frontend 逻辑，仅改 tray/server 的装配）：

```go
	defer stopTray()
	go runTrayStatusLoop(trayCtx, trayApp, ctrl, console.ReminderEngine{
		Store: console.NewFileReminderStore(in.Paths.ConsoleNotificationsFile),
	})

	if in.Options.OpenPage {
		runAsyncLauncherAction("open console page", func() error {
			return openBrowser(base + "/")
		})
	}
	if in.Options.OpenFrontend {
		runAsyncLauncherAction("open completed frontend", func() error {
			return ctrl.OpenFrontend(ctx)
		})
	}

	return runMainLoop(
		func(c context.Context) error { return trayApp.Run(c, trayActions) },
		trayCtx,
		func() error {
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				return err
			}
			return nil
		},
	)
```

- [ ] **Step 4: 删除现已不用的 `runTrayApp` / `stopTrayAndWait` / `trayShutdownTimeout`（lines 468-494）**

```bash
# 删除 main.go 中：
#   const trayShutdownTimeout = 2 * time.Second
#   func runTrayApp(...) {...}
#   func stopTrayAndWait(...) {...}
```

（手动删除或用编辑器；这些函数在 serve 路径不再被引用。）

- [ ] **Step 5: 交叉编译 + Linux 回归（Linux 可跑：非 darwin 路径编译并测试）**

Run: `go build ./cmd/launcher && go test -race -count=1 ./...`
Expected: Windows/Linux 行为不变，全测试 PASS。

Run: `GOOS=darwin GOARCH=arm64 go vet ./cmd/launcher`
Expected: darwin vet 通过（runapp_darwin.go 不含 CGo，linux 可 vet）。

- [ ] **Step 6: 验证（Mac-only）—— 关键端到端**

在 Mac 上运行 `.app`：
- 期望：菜单栏出现图标（**不**在 Dock 显示，因 `LSUIElement=true`）；控制台 HTTP 起在 `127.0.0.1:<port>`；浏览器可访问。
- 退出菜单：点「退出」→ tray 退出 → server goroutine 停 → 进程干净退出，端口文件清理。
- server 异常停止：tray 应随之退出（cancel 传播）。
- Windows 回归：`make cross-windows` + Windows 上运行，确认 tray 在 goroutine、server 主线程的旧流程未变。

- [ ] **Step 7: Commit**

```bash
git add cmd/launcher/runapp_other.go cmd/launcher/runapp_darwin.go cmd/launcher/main.go
git commit -m "feat(macos): run tray on main thread, server in goroutine (platform split)"
```

---

## Phase 6：端到端联调（仅 Mac；对应 spec §7.2、§7.3、§8.6）

> 全部为 Mac-only 观察型验证。每个任务是一份验收清单，逐项打勾。无 Linux 单测（CGo + 原生集成）。前置：Phase 1–5 全部完成且 `make package-macos` 产出可用 DMG。

### Task 6.1：universal `.app` + DMG 构建

- [ ] **Step 1: 构建**
  Run: `make package-macos`
  Expected: 产出 `dist/macos/星池指挥官-<version>-universal.dmg` + `.sha256`。
- [ ] **Step 2: 验证 universal 架构**
  Run: `lipo -archs dist/macos/stage/星池指挥官.app/Contents/MacOS/launcher`
  Expected: `x86_64 arm64`。
- [ ] **Step 3: 验证签名（ad-hoc）**
  Run: `codesign -dv dist/macos/stage/星池指挥官.app`
  Expected: `Signature=adhoc`（无 `MACOS_SIGN_IDENTITY` 时）。无报错。
- [ ] **Step 4: Gatekeeper 首启**
  挂载 DMG → 拖到 `/Applications` → 右键打开（绕过未公证提示）→ 期望启动 onboarding（不崩溃）。

### Task 6.2：首次启动 onboarding 全流程

- [ ] **Step 1: OAuth 登录** — 双击 `.app` → onboarding 向导 → 完成 ModelServer + AgentServer OAuth（PKCE loopback 回调）。
  Expected: `~/.agentserver-app/secrets.json` 写入 token。
- [ ] **Step 2: codex 运行时安装** — 向导按当前 GOARCH 选 manifest → 下载安装。
  Expected: `~/.agentserver-app/bin-root/bin/codex` 存在且 `--version` 可执行；无 quarantine。
- [ ] **Step 3: codex 配置写入** — 期望 `~/.codex/config.toml` 含本地代理 `base_url=http://127.0.0.1:53452/v1`，**无** `[windows]` 段（§4.14）。
- [ ] **Step 4: 前端安装（默认 Codex Desktop 模式）** — 向导安装 Codex.app。
  Expected: `/Applications/Codex.app` 存在，`codesign --verify` 通过。
- [ ] **Step 5: Quick Action 安装** — 向导安装 Finder 右键。
  Expected: `~/Library/Services/用星池指挥官打开.workflow` 存在；`~/.agentserver-app/open-folder-path.txt` 写入 open-folder 路径。
- [ ] **Step 6: token-refresher 守护启动** — 期望后台守护运行，本地代理 `127.0.0.1:53452/v1` 可用，token 自动刷新。
  Expected: `~/.agentserver-app/token-refresher.lock` 存在；`curl http://127.0.0.1:53452/v1/models` 返回模型列表。
- [ ] **Step 7: 转入常驻** — 向导完成 → 菜单栏图标出现 + 控制台就绪。

### Task 6.3：两种前端模式

- [ ] **Step 1: Codex Desktop 默认模式** — 菜单「启动 Codex Desktop」/打开前端。
  Expected: `codex://threads/new` 经 `open` 启动 Codex.app。
- [ ] **Step 2: 切极简 VS Code 模式** — 改 `~/.agentserver-app/install-mode.json` 为 `{"frontend_mode":"vscode_minimal"}`（或经控制台切换）→ 重启 → 向导装 VS Code（unzip 到 /Applications）+ 安装 vsix + 写 settings（含 osx profile）。
  Expected: `/Applications/Visual Studio Code.app` 存在；菜单「启动极简界面」打开 VS Code with codex 终端。

### Task 6.4：Finder 右键「用星池指挥官打开」

- [ ] **Step 1: 右键文件夹** — Finder 中右键任意文件夹 → 「用星池指挥官打开」。
  Expected: open-folder 二进制被调用 → 确保控制台后台运行 → 打开对应前端进入该文件夹。
- [ ] **Step 2: 非默认安装位置** — 拖到 `~/Applications` 重测；Quick Action 经 `open-folder-path.txt` 仍能定位 open-folder。

### Task 6.5：本地控制台 + 菜单栏 + 更新检查 + 代理/refresher

- [ ] **Step 1: 控制台** — 菜单「打开控制台」→ 浏览器打开本地 Web 仪表盘（登录、额度、本机 slave、工作区）。
- [ ] **Step 2: 菜单栏刷新** — 等 60s（`runTrayStatusLoop` 周期）→ 额度灰色行文本更新；tooltip 更新。
- [ ] **Step 3: 通知** — 触发 `Notify`（如额度提醒）→ 右上角弹通知 + Glass 音。
- [ ] **Step 4: 更新检查** — 控制台「检查更新」→ 命中 `macos/latest.json` → 显示版本（或最新）。
- [ ] **Step 5: model proxy + refresher** — 持续使用 codex → 请求经本地代理；token 临近过期时自动刷新。

### Task 6.6：本机 slave 增启停删

- [ ] **Step 1: 增加 + 启动** — 控制台添加本机 slave（driver-agent/slave-agent 为 universal darwin 二进制）→ 启动。
  Expected: slave 注册写入 `slaves.json`；进程存活核验（`ps comm` 强核验）正确识别。
- [ ] **Step 2: 停止/删除** — 停止 slave → 进程退出；删除 → 清理注册。PID 复用安全（强核验）。

### Task 6.7：自更新替换

- [ ] **Step 1: 构造假新版 dmg** — 用当前 `.app` 打包成「新版」dmg，服务端 `macos/latest.json` 指向它（更高 version）。
- [ ] **Step 2: 触发更新** — 控制台「下载并安装」→ 下载 → `StartInstaller` 解压 → `replaceFile` 把旧 `.app` → `.app.old`，新 `.app` 入位 → launcher 重启接管。
  Expected: 新版运行；`*.old` 在下次启动被 `CleanupOldBundles` 清理；端口文件被新进程接管。

### Task 6.8：卸载

- [ ] **Step 1: 运行卸载** — `agentctl uninstall`（或卸载器）→ `stopInstallProcesses` SIGKILL 所有 `.app/Contents/MacOS` 下进程。
  Expected: 相关进程退出；桌面别名、`~/Library/Services/用星池指挥官打开.workflow`、`~/.agentserver-app/open-folder-path.txt` 被删；`~/.agentserver-app/` 数据按策略清理。

### Task 6.9：回归确认（Linux）

- [ ] **Step 1: 全量单测**
  Run: `go test -race -count=1 ./...`
  Expected: 全 PASS（与基线 27 包一致；Windows/Linux 零回归）。
- [ ] **Step 2: 交叉编译冒烟**
  Run: `GOOS=windows GOARCH=amd64 go build ./... && GOOS=linux GOARCH=amd64 go build ./...`
  Expected: 通过。

---

## Phase 7：文档（对应 spec §8.7、§9）

### Task 7.1：README / 项目描述补 macOS 章节

**Files:**
- Modify: `README.md`（或仓库主描述文档）

- [ ] **Step 1: 在「构建/分发」章节追加 macOS 小节**

内容要点（写成 Markdown）：

- **支持平台：** macOS 11.0+（universal：Apple Silicon arm64 + Intel amd64）。
- **构建（仅 Mac）：** `make package-macos`（产出 `dist/macos/星池指挥官-<version>-universal.dmg` + sha256）。
- **安装：** 挂载 DMG → 拖「星池指挥官.app」到 `/Applications`（标准账户无需密码；非管理员可拖到 `~/Applications`）→ 双击首启 onboarding。
- **Gatekeeper：** v1 未签名/未公证，首启需「右键 → 打开」或 `xattr -dr com.apple.quarantine /Applications/星池指挥官.app`。拿到 Developer ID 后见签名说明。
- **数据位置：** `~/.agentserver-app/`（与 Windows/Linux 一致）。
- **已知限制：** GUI 应用不继承 rc 环境变量（env 持久化走 launchctl + 受管块，§4.5）；无 Mac App Store 沙箱版；无强制开机自启。

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs(macos): add macOS build/install/limitations section"
```

### Task 7.2：签名/公证说明（预留，对应 spec §9）

**Files:**
- Modify: `README.md` 或新增 `packaging/macos/SIGNING.md`

- [ ] **Step 1: 写签名/公证步骤（开关已就绪，拿到证书即用）**

文档内容（Markdown）：

```markdown
## macOS 签名与公证（拿到 Apple Developer ID 后启用）

打包脚本已开关化：设置环境变量即可启用签名+公证，无需改架构。

export MACOS_SIGN_IDENTITY="Developer ID Application: <Your Name (TEAMID)>"
export MACOS_NOTARY_PROFILE="agentserver"   # xcrun notarytool store 预存的 keychain profile
make package-macos

脚本会：
1. codesign --deep --force --options runtime --sign "$MACOS_SIGN_IDENTITY" 对 bundle 与各二进制签名；
2. xcrun notarytool submit <bundle> --keychain-profile ... --wait；
3. xcrun stapler staple <bundle>；
4. DMG 本身也签名 + 公证 + staple。

前置：Apple Developer Program（$99/年）→ Developer ID Application 证书；
xcrun notarytool store-credentials 预存 App Store Connect API key 为 MACOS_NOTARY_PROFILE。
```

- [ ] **Step 2: Commit**

```bash
git add packaging/macos/SIGNING.md   # 或 README.md
git commit -m "docs(macos): document signing/notarization opt-in (spec §9)"
```

---

## Self-Review（writing-plans 自检记录）

**1. Spec coverage（spec 章节 → 任务）：**

| Spec 章节 | 任务 |
|---|---|
| §2 架构/.app 布局/无安装器 | Phase 1（Task 1.1/1.5） |
| §4.1 tray | Task 5.2 |
| §4.2 folderpicker | Task 4.2 |
| §4.3 console 归属 | Task 3.1 |
| §4.4 slave liveness | Task 3.2 |
| §4.5 env | Task 4.1 |
| §4.6 shortcut + Quick Action | Task 4.3（+ Task 1.4 模板） |
| §4.7 vscode | Task 4.4 |
| §4.8 codexdesktop | Task 4.5 |
| §4.9 codex manifest 装配 | Task 4.6 |
| §4.10 updater | Task 4.8 |
| §4.11 uninstall | Task 4.9 |
| §4.12 兄弟命名 | Task 2.1–2.3 |
| §4.13 installmode 可写路径 | Task 2.5 |
| §4.14 codex config sandbox | Task 4.7 |
| §5.1 launcher 主循环 | Task 5.3 |
| §5.2 onboarding 依赖 | Task 4.6 + 4.4 + 4.5 + 4.3（loom 原样可用） |
| §5.3 Quick Action 构造 | Task 1.4 + 4.3 Step 1 |
| §6 打包/分发 | Phase 1 全部 |
| §7 测试策略 | 「测试策略」节 + Phase 6 |
| §9 签名/公证 | Task 7.2 |
| §10 风险 | systray→Task 5.1 已定；launcher 重构→5.3；Codex Desktop URL→4.5 命名常量待确认；env 局限→4.1 已述；codex 沙箱→4.7 省略 |

**2. Placeholder 扫描（已处理）：**
- codex darwin manifest 的 `integrity`/`shasum`/`pinned_version`/`urls` 用 `<FILL: …>` 标注——这是**待计算的具体值**（明确给了计算方式：`npm pack` + sha512-base64 + git blob sha1），非「以后再说」的占位。与 linux 流程一致（hash 必须来自真实 pin 包），不编造。
- Codex Desktop darwin 下载 URL 为命名常量 `darwinCodexDesktopURL`，注释标明 spec §10 的待确认项——机制完整，仅一字面量待维护者确认。
- 原 `<pkg>`/`<调用点所在包>` 占位已消除：codex manifest 装配已精确到 `cmd/launcher/main.go:710`（`CodexManifestPath`）。
- 无 `TBD`/`implement later`/`add error handling` 等空泛步骤。

**3. 类型一致性（跨任务符号核对）：**
- `process.ExeName(name string) string`（2.1 定义）↔ 2.2/2.3/2.5 使用 ✓
- `installmode.PathForWritable(dir) string` / `Path()`（2.5）✓
- `parseUIDFromPS` / `instanceProcessBelongsToCurrentUserWith`（3.1）✓
- `inspectOSProcessWith` / `resolveProcessExe`（3.2，`var` + 平台 `init` 覆盖）✓
- `injectManagedBlock`/`removeManagedBlock`/`managedStartMarker`（4.1 定义↔测试）✓
- `runMainLoop(trayRun, trayCtx, serverServe)`（5.3 两平台同签名）↔ main.go 调用 ✓
- `tray.New`/`App` 接口（tray.go）↔ `darwinApp` 实现（5.2）✓
- `installDesktopPlatform`（4.5：windows dispatch + darwin install_darwin.go 各一次，**不重复**）✓
- `codexManifestPath(installDir)`（4.6：cmd/launcher 两平台文件）↔ main.go:710 ✓

**4. 待确认项汇总（实现期需维护者/真机确认，已在对应任务标注）：**
- codex darwin manifest 的真实 pin 哈希（Task 1.3）。
- Codex Desktop darwin 分发包的确切下载 URL/格式（Task 4.5）。
- macOS codex 沙箱键约定（Task 4.7：当前省略，确认后再加）。
- Loom v0.0.5 darwin release 的 SHA（Task 1.5 fetch-loom-darwin.sh）。

---

## 执行交接（Execution Handoff）

计划已保存至 `docs/superpowers/plans/2026-06-15-macos-commander-implementation.md`。

**两种执行方式：**

**1. Subagent-Driven（推荐）** — 每个任务派发独立子代理实现，任务间两阶段评审，快速迭代。适合本计划任务多、且 Mac-only 项需在真机分阶段确认的特点。

**2. Inline Execution** — 在当前会话内按 `executing-plans` 批量执行，带 checkpoint 评审。

**建议执行顺序与分批：** 严格按 Phase 1 → 7。Phase 1/2/3/4 的 Go 逻辑可在 Linux 上 TDD + 交叉编译验证（CI 友好）；Phase 5（CGo）与 Phase 6（端到端）必须在 Mac 真机执行。Phase 6 各子任务依赖 Phase 1 产出的可运行 `.app`。

**选哪种？**

---
