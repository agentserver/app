# ChatGPT 桌面应用 / Codex Windows 兼容设计

**Date:** 2026-07-10
**Status:** Approved by same-session Codex review after security-hardening Round 3
**Issue:** [agentserver/app#23](https://github.com/agentserver/app/issues/23)
**Scope:** Windows 上 `codex_desktop` 前端模式的识别、安装、启动、错误呈现、打包和测试。

## 背景与已核实事实

OpenAI 已把原 Codex Windows 应用迁移进新的 ChatGPT 桌面应用。新的应用同时提供
Chat、Work 和 Codex；已有 Codex 应用会更新为新 ChatGPT 桌面应用。因此，产品文案和
安装入口不能继续假设用户只会安装名为 `Codex Desktop` 的旧应用，但现有
`codex_desktop` 状态值、命令和 `codex://` 深链仍需保持兼容。

本设计只依赖以下官方来源：

- [Using ChatGPT for Windows](https://help.openai.com/en/articles/9982051-using-chatgpt-for-windows)
  指向 Microsoft Store，并给出企业安装命令：
  `winget.exe install --id=9NT1R1C2HH7J --source=msstore --accept-package-agreements --accept-source-agreements`。
- [Moving to the new ChatGPT desktop app](https://help.openai.com/en/articles/20001276-moving-to-the-new-chatgpt-desktop-app)
  说明新 ChatGPT 桌面应用整合 Chat、Work 与 Codex，旧 Codex 应用会自动更新。
- [Microsoft Store product 9NT1R1C2HH7J](https://apps.microsoft.com/detail/9NT1R1C2HH7J)
  的官方产品元数据给出新应用精确 Package Family Name：
  `OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0`。
- Microsoft 的
  [QueryCurrentDefault](https://learn.microsoft.com/windows/win32/api/shobjidl_core/nf-shobjidl_core-iapplicationassociationregistration-querycurrentdefault)
  文档说明 `AL_EFFECTIVE` 返回当前默认关联的 ProgID；`AssocQueryStringW` 的
  [`pszAssoc` 合同](https://learn.microsoft.com/windows/win32/api/shlwapi/nf-shlwapi-assocquerystringw)
  明确允许 ProgID，而 [`ASSOCSTR_APPID`](https://learn.microsoft.com/windows/win32/api/shlwapi/ne-shlwapi-assocstr)
  返回该文件类型或 URI scheme 当前关联应用的 AppUserModelID；Windows SDK 的
  [`ASSOCF_INIT_FIXED_PROGID`](https://learn.microsoft.com/windows/win32/shell/assocf-str)
  明确要求在输入已经是 ProgID 时禁止再次按当前用户默认关联映射。
- Microsoft 的
  [`IApplicationActivationManager::ActivateForProtocol`](https://learn.microsoft.com/windows/win32/api/shobjidl_core/nf-shobjidl_core-iapplicationactivationmanager-activateforprotocol)
  提供按 AppUserModelID 直接执行协议激活的系统 API。

旧版精确 Package Family Name 为
`OpenAI.Codex_2p2nqsd0c76g0`。实现同时接受这两个精确身份，不猜测其他名称。

## 目标

- 用户可见名称改为“ChatGPT 桌面应用（含 Codex）”或短名称“ChatGPT / Codex”。
- 保留内部 `codex_desktop` 模式值、completed token、配置目录和 `codex://` 深链，避免
  破坏已安装版本与旧 Codex 应用。
- 将 Windows 可用性明确区分为：可用、未安装、已安装但协议缺失、协议目标无效；
  启动阶段另行区分“已安装但启动失败”。
- 安装和打包固定使用新 ChatGPT Microsoft Store Product ID `9NT1R1C2HH7J`。
- 启动前先检查受信包身份和有效协议处理器；以验证后的精确 AppUserModelID 直接激活，
  并在 UI/CLI 中呈现已知失败，不能仅因激活 API 返回成功就报告“已打开”。
- 覆盖旧 Codex、新 ChatGPT、协议缺失、协议损坏和启动器失败。

## 非目标

- 不改名内部 Go 包 `internal/codexdesktop`、前端模式 `codex_desktop` 或已有状态字段。
- 不发明新的 ChatGPT 私有 URI；线程创建继续使用兼容的 `codex://threads/new`。
- 不安装或信任第三方重打包的 ChatGPT/Codex 应用。
- 不证明应用在成功启动确认后会长期健康运行，也不证明深链已创建特定会话；但必须在
  有界时间内观察到归属于精确受信包的应用进程，不能把协议激活调用成功当成启动成功。
- 不在 agentserver 自身升级时强制结束整个 ChatGPT 应用。

## 用户可见命名

统一使用两种文案：

| 场景 | 文案 |
|---|---|
| 安装、配置和诊断说明 | `ChatGPT 桌面应用（含 Codex）` |
| 按钮、菜单等紧凑位置 | `ChatGPT / Codex` |
| 内部兼容标识 | `codex_desktop`、`CodexDesktop*`，保持不变 |

安装向导步骤改为“安装 ChatGPT 桌面应用（含 Codex）”和“准备 ChatGPT / Codex”。完成页、
托盘、控制台和 launcher 的启动标签使用“打开/启动 ChatGPT / Codex”。旧名称只出现在
兼容性注释、旧包身份和历史设计文档中。

## 检测模型

### 数据结构与状态

`internal/codexdesktop.Detected` 扩展为：

```go
type Status string

const (
    StatusReady               Status = "ready"
    StatusNotInstalled        Status = "not_installed"
    StatusSchemeMissing       Status = "scheme_missing"
    StatusSchemeTargetInvalid Status = "scheme_target_invalid"
    StatusLaunchFailed        Status = "launch_failed"
)

type Detected struct {
    Installed         bool
    Version           string
    Status            Status
    PackageFamilyName string
    InstallLocation   string
    AppUserModelID    string
    SchemeRegistered  bool
    SchemeTargetValid bool
}
```

`StatusLaunchFailed` 只用于启动错误，不由静态检测返回。为便于调用方使用
`errors.Is`，定义并保留以下哨兵：

- `ErrNotFound`：未安装；保留现有名称以兼容调用方。
- `ErrSchemeMissing`：精确包已安装，但没有有效 `codex://` 注册。
- `ErrSchemeTargetInvalid`：协议存在，但其生效目标缺失、不可信或无法解析。
- `ErrLaunchFailed`：预检通过，但 Windows 直接协议激活或随后应用启动确认失败。

PowerShell 对预期业务状态统一以退出码 0 输出单个压缩 JSON 对象。Go 端严格解析、校验
枚举和字段一致性；空输出、未知状态、矛盾字段或 PowerShell 非零退出均视为检测器运行
错误，不能降级成“未安装”。

### 包身份

只接受当前用户安装的以下精确 AppX/MSIX Package Family Name：

1. `OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0`（新 ChatGPT 应用，优先）；
2. `OpenAI.Codex_2p2nqsd0c76g0`（旧 Codex 兼容）。

禁止 `Name -like '*ChatGPT*'`、`Name -like '*Codex*'`、路径前缀猜测以及只检查任意
`codex://` 键就判定已安装。PFN 包含发布者身份，精确匹配可避免同名应用冒充。版本和
安装目录只取自匹配到的精确包。

### 生效协议处理器

检测 `codex://` 时只信任 Windows Shell 的生效关联 API 和 MSIX/AppX protocol contract：

1. 通过 `IApplicationAssociationRegistration.QueryCurrentDefault`，以 URL protocol 和
   `AL_EFFECTIVE` 查询当前用户真正生效的 ProgID；不自行重现 UserChoice/HKCR 合并规则；
2. 生效 ProgID 必须符合严格字符/长度约束，随后固定调用
   `AssocQueryStringW(ASSOCF_INIT_FIXED_PROGID = 0x0800, ASSOCSTR_APPID = 21,
   effectiveProgID, NULL, ...)` 查询 AppUserModelID；禁止 flags=0，因为输入已经是上一步的
   生效 ProgID，不能再经当前用户默认关联二次映射；
3. 将 `<PackageFamilyName>!<ApplicationId>` 与当前用户安装的两个精确 PFN反向绑定，再
   通过 `Get-AppxPackageManifest` 验证同一 ApplicationId 确实声明名称严格等于 `codex`
   的 `windows.protocol` contract；
4. 只有且仅有一个精确包/manifest application 与生效 AppUserModelID 匹配时才为 `ready`。
   传统 Win32 command、DelegateExecute 文本和私有 AppModel Repository 注册表布局均不作为
   信任来源，也不会被解析或执行。

处理器验证遵循 fail-closed：

- packaged association 必须同时满足精确 PFN、AppUserModelID/ApplicationId 和 manifest
  protocol declaration 三向绑定；仅有 `AppX*` ProgID 或系统 broker 不足以信任；
- 不读取或执行注册表 command，不把 System32 路径、任意 broker 或任意参数 allowlist
  当作包身份；因此指向 LOLBin 或传统 executable 的关联不能通过；
- 无关联返回 `scheme_missing`；固定 ProgID 查询失败，或有生效关联但缺少唯一可信
  AUMID/manifest 绑定，均返回 `scheme_target_invalid`；
- 输出只含类型化状态和已验证包事实，不含原始关联文本、注册表命令或用户目录路径。

启动时使用检测阶段得到并再次严格校验的 AppUserModelID，调用
`IApplicationActivationManager::ActivateForProtocol` 激活固定生成的
`codex://threads/new`。代码不让 Shell 再次解析可能已变化的关联，不自行解释 `%1`，
也不执行处理器命令，从而收窄预检到使用之间的 TOCTOU。激活后仍按精确包身份确认应用
进程；无法确认即 fail closed。

### 状态决策

| 精确包 | 协议 | 目标 | 状态 | 行为 |
|---|---|---|---|---|
| 无 | 任意 | 任意 | `not_installed` | 提示从 Microsoft Store/精确 winget ID 安装 |
| 有 | 无 | — | `scheme_missing` | 不启动；提示 Repair、Reset、Reinstall |
| 有 | 有 | 无效/不可信 | `scheme_target_invalid` | 不启动；提示 Repair、Reset、Reinstall |
| 有 | 有 | 有效且可信 | `ready` | 允许启动 |

孤立的 `codex://` 注册不能代替受信包身份；即使它指向存在的程序，没有精确包时仍是
`not_installed`。若新旧包同时存在，`ready` 的版本和路径来自生效 AppUserModelID 唯一
绑定的那个包；只有无法形成可信绑定、需要返回 non-ready 诊断时，才按新 ChatGPT、旧
Codex 的顺序选择一个包提供非敏感版本/路径事实。

## 安装与打包

### Go winget 路径

`WingetInstallArgs` 使用精确 Store ID：

```text
install --id=9NT1R1C2HH7J --source=msstore --exact
--accept-package-agreements --accept-source-agreements --disable-interactivity
```

错误文案改为 ChatGPT/Codex，并保留 winget 缺失、Store source 不可用、网络失败和一般
失败的分类。一般失败消息包含精确 Product ID，便于诊断且不依赖本地化产品名。

`EnsureInstalled` 仅对 `not_installed` 执行安装。若已检测到包但协议缺失或损坏，则返回
对应可操作错误，不把“包存在”误报为已就绪，也不自动删除注册表或覆盖用户关联。安装后
必须重新检测到 `ready` 才成功。

### PowerShell 与离线引导器

`packaging/windows/ensure-codex-desktop.ps1` 保留文件名以兼容现有安装流程，但用户文案
改为 ChatGPT/Codex。它与 Go 检测采用相同的两个精确 PFN和状态规则：

- `ready`：跳过安装；
- `not_installed`：先尝试随包引导器，再回退到精确 winget 命令；
- `scheme_missing` / `scheme_target_invalid`：失败并给出 Repair、Reset、Reinstall 指引；
- 安装结束后只有 `ready` 才成功。

随包引导器改为新 Product ID `9NT1R1C2HH7J`，缓存和载荷名改为
`chatgpt-desktop-installer.exe`。下载仍使用 Microsoft 官方
`https://get.microsoft.com/installer/download/<ProductID>` 入口。

供应链防护由打包期和安装期复用的
`verify-chatgpt-desktop-installer.ps1` 统一实施：

- 每次打包刷新到临时文件，成功验证后原子替换共享缓存；
- 最小文件大小和 `MZ` 文件头检查；
- 打包环境可用 PowerShell 时验证 Authenticode；
- 最终用户运行前再次要求 `Get-AuthenticodeSignature` 为 `Valid`；
- 用 `CertGetNameStringW` 按 OID 精确读取签名者主题，要求
  `O=Microsoft Corporation`、`C=US`、`CN=Microsoft Corporation`，并要求 code-signing
  EKU；
- 对签名链执行 online revocation、`ExcludeRoot` 和 15 秒 URL retrieval timeout，并把根
  证书固定为已核验的 Microsoft Root Certificate Authority 2011，原始 DER SHA-256 为
  `847DF6A78497943F27FC72EB93F9A637320A02B561D0A91B09E87A7807ED7C61`；
- 只有链失败项非空且全部为 `NotTimeValid`、同时 Authenticode 有有效时间戳时，才允许
  忽略签名者当前时间失效；时间戳证书必须有 timestamp EKU、通过 online chain，并固定到
  Microsoft Root Certificate Authority 2010，其原始 DER SHA-256 为
  `DF545BF919A2439C36983B54CDFC903DFA4F37D3996D8D84B4C31EEC6F3C163E`。根指纹通过
  PowerShell 5.1 兼容的原始证书 SHA-256 计算；这些 pin 是针对当前官方 Product ID
  `9NT1R1C2HH7J` 引导器链核验并记录的接受集合，只有重新核验该官方载荷链后才能审查、
  更新或追加；
- 本地引导器验证或执行失败时才回退 winget，不能把非零退出码当成功。

此外，打包时为本次下载生成 `chatgpt-desktop-installer.manifest.json`，至少包含固定
`product_id`、固定官方 `source_url`、SHA-256 和字节数。Inno 与 portable 必须把 manifest
和引导器作为一对载荷分发；最终用户运行前先要求 manifest 的 Product ID/URL 精确匹配
编译期常量，再校验文件 size/SHA-256，最后执行上述 Authenticode/证书链验证。manifest
用于防止缓存或载荷错配，安装后 `ready` 检测仍是产品身份的最终判据。

### Inno、portable 与进程处理

`installer.iss`、portable 清单和 shell 打包脚本引用新 Product ID、缓存路径和载荷名。
`test/e2e/windows` 同时识别新旧精确 PFN，并禁止模糊包名匹配。

agentserver 的升级只停止位于 agentserver 安装目录内的自身进程及其受控本地
`codex.exe`。删除现有按 `OpenAI.Codex_*` 路径或命令行强制结束桌面应用的逻辑，也不
新增 `ChatGPT.exe` 名称匹配。ChatGPT 同时承载普通聊天，agentserver 升级不应终止用户
无关会话；配置文件暂时被应用占用时沿用现有原子写入错误处理。

## 启动与错误传播

`Launch` 的顺序固定为：

1. 检查 context；
2. 在 Windows 执行上述预检，只有 `ready` 继续；
3. 创建覆盖启动前快照、原生激活和轮询的单一 10 秒 deadline；以精确
   `PackageFamilyName` 和受信 AppX 包的 canonical/final `InstallLocation` 采集启动前包进程
   快照。候选进程的 image path 必须通过 Windows 文件句柄解析为 canonical/final path，
   严格位于同一安装目录下，并与 launcher 的当前用户 SID 和 SessionId 完全一致；任何
   owner/path/final-path 读取或解析失败均跳过，不退化成纯 lexical path、
   `ChatGPT.exe`/`Codex.exe` 名称或模糊命令行判断；
4. 生成固定 scheme/host/path 的 URL，仅对可选文件夹使用 `url.Values` 编码；
5. 再次校验 `Detected`，用其精确 AppUserModelID 调用
   `IApplicationActivationManager::ActivateForProtocol`；
6. 激活成功后每 250ms 查询一次可信进程。首次 post-launch 采样出现相对 baseline 的新
   可信 PID 即确认启动；若只有 baseline 已存在 PID，则该 PID 必须在两次连续 post-launch
   采样中仍存活，避免把启动前的瞬时状态直接当作本次激活成功；
7. 激活失败、context 取消，或 deadline 内仍没有可信包进程，均包装为
   `ErrLaunchFailed`。

若 Windows 对应用进程隐藏 `ExecutablePath`，实现不得退化成进程名或任意命令行匹配；
应返回无法确认启动的明确错误。该合同证明受信应用在请求后已运行，但不声称窗口必然
可交互、指定线程已创建或应用会长期不崩溃。

包内测试通过私有 launch options 注入 detector、protocol activator、进程 snapshotter 和
sleep；生产入口不可注入，不能绕过预检或启动确认。非 Windows 开发环境保留现有
best-effort opener，Windows 行为由带 build tag 的实现提供。

错误必须沿现有链路返回，同时把诊断 cause 与用户可见文本分离：

- launcher 控制台 `/api/console/open-frontend` 返回非 2xx，Dashboard 的持久错误区显示；
- onboarding `LaunchAndShutdown` 只有启动成功才关闭 HTTP server；
- `cmd/open-folder` 和 `agentctl test-open-folder` 返回非零并打印错误，不能打印
  `opened ...`；
- 依赖注入的 launcher/activator 失败也必须经过同样包装，以便覆盖启动器失败测试；
- HTTP、持久状态和托盘日志只使用固定、按 frontend mode 分类且有长度上限的安全文本，
  不回显 token、用户路径、PowerShell 输出或注册表内容；Go error 仍通过 `Unwrap` 保留
  底层 cause，供 `errors.Is` 和本地诊断使用。

面向用户的四类核心错误如下：

- 未安装：说明未检测到 ChatGPT 桌面应用（含 Codex），给出 Microsoft Store 与精确
  winget Product ID；
- 协议缺失：说明应用已安装但 `codex://` 未注册；
- 目标损坏：说明 `codex://` 已注册但处理器无效；
- 启动失败：明确“ChatGPT / Codex 桌面应用本身无法启动”，建议在 Windows
  “已安装的应用 → ChatGPT → 高级选项”依次尝试 Repair、Reset，仍失败则从 Microsoft
  Store 重新安装，并保留底层错误作为诊断 cause。

所有失败都不得关闭控制台/onboarding，也不得输出“已打开”。

## 测试策略

### Go 单元测试

- JSON 解析：`ready`、`not_installed`、`scheme_missing`、
  `scheme_target_invalid`、未知/矛盾状态、空输出、PowerShell 运行失败；
- Windows 检测源：同时包含两个精确 PFN、`QueryCurrentDefault(..., AL_EFFECTIVE)`、
  `AssocQueryStringW(ASSOCF_INIT_FIXED_PROGID = 0x0800, ASSOCSTR_APPID)` 和可信目标检查；
  源码约束测试必须拒绝 flags=0 的 ProgID 查询，且不包含 UserChoice/HKCR/AppModel 私有
  注册表读取、ChatGPT/Codex 模糊匹配或注册表命令执行；
- packaged association：AppUserModelID、ApplicationId、精确 PFN 与 manifest `codex`
  contract 完整且唯一绑定时可用；任一不匹配或只能解析到传统 executable/LOLBin 时目标
  无效；
- 安装：仅 `not_installed` 调 winget；协议缺失/损坏不安装；安装后非 `ready` 失败；
- winget：参数包含精确新 ID/source/exact/agreement/noninteractive，错误不再引用旧命令；
- 启动：预检四种状态、AUMID 再校验、URL 编码、原生 activator 失败映射
  `ErrLaunchFailed`、取消 context；激活 API 成功但没有可信包进程时仍失败；新进程和
  两次稳定采样的已存在同包进程分别成功；进程 image path 必须经文件句柄解析 final path，
  reparse point/路径前缀碰撞以及 final-path 读取失败不得被确认；
- launcher、onboarding、open-folder：启动失败可见、不 shutdown、不打印成功，且 API/
  持久状态不泄露原始路径、token、PowerShell 或注册表细节。

### 脚本与打包测试

- ensure 脚本同时使用新旧精确 PFN，安装命令只用 `9NT1R1C2HH7J`；
- 新 ChatGPT 引导器路径贯穿 Inno、portable 和打包清单；
- MZ、大小、共享 Authenticode verifier、精确 signer attributes/EKU、online revocation、
  固定 Microsoft root、严格 timestamp 例外和临时文件回滚发布断言；
- 引导器 manifest 的 Product ID、官方 URL、size、SHA-256 生成、分发和运行前校验；
- install.ps1 与 Inno 停进程脚本不包含旧/新桌面包 PFN、`ChatGPT.exe` 或模糊包前缀；
- E2E 检测同时接受两个精确 PFN，禁止 `*Codex*` / `*ChatGPT*`。

### UI 测试

- 安装步骤、frontend name、完成页、Dashboard 和托盘/launcher 文案采用新名称；
- 后端返回协议缺失、目标损坏或启动失败时，错误保持可见；
- 成功刷新不能清除并发启动失败，失败时启动按钮可重试。

### 验证命令

```bash
go test -count=1 ./...
(cd internal/ui/web && npm test)
(cd extensions/agentserver-app && npm run compile)
bash -n scripts/windows-package-common.sh scripts/package-windows.sh scripts/package-windows-zip.sh
```

若环境提供 `pwsh`，额外解析/执行不改变系统状态的 PowerShell 测试；Linux 验证不能替代
真实 Windows 上对新旧应用、损坏协议和原生激活失败的运行验证，交付时必须明确说明。

发布前 Windows 专项验收矩阵为：真实新 ChatGPT 包、真实旧 Codex 包、有效 AUMID/manifest
AppX handler、scheme 缺失、生效关联指向系统 LOLBin或传统 executable、AUMID/PFN/
ApplicationId/manifest 任一不匹配、`ActivateForProtocol` 失败，以及激活 API 成功但没有
可信包进程。
若当前开发环境无法执行此矩阵，代码可以完成并通过自动化门禁，但不得把 Windows 运行
验收标记为已完成，需作为明确的发布前验证项交接。

## 安全不变量

1. 包身份只做两个精确 PFN匹配；安装只用精确 Microsoft Store Product ID。
2. 不执行、`Invoke-Expression`、`Start-Process` 或拼接注册表中的处理器命令。
3. 孤立协议键和传统处理器不构成可信安装；packaged handler 必须由
   AppUserModelID/ApplicationId/manifest contract 三向唯一绑定精确包；System32 路径或
   broker 名称本身不可信。
4. 不自动修改/删除 UserChoice 或协议注册表，修复交给 Windows Repair/Reset/Reinstall。
5. 文件夹只作为 URL query 编码，不进入 shell/命令拼接。
6. 供应链签名验证保持双层并复用同一强策略；验证失败的下载不覆盖上次缓存。
7. agentserver 升级不按宽泛名称、路径片段或桌面包身份终止 ChatGPT/Codex 进程。
8. 检测器异常 fail closed；启动失败必须可见且不得触发 shutdown/成功提示。

## Codex 审查记录

### Round 1 — NOT READY

- **Critical:** 系统激活器规则过宽，可能放行 System32 LOLBin。已改为传统目标仅允许精确
  包目录；broker 只在 packaged metadata 与 manifest 完整反向绑定时接受。
- **Important:** 只等待 `rundll32` 不能确认应用启动。已增加精确包进程快照和 10 秒有界
  确认，helper 成功但无可信进程仍失败。
- **Important:** 未覆盖 AppX 无传统 command/DelegateExecute。已增加 AppUserModelID、
  ApplicationId、精确 PFN 和 manifest protocol contract 的 packaged 分支。
- **Minor:** 已加入 Product ID/source/hash/size manifest 绑定和 Windows 专项验收矩阵。

### Round 2 — READY

- 无 Critical 或 Important 问题。
- 已收敛 Minor：明确 `ErrLaunchFailed` 包含启动确认失败；实施阶段必须优先读取 Shell
  生效关联/AppModel 数据，不能把任意 HKCR 文本值当作 packaged 身份权威。

### Round 3 security-hardening re-review — READY

- 无 Critical、Important 或 Minor 问题。
- 固定 ProgID 查询明确要求 `ASSOCF_INIT_FIXED_PROGID = 0x0800`，查询失败 fail closed；
  source-contract test 必须拒绝 flags=0。
- 安装器 signer/timestamp 根证书 pin 已记录精确 SHA-256 与更新规则；进程确认已收紧为
  Windows 文件句柄解析的 canonical/final path，禁止 lexical fallback。

### 实施阶段安全收敛（最终代码门禁已通过）

- 不再读取 UserChoice、HKCR command 或私有 AppModel Repository；改用
  `QueryCurrentDefault(AL_EFFECTIVE)` 与文档化的 `ASSOCSTR_APPID`，并只接受唯一的
  AUMID/PFN/ApplicationId/manifest 绑定。
- 不再经 Shell 二次解析协议关联；改用验证后的 AppUserModelID 调
  `ActivateForProtocol`，缩小关联 TOCTOU。
- 已有可信进程必须通过两次连续 post-launch 采样；新可信 PID 可在首次 post-launch 采样
  确认。
- 打包期与安装期复用同一 Authenticode verifier，增加精确发布者属性/EKU、online
  revocation、Microsoft root pin 和严格时间戳例外。
- 最终代码 reviewer 已把本节与正文变更作为对已批准设计的安全强化一并复核。

### 最终代码审查记录（同一 `gpt-5.5` / `xhigh` session）

#### Round 1 — NOT READY

- **Critical:** 无。
- **Important:** 固定 ProgID 的 `AssocQueryStringW` APPID 查询异常会逃逸成 detector 运行
  错误；已改为仅捕获该查询并归类 `scheme_target_invalid`，同时保留
  `QueryCurrentDefault` 异常为运行错误。
- **Important:** Authenticode 验证对攻击者可影响的安装器路径使用 `-FilePath`；已改为
  `Get-AuthenticodeSignature -LiteralPath` 并加入回归测试。
- **Minor:** 未安装文案缺少“含 Codex”；已统一使用 `LongDisplayName` 的精确批准文本。

#### Round 2 — NOT READY

- Round 1 三项均关闭；无 Critical 或 Minor。
- **Important:** onboarding 安装 SSE 会原样回显 `EnsureFrontend` 的 winget/PowerShell
  诊断。已在 SSE 边界加入按 frontend mode 分类、最多 256 rune 的安装错误包装，并只通过
  `Unwrap`/`errors.Is` 保留底层 cause。
- reviewer 明确裁定 Go 的 `winget` App Execution Alias/PATH 发现不是独立 finding：该路径
  以当前用户非提权运行，安全边界由精确 Store ID/source/exact 参数和安装后可信包/协议
  复检共同构成。

#### Round 3 — READY

- Critical、Important、Minor 均为无。
- reviewer 确认安装 SSE 不再回显 token、用户路径、PowerShell/winget 输出或注册表文本；
  Codex 哨兵、三种 frontend mode、长度上限、cause identity 和 server 边界均有测试覆盖。

## 兼容与迁移

- 已有 `frontend_mode: "codex_desktop"`、状态 JSON、API 路由、快捷方式和 completed token
  无需迁移。
- 旧 Codex 包在精确 PFN和有效 `codex://` 处理器存在时仍是 `ready`。
- 下次打包开始只分发新 ChatGPT Store 引导器；已缓存旧 Product ID 的文件因缓存路径改变
  不会被误用。
- 旧历史文档保持原样；当前代码、测试和用户界面采用新命名。

## 审查门禁

进入实施计划前，独立 Codex reviewer 必须检查需求覆盖、Windows 行为、安全边界、错误
传播和可测试性。Critical/Important（重大）问题必须修复并重新审查；Minor 可记录到下一
阶段，但最终代码审查前必须处理或明确证明不影响正确性与安全性。
