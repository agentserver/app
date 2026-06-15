# macOS 签名与公证（拿到 Apple Developer ID 后启用）

打包脚本 [`scripts/package-macos.sh`](../../scripts/package-macos.sh) 已开关化：设置环境变量即可启用签名 + 公证，无需改架构。默认（不设变量）走 ad-hoc 签名（`codesign -s -`），用户首启需「右键 → 打开」绕过 Gatekeeper。

## 前置

1. **Apple Developer Program**（$99/年）→ 申请 **Developer ID Application** 证书，导入钥匙串。
2. **App Store Connect API key**（用于 `xcrun notarytool`），预存到钥匙串：
   ```bash
   xcrun notarytool store-credentials "agentserver" \
     --apple-id "you@example.com" \
     --team-id "<TEAMID>" \
     --password "<app-specific-password>"
   ```
   （`agentserver` 即下面的 `MACOS_NOTARY_PROFILE`。）

## 启用签名 + 公证

```bash
export MACOS_SIGN_IDENTITY="Developer ID Application: Your Name (TEAMID)"
export MACOS_NOTARY_PROFILE="agentserver"
make package-macos
```

`scripts/package-macos.sh` 会依次：

1. `codesign --deep --force --options runtime --sign "$MACOS_SIGN_IDENTITY"` 对 `星池指挥官.app` bundle 与 `Contents/MacOS/` 下各二进制签名（hardened runtime）。
2. `xcrun notarytool submit <bundle> --keychain-profile "$MACOS_NOTARY_PROFILE" --wait` 提交公证并等待结果。
3. `xcrun stapler staple <bundle>` 装订公证票据。
4. 对生成的 **DMG 本身**同样签名 + 公证 + 装订（步骤同上，针对 `.dmg`）。

> 注意：`codesign --deep` 会递归签名 bundle 内所有 Mach-O。`driver-agent` / `slave-agent` / `codex` 这些随包二进制也会被一并签名（它们在 `Contents/MacOS/` 或经 codex runtime 装配到 `~/.agentserver-app/bin-root/`；后者在运行期下载，需单独 `xattr -dr com.apple.quarantine`，签名由上游 @openai/codex 提供）。

## 验证

```bash
codesign -dv --verbose=4 /Applications/星池指挥官.app
spctl --assess --type execute --verbose /Applications/星池指挥官.app
xcrun stapler validate /Applications/星池指挥官.app
```

签名 + 公证 + 装订齐全后，用户双击即可启动，无需「右键 → 打开」或 `xattr` 清隔离。

## 参见

- 设计：`docs/superpowers/specs/2026-06-15-macos-commander-design.md` §9
- 实现计划：`docs/superpowers/plans/2026-06-15-macos-commander-implementation.md` Phase 7
