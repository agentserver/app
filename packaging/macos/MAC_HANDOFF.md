# macOS 移植 — Mac 端交接清单 (Handoff)

> Linux 端（CI 可跑）的全部 Go 逻辑已实现并通过测试（`go test -race ./...` 38 包全绿；windows/linux/darwin-非CGo 交叉编译通过；gofmt/vet 干净）。以下是**必须在 Mac 上完成**的事项，按顺序执行。
>
> 分支：`feature/macos-implementation`。设计：`docs/superpowers/specs/2026-06-15-macos-commander-design.md`。实现计划：`docs/superpowers/plans/2026-06-15-macos-commander-implementation.md`。

## 1. 依赖与 darwin CGo 编译（阻塞一切）

- [ ] `go mod tidy` — 把 `fyne.io/systray` 从 `// indirect` 提升为 direct（Linux 无法做：该 import 在 `//go:build darwin` 文件里）。验证：`grep fyne.io/systray go.mod` 无 `// indirect`。
- [ ] `GOOS=darwin go vet ./cmd/launcher ./internal/tray` — 这两个 CGo/systray 包在 Linux 无法编译，Mac 上必须通过。修复任何编译错误（systray API 已核对 v1.12.2，理论无误）。
- [ ] `GOOS=darwin go build ./...` — 全量 darwin 编译通过。

## 2. 填充资源（构建前）

- [ ] **codex manifest 哈希** — `packaging/macos/codex-manifest-darwin-{arm64,amd64}.json` 各 6 个 `<<FILL>>` 字段：`pinned_version`、`pinned.integrity`（sha512-base64）、`pinned.shasum`（40-hex git blob sha1）、3 个 `pinned.urls[]` 镜像。用 `npm pack @openai/codex@<ver>` 取包，`shasum -a 512 … | xxd -r -p | base64` 算 integrity，`git hash-object` 算 shasum。每个 arch 一套。
- [ ] **确认 Codex Desktop 下载 URL** — `internal/codexdesktop/install_darwin.go` 的 `darwinCodexDesktopURL`（当前是占位猜测，标了 spec §10）。发布前核对 OpenAI 真实 darwin 资产地址/格式（dmg/zip）。
- [ ] **图标**：
  - `make macos-icon` → `packaging/macos/icon.icns`（`image/icon.png` 经 sips+iconutil）。
  - `cp image/icon.png packaging/macos/icon.png`（`package-macos.sh` 引用但脚本不生成）。
  - 提供 `packaging/macos/icon-template.png`（菜单栏 template image：黑+透明，~22px；`package-macos.sh` 引用）。
- [ ] **Finder Quick Action** — 用 Automator 制作 `packaging/macos/用星池指挥官打开.workflow`（快速操作，接收「文件或文件夹」，运行 shell）。shell 内容见实现计划 Task 1.4 / 4.3（读 `~/.agentserver-app/open-folder-path.txt` 间接定位 open-folder，位置无关）。无脚本生成它，必须手工。

## 3. 构建分发物

- [ ] `make package-macos` → 产出 `dist/macos/星池指挥官-<version>-universal.dmg` + `.sha256`。需先完成 1–2。
- [ ] 验证 universal：`lipo -archs …/星池指挥官.app/Contents/MacOS/launcher` → `x86_64 arm64`。
- [ ] ad-hoc 签名：`codesign -dv …/星池指挥官.app` → `Signature=adhoc`。

## 4. 端到端验收（spec §7.3 矩阵）

按 `docs/superpowers/plans/2026-06-15-macos-commander-implementation.md` Phase 6 全部子任务逐项验证：

- [ ] 首启 onboarding（OAuth 登录、codex 安装、codex 配置写入且**无** `[windows]` 段、前端安装、Quick Action 安装、token-refresher 守护）。
- [ ] Codex Desktop 默认模式 + 极简 VS Code 模式切换。
- [ ] Finder 右键「用星池指挥官打开」（默认 `/Applications` 与 `~/Applications` 两种安装位置）。
- [ ] 本地控制台 + 菜单栏（图标/菜单/通知/额度刷新）+ 更新检查（命中 `macos/latest.json`）+ model proxy/refresher。
- [ ] 本机 slave 增启停删（强核验 PID 归属/exe）。
- [ ] 自更新替换（`.app` → `.app.old` → 新版入位 → 重启 → `.old` 清理）。
- [ ] 卸载（SIGKILL `.app/Contents/MacOS` 下进程 + 清别名/workflow/数据）。

## 5. 回归确认

- [ ] `go test -race -count=1 ./...` 全绿（Linux 基线已绿；Mac 上再确认）。
- [ ] Windows/Linux 流程零回归（构建标签分派 + 回归测试保护；Linux 端已验证）。

## 6. 发布（可选，拿到 Apple Developer ID 后）

- [ ] 按 [`SIGNING.md`](SIGNING.md) 设 `MACOS_SIGN_IDENTITY` + `MACOS_NOTARY_PROFILE`，`make package-macos` 产出签名 + 公证 + 装订的 DMG。

---

## 已知设计决策与限制（Mac 上留意）

- **env 持久化契合度最弱**（§4.5）：`launchctl setenv` 只对当前 GUI 会话生效，rc 受管块对新终端生效；GUI 应用不继承 rc。对 launcher 显式 spawn 的子进程够用。
- **菜单栏通知**走 `osascript`（systray 通知支持参差）。
- **`ps -o comm=`** 在现代 macOS 返回完整路径（强核验依赖此）；若遇截断，考虑改 `args=`。
- **Darwin 单测缺失**：tray/installer/replace/uninstall 的 darwin 实现无单测（CGo/原生命令，与 Windows 实现一致的既有模式），靠端到端验收兜底。
- **Codex macOS 沙箱键**：当前省略（§4.14）；确认 codex darwin 沙箱约定后再加。
