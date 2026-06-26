# 构建 Windows EXE 安装包

> **注意：如果构建流程发生变化（依赖版本、脚本改动、新增步骤等），请及时更新本文档，保持与实际操作一致。**

## 环境要求

| 依赖 | 最低版本 | 说明 |
|------|---------|------|
| Go | 1.22+ | 交叉编译 Windows 二进制 |
| Node.js | 20+ | 构建前端 UI 和 VS Code 扩展 |
| npm | 10+ | 随 Node.js 安装 |
| Wine | 9.0+ | Linux 上运行 Inno Setup |
| Inno Setup 6 | 6.2+ | 通过 Wine 安装到 `~/.wine/drive_c/Program Files (x86)/Inno Setup 6/` |

### 安装 Inno Setup（Linux 环境）

```bash
# 下载 Inno Setup 安装程序
wget https://jrsoftware.org/download.php/is.exe -O innosetup.exe
# 静默安装到 Wine
wine innosetup.exe /VERYSILENT
```

## 构建步骤

### 一键构建

```bash
cd /root/app
make package
```

`make package` 会依次执行：

1. **`make ui-build`** — 构建 Vue 前端到 `internal/ui/assets/dist/`
2. **`make cross-windows`** — 交叉编译所有 Go 命令到 `dist/windows/*.exe`（launcher, onboarding-server, agentctl, open-folder, uninstall, token-refresher）
3. **`make ext-build`** — 构建 VS Code 扩展 `.vsix`
4. **`bash scripts/package-windows.sh`** — 下载 loom 资产、打包 superpowers/codex-prompts、调用 Inno Setup 生成安装包

### 产物位置

```
packaging/windows/Output/agentserver-app-<VERSION>-setup.exe
```

当前版本号定义在 `scripts/package-windows.sh` 中的 `VERSION` 变量（如 `VERSION="0.1.2"`）。

## 版本与资产管理

loom 二进制版本和 SHA256 校验值在以下文件中维护：

- `scripts/linux-package-common.sh` — Linux 打包用的 loom 资产（含 amd64/arm64）
- `scripts/windows-package-common.sh` — Windows 打包用的 loom 资产
- `packaging/windows/installer.iss` — Inno Setup 脚本中的 loom 缓存路径（需与 `LOOM_RELEASE` 版本一致）

升级 loom 版本时，三个文件需同步更新 `LOOM_RELEASE` 和对应的 SHA256 值。SHA256 可从 GitHub Release 的 `sha256sums.txt` 获取：

```bash
gh release view <version> --repo agentserver/loom --json assets \
  --jq '.assets[] | select(.name=="sha256sums.txt") | .url' | xargs curl -sL
```

## 部署到测试机

```bash
# 传输
scp packaging/windows/Output/agentserver-app-0.1.2-setup.exe Administrator@9.0.16.110:C:/Users/Administrator/Desktop/

# 远程静默安装
ssh Administrator@9.0.16.110 'C:\Users\Administrator\Desktop\agentserver-app-0.1.2-setup.exe /VERYSILENT /SUPPRESSMSGBOXES /NORESTART'
```

安装目录：`C:\Users\Administrator\AppData\Local\Programs\agentserver-app\`

## 常见问题

- **Inno Setup not found** — 确认 Wine 已安装且 `~/.wine/drive_c/Program Files (x86)/Inno Setup 6/ISCC.exe` 存在
- **SHA256 mismatch** — 检查 `sha256sums.txt` 中的值是否正确复制到对应的 `*_SHA256` 变量
- **wine fixme 日志** — `wineusb:query_id` 等 fixme 信息可以忽略，不影响构建
