$script:ChatGPTCodexSnapshotFamilies = @(
    'OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0',
    'OpenAI.Codex_2p2nqsd0c76g0'
)

function Initialize-ChatGPTCodexFinalPathHelper {
    if ('ChatGPTCodex.ProcessFinalPath' -as [type]) { return }

    $null = Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Text;
using Microsoft.Win32.SafeHandles;

namespace ChatGPTCodex {
    public static class ProcessFinalPath {
        private const uint FILE_READ_ATTRIBUTES = 0x00000080;
        private const uint FILE_SHARE_READ = 0x00000001;
        private const uint FILE_SHARE_WRITE = 0x00000002;
        private const uint FILE_SHARE_DELETE = 0x00000004;
        private const uint OPEN_EXISTING = 3;
        private const uint FILE_FLAG_BACKUP_SEMANTICS = 0x02000000;
        private const uint FILE_NAME_NORMALIZED = 0x00000000;
        private const uint VOLUME_NAME_DOS = 0x00000000;

        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true, ExactSpelling = true)]
        private static extern SafeFileHandle CreateFileW(
            string lpFileName,
            uint dwDesiredAccess,
            uint dwShareMode,
            IntPtr lpSecurityAttributes,
            uint dwCreationDisposition,
            uint dwFlagsAndAttributes,
            IntPtr hTemplateFile);

        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true, ExactSpelling = true)]
        private static extern uint GetFinalPathNameByHandleW(
            SafeFileHandle hFile,
            StringBuilder lpszFilePath,
            uint cchFilePath,
            uint dwFlags);

        public static string GetFinalPath(string path, bool directory) {
            if (String.IsNullOrWhiteSpace(path)) {
                throw new ArgumentException("Path is empty", "path");
            }

            uint flags = directory ? FILE_FLAG_BACKUP_SEMANTICS : 0;
            using (SafeFileHandle handle = CreateFileW(
                path,
                FILE_READ_ATTRIBUTES,
                FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                IntPtr.Zero,
                OPEN_EXISTING,
                flags,
                IntPtr.Zero)) {
                if (handle.IsInvalid) {
                    throw new Win32Exception(Marshal.GetLastWin32Error());
                }

                uint capacity = 512;
                StringBuilder buffer = new StringBuilder((int)capacity);
                uint length = GetFinalPathNameByHandleW(
                    handle,
                    buffer,
                    capacity,
                    FILE_NAME_NORMALIZED | VOLUME_NAME_DOS);
                if (length == 0) {
                    throw new Win32Exception(Marshal.GetLastWin32Error());
                }
                if (length >= capacity) {
                    capacity = length + 1;
                    buffer = new StringBuilder((int)capacity);
                    length = GetFinalPathNameByHandleW(
                        handle,
                        buffer,
                        capacity,
                        FILE_NAME_NORMALIZED | VOLUME_NAME_DOS);
                    if (length == 0 || length >= capacity) {
                        throw new Win32Exception(Marshal.GetLastWin32Error());
                    }
                }
                return buffer.ToString();
            }
        }
    }
}
'@ -ErrorAction Stop
}

function Resolve-ChatGPTCodexFinalPath {
    param([Parameter(Mandatory = $true)][string]$Path)

    Initialize-ChatGPTCodexFinalPathHelper
    return [string][ChatGPTCodex.ProcessFinalPath]::GetFinalPath($Path, $false)
}

function Resolve-ChatGPTCodexFinalDirectoryPath {
    param([Parameter(Mandatory = $true)][string]$Path)

    Initialize-ChatGPTCodexFinalPathHelper
    return [string][ChatGPTCodex.ProcessFinalPath]::GetFinalPath($Path, $true)
}

function Add-ChatGPTCodexTrailingDirectorySeparator {
    param([Parameter(Mandatory = $true)][string]$Path)

    return [string]$Path.TrimEnd('\') + '\'
}

function Get-ChatGPTCodexProcessSnapshot {
    $currentSessionId = [uint32][System.Diagnostics.Process]::GetCurrentProcess().SessionId
    $currentUserSid = [string][System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    if ([string]::IsNullOrWhiteSpace($currentUserSid)) {
        throw 'Unable to determine the current Windows user SID'
    }

    $trustedPackages = @()
    foreach ($package in @(Get-AppxPackage -ErrorAction Stop)) {
        $family = [string]$package.PackageFamilyName
        if (-not ($script:ChatGPTCodexSnapshotFamilies -ccontains $family)) { continue }
        $location = [string]$package.InstallLocation
        if ([string]::IsNullOrWhiteSpace($location)) { continue }
        try {
            $finalLocation = Resolve-ChatGPTCodexFinalDirectoryPath -Path $location
            $rootWithSeparator = Add-ChatGPTCodexTrailingDirectorySeparator -Path $finalLocation
        } catch {
            continue
        }
        $trustedPackages += [pscustomobject]@{
            PackageFamilyName    = $family
            FinalInstallLocation = $finalLocation
            RootWithSeparator    = $rootWithSeparator
        }
    }

    $matches = @()
    foreach ($process in @(Get-CimInstance Win32_Process -ErrorAction Stop)) {
        if ($null -eq $process.SessionId -or [uint32]$process.SessionId -ne $currentSessionId) {
            continue
        }
        $executablePath = [string]$process.ExecutablePath
        if ([string]::IsNullOrWhiteSpace($executablePath)) { continue }
        try {
            $finalExecutablePath = Resolve-ChatGPTCodexFinalPath -Path $executablePath
        } catch {
            continue
        }

        $matchedPackage = $null
        foreach ($package in $trustedPackages) {
            if ($finalExecutablePath.StartsWith(
                [string]$package.RootWithSeparator,
                [System.StringComparison]::OrdinalIgnoreCase)) {
                $matchedPackage = $package
                break
            }
        }
        if ($null -eq $matchedPackage) { continue }

        try {
            $owner = Invoke-CimMethod -InputObject $process -MethodName GetOwnerSid -ErrorAction Stop
        } catch {
            continue
        }
        if ($null -eq $owner -or [uint32]$owner.ReturnValue -ne 0) { continue }
        $ownerSid = [string]$owner.Sid
        if ($ownerSid -cne $currentUserSid) { continue }

        $matches += [pscustomobject]@{
            pid                 = [uint32]$process.ProcessId
            package_family_name = [string]$matchedPackage.PackageFamilyName
            install_location    = [string]$matchedPackage.FinalInstallLocation
            owner_sid           = $ownerSid
            session_id          = [uint32]$process.SessionId
        }
    }

    return [pscustomobject]@{
        current_user_sid   = $currentUserSid
        current_session_id = $currentSessionId
        processes           = @($matches)
    }
}
