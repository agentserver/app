; 星池指挥官 v1 Inno Setup script
; Build: ISCC.exe installer.iss
;        (Linux: wine "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" installer.iss)

#define MyAppId "agentserver-vscode"
#define MyAppName "星池指挥官"
#define MyAppVersion "0.1.0"
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
ArchitecturesAllowed=x64
ArchitecturesInstallIn64BitMode=x64

[Languages]
Name: "chinesesimplified"; MessagesFile: "ChineseSimplified.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked
Name: "minimalvscode"; Description: "极简风界面（安装简化 VS Code）"; GroupDescription: "界面模式"; Flags: unchecked

[Files]
; Cross-built Windows binaries
Source: "..\..\dist\windows\launcher.exe";          DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows\onboarding-server.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows\agentctl.exe";          DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows\open-folder.exe";       DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows\uninstall.exe";         DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\dist\windows\token-refresher.exe";   DestDir: "{app}"; Flags: ignoreversion
; Bundled offline payloads
Source: "..\..\dist\cache\rust-v0.136.0\codex-x86_64-pc-windows-msvc.exe"; \
    DestDir: "{app}"; DestName: "codex.exe"; Flags: ignoreversion
Source: "..\..\dist\cache\vscode\1.96.0\VSCodeUserSetup-x64-1.96.0.exe"; \
    DestDir: "{app}"; DestName: "vscode-installer.exe"; Flags: ignoreversion
; Bundled VS Code extension
Source: "..\..\extensions\agentserver-vscode\agentserver-vscode-0.1.0.vsix"; \
    DestDir: "{app}"; DestName: "agentserver-vscode.vsix"; Flags: ignoreversion
; Icon
Source: "icon.ico"; DestDir: "{app}"; Flags: ignoreversion
; License
Source: "LICENSE.zh.txt"; DestDir: "{app}"; Flags: ignoreversion
; Portable installer helpers reused by the Inno setup.
Source: "install.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "ensure-vscode.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "ensure-codex-desktop.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "write-install-mode.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "vscode-manifest.json"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{userdesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; \
      IconFilename: "{app}\icon.ico"; Tasks: desktopicon
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; \
      IconFilename: "{app}\icon.ico"
Name: "{group}\卸载 {#MyAppName}"; Filename: "{uninstallexe}"

[Run]
Filename: "powershell"; \
    Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\write-install-mode.ps1"" -Mode codex_desktop -Path ""{app}\install-mode.json"""; \
    Flags: runhidden waituntilterminated; Check: ShouldInstallCodexDesktop
Filename: "powershell"; \
    Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\ensure-codex-desktop.ps1"""; \
    Flags: runhidden waituntilterminated; Check: ShouldInstallCodexDesktop
Filename: "powershell"; \
    Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\write-install-mode.ps1"" -Mode minimal_vscode -Path ""{app}\install-mode.json"""; \
    Flags: runhidden waituntilterminated; Tasks: minimalvscode
Filename: "powershell"; \
    Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\ensure-vscode.ps1"" -ManifestPath ""{app}\vscode-manifest.json"""; \
    Flags: runhidden waituntilterminated; Tasks: minimalvscode
Filename: "{app}\{#MyAppExeName}"; \
    Description: "{cm:LaunchProgram,{#MyAppName}}"; Flags: nowait postinstall skipifsilent

[Code]
function ShouldInstallCodexDesktop(): Boolean;
begin
  Result := not WizardIsTaskSelected('minimalvscode');
end;

[UninstallRun]
; Best-effort cleanup; ignored if exit non-zero
Filename: "{app}\uninstall.exe"; Parameters: "--silent --keep-install-dir"; \
    Flags: runhidden; RunOnceId: "agentserver-vscode-uninstall"
