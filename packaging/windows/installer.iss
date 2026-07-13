; 星池指挥官 v1 Inno Setup script
; Build: ISCC.exe installer.iss
;        (Linux: wine "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" installer.iss)

#define MyAppId "agentserver-app"
#define MyAppName "星池指挥官"
#define MyAppVersion "0.1.9"
#define MyAppPublisher "agentserver"
#define MyAppURL "https://agent.cs.ac.cn"
#define MyAppExeName "launcher.exe"

[Setup]
AppId={{A1B2C3D4-E5F6-4789-ABCD-EF0123456789}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
DefaultDirName={localappdata}\Programs\{#MyAppId}
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
OutputDir=Output
OutputBaseFilename={#MyAppId}-{#MyAppVersion}-setup
SetupIconFile=icon.ico
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
LicenseFile=LICENSE.zh.txt
UninstallDisplayIcon={app}\icon.ico
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible

[Languages]
Name: "chinesesimplified"; MessagesFile: "ChineseSimplified.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"
Name: "codexdesktop"; Description: "ChatGPT 桌面应用（含 Codex）"; GroupDescription: "界面模式"; Flags: exclusive
Name: "opencodedesktop"; Description: "OpenCode Desktop 智能助手"; GroupDescription: "界面模式"; Flags: exclusive unchecked
Name: "minimalvscode"; Description: "极简风界面（安装简化 VS Code）"; GroupDescription: "界面模式"; Flags: exclusive unchecked

[Files]
; Cross-built Windows binaries
Source: "..\..\dist\windows\launcher.exe";          DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows\onboarding-server.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows\agentctl.exe";          DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows\codex-debug-wrapper.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows\open-folder.exe";       DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows\uninstall.exe";         DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows\token-refresher.exe";   DestDir: "{app}"; Flags: ignoreversion
; Bundled offline payloads
Source: "..\..\dist\cache\loom\v0.0.10\driver-agent.windows-amd64.exe"; \
    DestDir: "{app}"; DestName: "driver-agent.exe"; Flags: ignoreversion
Source: "..\..\dist\cache\loom\v0.0.10\slave-agent.windows-amd64.exe"; \
    DestDir: "{app}"; DestName: "slave-agent.exe"; Flags: ignoreversion
Source: "..\..\dist\cache\loom\v0.0.10\driver-skills.tar.gz"; \
    DestDir: "{app}"; DestName: "driver-skills.tar.gz"; Flags: ignoreversion
Source: "..\..\dist\cache\superpowers\driver-superpower-skills.tar.gz"; \
    DestDir: "{app}"; DestName: "driver-superpower-skills.tar.gz"; Flags: ignoreversion
Source: "..\..\dist\cache\loom\v0.0.10\driver-codex-prompts.tar.gz"; \
    DestDir: "{app}"; DestName: "driver-codex-prompts.tar.gz"; Flags: ignoreversion
Source: "..\..\dist\cache\chatgpt-desktop\9PLM9XGG6VKS\ChatGPT Installer.exe"; \
    DestDir: "{app}"; DestName: "chatgpt-desktop-installer.exe"; Flags: ignoreversion
Source: "..\..\dist\cache\chatgpt-desktop\9PLM9XGG6VKS\chatgpt-desktop-installer.manifest.json"; \
    DestDir: "{app}"; DestName: "chatgpt-desktop-installer.manifest.json"; Flags: ignoreversion
Source: "..\..\internal\codexdesktop\detect_windows.ps1"; \
    DestDir: "{app}"; DestName: "codex-desktop-detect.ps1"; Flags: ignoreversion
; Bundled VS Code extension
Source: "..\..\extensions\agentserver-app\agentserver-app-0.1.9.vsix"; \
    DestDir: "{app}"; DestName: "agentserver-app.vsix"; Flags: ignoreversion
; Icon
Source: "icon.ico"; DestDir: "{app}"; Flags: ignoreversion
; License
Source: "LICENSE.zh.txt"; DestDir: "{app}"; Flags: ignoreversion
; Portable installer helpers reused by the Inno setup.
Source: "install.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "install-driver-support.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "ensure-vscode.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "ensure-codex.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "ensure-codex-desktop.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "verify-chatgpt-desktop-installer.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "ensure-opencode-desktop.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "write-install-mode.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "machine.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "codex-manifest.json"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{userdesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; \
      IconFilename: "{app}\icon.ico"; Tasks: desktopicon
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; \
      IconFilename: "{app}\icon.ico"
Name: "{group}\卸载 {#MyAppName}"; Filename: "{uninstallexe}"

[Run]
Filename: "{app}\{#MyAppExeName}"; \
    Description: "{cm:LaunchProgram,{#MyAppName}}"; Flags: nowait postinstall skipifsilent

[Code]
var
  ComputerNamePage: TInputQueryWizardPage;
  PostInstallFailed: Boolean;
  PostInstallFailureMessage: String;

function ShouldInstallCodexDesktop(): Boolean;
begin
  Result := WizardIsTaskSelected('codexdesktop');
end;

function ShouldInstallOpenCodeDesktop(): Boolean;
begin
  Result := WizardIsTaskSelected('opencodedesktop');
end;

function ShouldInstallMinimalVSCode(): Boolean;
begin
  Result := WizardIsTaskSelected('minimalvscode');
end;

function GetMachinePath(): String;
var
  UserProfile: String;
begin
  UserProfile := GetEnv('USERPROFILE');
  if UserProfile = '' then begin
    UserProfile := ExpandConstant('{userappdata}');
  end;
  Result := AddBackslash(UserProfile) + '.agentserver-app\machine.json';
end;

function GetInitialComputerName(): String;
begin
  Result := Trim(GetEnv('COMPUTERNAME'));
  if Result = '' then begin
    Result := 'local-computer';
  end;
end;

function PowerShellQuote(Value: String): String;
begin
  StringChangeEx(Value, '''', '''''', True);
  Result := '''' + Value + '''';
end;

function GetExistingComputerName(): String;
var
  RunnerPath: String;
  ScriptBody: String;
  PowerShellExe: String;
  ResultCode: Integer;
  ExistingName: String;
begin
  Result := '';
  if not FileExists(GetMachinePath()) then begin
    Exit;
  end;

  RegDeleteValue(HKCU, 'Software\AgentServerApp\Installer', 'ExistingComputerName');
  RunnerPath := ExpandConstant('{tmp}\agentserver-read-machine-name.ps1');
  DeleteFile(RunnerPath);
  ScriptBody :=
    '$ErrorActionPreference = ''Stop''' + #13#10 +
    '$machinePath = ' + PowerShellQuote(GetMachinePath()) + #13#10 +
    '$regPath = ''HKCU:\Software\AgentServerApp\Installer''' + #13#10 +
    'try {' + #13#10 +
    '  $utf8NoBom = New-Object System.Text.UTF8Encoding $false' + #13#10 +
    '  $text = [System.IO.File]::ReadAllText($machinePath, $utf8NoBom)' + #13#10 +
    '  $machine = $text | ConvertFrom-Json' + #13#10 +
    '  $machineID = [string]$machine.machine_id' + #13#10 +
    '  $computerName = [string]$machine.computer_name' + #13#10 +
    '  if (-not [string]::IsNullOrWhiteSpace($machineID) -and -not [string]::IsNullOrWhiteSpace($computerName)) {' + #13#10 +
    '    New-Item -Path $regPath -Force | Out-Null' + #13#10 +
    '    New-ItemProperty -Path $regPath -Name ''ExistingComputerName'' -Value $computerName.Trim() -PropertyType String -Force | Out-Null' + #13#10 +
    '  }' + #13#10 +
    '} catch {' + #13#10 +
    '  exit 0' + #13#10 +
    '}' + #13#10;
  if not SaveStringToFile(RunnerPath, ScriptBody, False) then begin
    Exit;
  end;
  PowerShellExe := ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe');
  if not Exec(PowerShellExe, '-NoProfile -ExecutionPolicy Bypass -File "' + RunnerPath + '"', '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then begin
    Exit;
  end;
  if RegQueryStringValue(HKCU, 'Software\AgentServerApp\Installer', 'ExistingComputerName', ExistingName) then begin
    Result := Trim(ExistingName);
  end;
  RegDeleteValue(HKCU, 'Software\AgentServerApp\Installer', 'ExistingComputerName');
end;

function GetChosenComputerName(): String;
begin
  if WizardSilent then begin
    Result := '';
  end else begin
    Result := Trim(ComputerNamePage.Values[0]);
    if Result = '' then begin
      Result := GetInitialComputerName();
    end;
  end;
end;

function SaveUTF8Text(Path, Text: String): Boolean;
var
  Lines: TArrayOfString;
begin
  SetArrayLength(Lines, 1);
  Lines[0] := Text;
  Result := SaveStringsToUTF8FileWithoutBOM(Path, Lines, False);
end;

function FormatDuration(Seconds: Integer): String;
var
  Minutes: Integer;
  Rest: Integer;
begin
  if Seconds < 60 then begin
    Result := IntToStr(Seconds) + '秒';
  end else begin
    Minutes := Seconds div 60;
    Rest := Seconds mod 60;
    if Rest = 0 then begin
      Result := IntToStr(Minutes) + '分钟';
    end else begin
      Result := IntToStr(Minutes) + '分' + IntToStr(Rest) + '秒';
    end;
  end;
end;

function BuildPowerShellRunner(TargetScript: String; TargetArgs: String; ExitPath: String; LogPath: String): String;
begin
  Result :=
    '$ErrorActionPreference = ''Stop''' + #13#10 +
    '$exitPath = ' + PowerShellQuote(ExitPath) + #13#10 +
    '$logPath = ' + PowerShellQuote(LogPath) + #13#10 +
    'try {' + #13#10 +
    '  & ' + PowerShellQuote(TargetScript) + ' ' + TargetArgs + ' *> $logPath' + #13#10 +
    '  $code = $LASTEXITCODE' + #13#10 +
    '  if ($null -eq $code) { $code = 0 }' + #13#10 +
    '  Set-Content -LiteralPath $exitPath -Value $code -Encoding ASCII' + #13#10 +
    '} catch {' + #13#10 +
    '  Set-Content -LiteralPath $exitPath -Value (''ERROR: '' + $_.Exception.Message) -Encoding UTF8' + #13#10 +
    '  exit 1' + #13#10 +
    '}' + #13#10;
end;

procedure UpdateEstimatedProgress(StatusText: String; ElapsedSeconds: Integer; EstimateSeconds: Integer);
var
  DetailText: String;
  RemainingSeconds: Integer;
  Progress: Integer;
begin
  if EstimateSeconds <= 0 then begin
    Progress := 50;
    DetailText := '已用 ' + FormatDuration(ElapsedSeconds) + '，请勿关闭安装器。';
  end else begin
    Progress := (ElapsedSeconds * 95) div EstimateSeconds;
    if Progress > 95 then begin
      Progress := 95;
    end;
    if Progress < 1 then begin
      Progress := 1;
    end;
    RemainingSeconds := EstimateSeconds - ElapsedSeconds;
    if RemainingSeconds > 0 then begin
      DetailText := '已用 ' + FormatDuration(ElapsedSeconds) + '，预计还需 ' + FormatDuration(RemainingSeconds) + '。';
    end else begin
      DetailText := '已用 ' + FormatDuration(ElapsedSeconds) + '，仍在安装，请勿关闭安装器。';
    end;
  end;

  WizardForm.StatusLabel.Caption := StatusText;
  WizardForm.FilenameLabel.Caption := DetailText;
  WizardForm.ProgressGauge.Min := 0;
  WizardForm.ProgressGauge.Max := 100;
  WizardForm.ProgressGauge.Position := Progress;
  WizardForm.Refresh;
end;

procedure RecordPostInstallFailure(Message: String; LogPath: String);
var
  FullMessage: String;
begin
  FullMessage := Message;
  if LogPath <> '' then begin
    FullMessage := FullMessage + '。日志：' + LogPath;
  end;
  if not PostInstallFailed then begin
    PostInstallFailureMessage := FullMessage;
    Log(FullMessage);
    if not WizardSilent then begin
      MsgBox(FullMessage, mbError, MB_OK);
    end;
  end;
  PostInstallFailed := True;
end;

function GetCustomSetupExitCode(): Integer;
begin
  Result := 0;
  if PostInstallFailed then begin
    Result := 1;
  end;
end;

function RunEstimatedPowerShellStep(StepID: String; StatusText: String; ScriptName: String; ScriptArgs: String; EstimateSeconds: Integer): Boolean;
var
  RunnerPath: String;
  ExitPath: String;
  LogPath: String;
  ScriptPath: String;
  ScriptBody: String;
  PowerShellExe: String;
  ResultCode: Integer;
  ElapsedSeconds: Integer;
  ResultText: AnsiString;
begin
  Result := False;
  RunnerPath := ExpandConstant('{tmp}\agentserver-' + StepID + '.ps1');
  ExitPath := ExpandConstant('{tmp}\agentserver-' + StepID + '.exit');
  LogPath := ExpandConstant('{tmp}\agentserver-' + StepID + '.log');
  ScriptPath := ExpandConstant('{app}\' + ScriptName);

  DeleteFile(RunnerPath);
  DeleteFile(ExitPath);
  DeleteFile(LogPath);

  ScriptBody := BuildPowerShellRunner(ScriptPath, ScriptArgs, ExitPath, LogPath);
  if not SaveStringToFile(RunnerPath, ScriptBody, False) then begin
    RecordPostInstallFailure('无法准备安装步骤：' + StatusText, LogPath);
    Exit;
  end;

  PowerShellExe := ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe');
  UpdateEstimatedProgress(StatusText, 0, EstimateSeconds);
  if not Exec(PowerShellExe, '-NoProfile -ExecutionPolicy Bypass -File "' + RunnerPath + '"', '', SW_HIDE, ewNoWait, ResultCode) then begin
    RecordPostInstallFailure('无法启动安装步骤：' + StatusText, LogPath);
    Exit;
  end;

  ElapsedSeconds := 0;
  while not FileExists(ExitPath) do begin
    Sleep(1000);
    ElapsedSeconds := ElapsedSeconds + 1;
    UpdateEstimatedProgress(StatusText, ElapsedSeconds, EstimateSeconds);
  end;

  WizardForm.StatusLabel.Caption := StatusText;
  WizardForm.FilenameLabel.Caption := '正在验证结果...';
  WizardForm.ProgressGauge.Position := 100;
  WizardForm.Refresh;

  if not LoadStringFromFile(ExitPath, ResultText) then begin
    RecordPostInstallFailure('无法读取安装步骤结果：' + StatusText, LogPath);
    Exit;
  end;
  ResultText := Trim(ResultText);
  if ResultText <> '0' then begin
    RecordPostInstallFailure(StatusText + ' 失败：' + ResultText, LogPath);
    Exit;
  end;
  Result := True;
end;

function StopRunningAgentserverProcesses(): Boolean;
var
  RunnerPath: String;
  ScriptBody: String;
  PowerShellExe: String;
  ResultCode: Integer;
begin
  RunnerPath := ExpandConstant('{tmp}\agentserver-stop-processes.ps1');
  DeleteFile(RunnerPath);

  ScriptBody :=
    '$ErrorActionPreference = ''Stop''' + #13#10 +
    '$installDir = ' + PowerShellQuote(ExpandConstant('{app}')) + #13#10 +
    '$installRoot = [System.IO.Path]::GetFullPath($installDir).TrimEnd(''\'')' + #13#10 +
    'if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {' + #13#10 +
    '  $localAppDataRoot = ' + PowerShellQuote(ExpandConstant('{localappdata}\agentserver-app')) + #13#10 +
    '} else {' + #13#10 +
    '  $localAppDataRoot = Join-Path $env:LOCALAPPDATA ''agentserver-app''' + #13#10 +
    '}' + #13#10 +
    '$codexBin = Join-Path $localAppDataRoot ''bin\codex.exe''' + #13#10 +
    '$names = @(''launcher.exe'', ''onboarding-server.exe'', ''agentctl.exe'', ''codex-debug-wrapper.exe'', ''open-folder.exe'', ''token-refresher.exe'', ''driver-agent.exe'', ''slave-agent.exe'', ''codex.exe'')' + #13#10 +
    '$filter = {' + #13#10 +
    '  $exePath = [string]$_.ExecutablePath' + #13#10 +
    '  $exe = ""' + #13#10 +
    '  if (-not [string]::IsNullOrWhiteSpace($exePath)) {' + #13#10 +
    '    $exe = [System.IO.Path]::GetFullPath($exePath)' + #13#10 +
    '  }' + #13#10 +
    '  $inInstallDir = ($exe -ne "") -and ($names -contains $_.Name) -and $exe.StartsWith($installRoot + ''\'', [System.StringComparison]::OrdinalIgnoreCase)' + #13#10 +
    '  $isLocalCodex = ($exe -ne "") -and ($_.Name -eq ''codex.exe'') -and ($exe -ieq $codexBin)' + #13#10 +
    '  $inInstallDir -or $isLocalCodex' + #13#10 +
    '}' + #13#10 +
    '$procs = @(Get-CimInstance Win32_Process | Where-Object $filter)' + #13#10 +
    'foreach ($p in $procs) {' + #13#10 +
    '  Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue' + #13#10 +
    '}' + #13#10 +
    '$deadline = (Get-Date).AddSeconds(8)' + #13#10 +
    'do {' + #13#10 +
    '  Start-Sleep -Milliseconds 250' + #13#10 +
    '  $remaining = @(Get-CimInstance Win32_Process | Where-Object $filter)' + #13#10 +
    '} while ($remaining.Count -gt 0 -and (Get-Date) -lt $deadline)' + #13#10;

  if not SaveStringToFile(RunnerPath, ScriptBody, False) then begin
    Result := False;
    Exit;
  end;

  PowerShellExe := ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe');
  Result := Exec(PowerShellExe, '-NoProfile -ExecutionPolicy Bypass -File "' + RunnerPath + '"', '', SW_HIDE, ewWaitUntilTerminated, ResultCode) and (ResultCode = 0);
end;

procedure DeleteObsoleteBundledPayloads();
begin
  DeleteFile(ExpandConstant('{app}\codex.exe'));
  DeleteFile(ExpandConstant('{app}\codex-desktop-installer.exe'));
  DeleteFile(ExpandConstant('{app}\vscode-installer' + '.exe'));
  DeleteFile(ExpandConstant('{app}\vscode-manifest' + '.json'));
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  if StopRunningAgentserverProcesses() then begin
    Result := '';
  end else begin
    Result := '无法关闭正在运行的星池指挥官后台程序。请关闭后重试。';
  end;
end;

procedure InitializeWizard();
var
  ExistingComputerName: String;
begin
  ComputerNamePage := CreateInputQueryPage(
    wpSelectDir,
    '电脑名称',
    '设置这台电脑在星池指挥官中的名称',
    '默认读取已有电脑名称；安装时可修改，machine_id 会保持不变。');
  ComputerNamePage.Add('电脑名称:', False);
  ExistingComputerName := GetExistingComputerName();
  if ExistingComputerName <> '' then begin
    ComputerNamePage.Values[0] := ExistingComputerName;
  end else begin
    ComputerNamePage.Values[0] := GetInitialComputerName();
  end;
end;

function NextButtonClick(CurPageID: Integer): Boolean;
begin
  Result := True;
  if (CurPageID = wpSelectTasks) and
     (not WizardIsTaskSelected('codexdesktop')) and
     (not WizardIsTaskSelected('opencodedesktop')) and
     (not WizardIsTaskSelected('minimalvscode')) then begin
    MsgBox('请选择一种界面模式。', mbError, MB_OK);
    Result := False;
  end;
  if (CurPageID = ComputerNamePage.ID) and (Trim(ComputerNamePage.Values[0]) = '') then begin
    MsgBox('电脑名称不能为空。', mbError, MB_OK);
    Result := False;
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  ModePath: String;
  MachinePath: String;
  ComputerName: String;
  ComputerNamePath: String;
  MachineArgs: String;
begin
  if CurStep <> ssPostInstall then begin
    Exit;
  end;

  DeleteObsoleteBundledPayloads();

  MachinePath := GetMachinePath();
  ComputerName := GetChosenComputerName();
  MachineArgs := '-MachinePath ' + PowerShellQuote(MachinePath);
  if ComputerName = '' then begin
    MachineArgs := MachineArgs + ' -PreserveExistingComputerName';
  end else begin
    ComputerNamePath := ExpandConstant('{tmp}\agentserver-machine-name.txt');
    DeleteFile(ComputerNamePath);
    if not SaveUTF8Text(ComputerNamePath, ComputerName) then begin
      RecordPostInstallFailure('无法保存电脑名称。', '');
      Exit;
    end;
    MachineArgs := MachineArgs + ' -ComputerNamePath ' + PowerShellQuote(ComputerNamePath);
  end;
  if not RunEstimatedPowerShellStep('machine', '正在初始化电脑名称...', 'machine.ps1',
      MachineArgs, 10) then begin
    Exit;
  end;

  if not RunEstimatedPowerShellStep('codex-runtime', '正在从国内 npm 镜像准备 Codex 运行时...', 'ensure-codex.ps1',
      '-ManifestPath ' + PowerShellQuote(ExpandConstant('{app}\codex-manifest.json')), 300) then begin
    Exit;
  end;

  if not RunEstimatedPowerShellStep('driver-support', '正在安装 driver skills 和 Codex 指令...', 'install-driver-support.ps1',
      '-InstallDir ' + PowerShellQuote(ExpandConstant('{app}')), 20) then begin
    Exit;
  end;

  ModePath := ExpandConstant('{app}\install-mode.json');
  if ShouldInstallOpenCodeDesktop then begin
    if not RunEstimatedPowerShellStep('opencode-mode', '正在准备 OpenCode Desktop 模式...', 'write-install-mode.ps1',
        '-Mode ' + PowerShellQuote('opencode_desktop') + ' -Path ' + PowerShellQuote(ModePath), 10) then begin
      Exit;
    end;
    if not RunEstimatedPowerShellStep('opencode-install', '正在下载并安装 OpenCode Desktop（请勿关闭）...', 'ensure-opencode-desktop.ps1',
        '', 900) then begin
      Exit;
    end;
  end else if ShouldInstallCodexDesktop then begin
    if not RunEstimatedPowerShellStep('codex-mode', '正在准备 ChatGPT / Codex 模式...', 'write-install-mode.ps1',
        '-Mode ' + PowerShellQuote('codex_desktop') + ' -Path ' + PowerShellQuote(ModePath), 10) then begin
      Exit;
    end;
    if not RunEstimatedPowerShellStep('codex-install', '正在安装 ChatGPT 桌面应用（含 Codex；请在弹出的安装器中完成安装，请勿关闭）...', 'ensure-codex-desktop.ps1',
        '', 900) then begin
      Exit;
    end;
  end else if ShouldInstallMinimalVSCode then begin
    if not RunEstimatedPowerShellStep('vscode-mode', '正在准备极简风模式...', 'write-install-mode.ps1',
        '-Mode ' + PowerShellQuote('minimal_vscode') + ' -Path ' + PowerShellQuote(ModePath), 10) then begin
      Exit;
    end;
    if not RunEstimatedPowerShellStep('vscode-install', '正在安装极简 VS Code（可能需要几分钟，请勿关闭）...', 'ensure-vscode.ps1',
        '', 300) then begin
      Exit;
    end;
  end else begin
    RecordPostInstallFailure('请选择一种界面模式。', '');
    Exit;
  end;
end;

[UninstallRun]
; Best-effort cleanup; ignored if exit non-zero
Filename: "{app}\uninstall.exe"; Parameters: "--silent --keep-install-dir"; \
    Flags: runhidden; RunOnceId: "agentserver-app-uninstall"
