$script:ChatGPTCodexPackageFamilies = @(
    'OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0',
    'OpenAI.Codex_2p2nqsd0c76g0'
)

function New-ChatGPTCodexDetection {
    param(
        [Parameter(Mandatory = $true)][string]$Status,
        [bool]$Installed = $false,
        [string]$Version = '',
        [string]$PackageFamilyName = '',
        [string]$InstallLocation = '',
        [string]$AppUserModelID = '',
        [bool]$SchemeRegistered = $false,
        [bool]$SchemeTargetValid = $false
    )

    [pscustomobject]@{
        status                  = $Status
        installed               = $Installed
        version                 = $Version
        package_family_name     = $PackageFamilyName
        install_location        = $InstallLocation
        app_user_model_id       = $AppUserModelID
        scheme_registered       = $SchemeRegistered
        scheme_target_valid     = $SchemeTargetValid
    }
}

function Get-InstalledChatGPTCodexPackages {
    return @(Get-AppxPackage -ErrorAction Stop | Where-Object {
        $script:ChatGPTCodexPackageFamilies -ccontains [string]$_.PackageFamilyName
    })
}

function Get-EffectiveCodexProgId {
    if (-not ('ChatGPTCodex.ApplicationAssociation' -as [type])) {
        $null = Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

namespace ChatGPTCodex {
    public enum AssociationType {
        FileExtension = 0,
        UrlProtocol = 1,
        StartMenuClient = 2,
        MimeType = 3
    }

    public enum AssociationLevel {
        Machine = 0,
        Effective = 1,
        User = 2
    }

    public enum AssocString {
        AppUserModelID = 21
    }

    [ComImport]
    [Guid("4e530b0a-e611-4c77-a3ac-9031d022281b")]
    [InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    internal interface IApplicationAssociationRegistration {
        [PreserveSig]
        int QueryCurrentDefault(
            [MarshalAs(UnmanagedType.LPWStr)] string query,
            AssociationType queryType,
            AssociationLevel queryLevel,
            [MarshalAs(UnmanagedType.LPWStr)] out string association);
    }

    [ComImport]
    [Guid("591209C7-767B-42B2-9FBA-44EE4615F2C7")]
    internal class ApplicationAssociationRegistration {
    }

    public static class ApplicationAssociation {
        private const int NoAssociation = unchecked((int)0x80070483);
        private const uint ASSOCF_INIT_FIXED_PROGID = 0x0800;

        [DllImport("Shlwapi.dll", CharSet = CharSet.Unicode, ExactSpelling = true)]
        private static extern int AssocQueryStringW(
            uint flags,
            AssocString str,
            string association,
            string extra,
            System.Text.StringBuilder output,
            ref uint outputLength);

        public static string QueryCurrentDefault(string protocol) {
            IApplicationAssociationRegistration registration =
                (IApplicationAssociationRegistration)new ApplicationAssociationRegistration();
            try {
                string association;
                int result = registration.QueryCurrentDefault(
                    protocol,
                    AssociationType.UrlProtocol,
                    AssociationLevel.Effective,
                    out association);
                if (result == NoAssociation) {
                    return String.Empty;
                }
                Marshal.ThrowExceptionForHR(result);
                return association ?? String.Empty;
            } finally {
                Marshal.FinalReleaseComObject(registration);
            }
        }

        public static string QueryAppUserModelID(string progId) {
            if (String.IsNullOrWhiteSpace(progId)) {
                return String.Empty;
            }
            uint capacity = 4096;
            var output = new System.Text.StringBuilder((int)capacity);
            int result = AssocQueryStringW(
                ASSOCF_INIT_FIXED_PROGID,
                AssocString.AppUserModelID,
                progId,
                null,
                output,
                ref capacity);
            if (result == NoAssociation) {
                return String.Empty;
            }
            Marshal.ThrowExceptionForHR(result);
            return output.ToString();
        }
    }
}
'@ -ErrorAction Stop
    }

    return [string][ChatGPTCodex.ApplicationAssociation]::QueryCurrentDefault('codex')
}

function Get-EffectiveAssociationAppUserModelId {
    param([Parameter(Mandatory = $true)][string]$EffectiveProgId)

    return [string][ChatGPTCodex.ApplicationAssociation]::QueryAppUserModelID($EffectiveProgId)
}

function Get-CodexProtocolApplications {
    param([Parameter(Mandatory = $true)]$Package)

    $manifest = Get-AppxPackageManifest -Package $Package.PackageFullName -ErrorAction Stop
    foreach ($application in @($manifest.SelectNodes("//*[local-name()='Application']"))) {
        $declaresCodex = $false
        foreach ($extension in @($application.SelectNodes(".//*[local-name()='Extension' and @Category='windows.protocol']"))) {
            foreach ($protocol in @($extension.SelectNodes(".//*[local-name()='Protocol']"))) {
                if ([string]$protocol.GetAttribute('Name') -ceq 'codex') {
                    $declaresCodex = $true
                }
            }
        }
        if (-not $declaresCodex) { continue }

        $applicationID = [string]$application.GetAttribute('Id')
        if ([string]::IsNullOrWhiteSpace($applicationID)) { continue }
        [pscustomobject]@{
            ApplicationID = $applicationID
            AppUserModelID = [string]$Package.PackageFamilyName + '!' + $applicationID
        }
    }
}

function Get-CodexProtocolCapablePackages {
    param([Parameter(Mandatory = $true)][AllowEmptyCollection()][object[]]$Packages)

    $capable = @()
    foreach ($package in $Packages) {
        if (@(Get-CodexProtocolApplications -Package $package).Count -gt 0) {
            $capable += $package
        }
    }
    return $capable
}

function Find-CodexProtocolPackageByAppUserModelID {
    param(
        [Parameter(Mandatory = $true)][object[]]$Packages,
        [Parameter(Mandatory = $true)][string]$EffectiveProgId,
        [Parameter(Mandatory = $true)][string]$EffectiveAppUserModelID
    )

    $matches = @()
    foreach ($package in $Packages) {
        $family = [string]$package.PackageFamilyName
        if (-not ($script:ChatGPTCodexPackageFamilies -ccontains $family)) { continue }
        foreach ($application in @(Get-CodexProtocolApplications -Package $package)) {
            if ([string]$application.AppUserModelID -ceq $EffectiveAppUserModelID) {
                $matches += [pscustomobject]@{
                    Package        = $package
                    ApplicationID  = [string]$application.ApplicationID
                    AppUserModelID = [string]$application.AppUserModelID
                    ProgId         = $EffectiveProgId
                }
            }
        }
    }

    # More than one owner is ambiguous. Do not resolve that ambiguity by package preference.
    if ($matches.Count -ne 1) { return $null }
    return $matches[0]
}

function Get-DiagnosticChatGPTCodexPackage {
    param([Parameter(Mandatory = $true)][object[]]$Packages)

    foreach ($family in $script:ChatGPTCodexPackageFamilies) {
        $match = @($Packages | Where-Object {
            [string]$_.PackageFamilyName -ceq $family
        } | Select-Object -First 1)
        if ($match.Count -gt 0) { return $match[0] }
    }
    return $null
}

function Get-ChatGPTCodexDetection {
    $packages = @(Get-InstalledChatGPTCodexPackages)
    $capablePackages = @(Get-CodexProtocolCapablePackages -Packages $packages)
    $effectiveProgId = Get-EffectiveCodexProgId
    $schemeRegistered = -not [string]::IsNullOrWhiteSpace($effectiveProgId)

    if ($capablePackages.Count -eq 0) {
        return New-ChatGPTCodexDetection -Status 'not_installed' `
            -SchemeRegistered $schemeRegistered
    }

    $mapping = $null
    if ($schemeRegistered -and $effectiveProgId -cmatch '^[A-Za-z0-9._-]{1,255}$') {
        $effectiveAppUserModelID = ''
        try {
            $effectiveAppUserModelID = Get-EffectiveAssociationAppUserModelId `
                -EffectiveProgId $effectiveProgId
        } catch {
            $effectiveAppUserModelID = ''
        }
        if (-not [string]::IsNullOrWhiteSpace($effectiveAppUserModelID)) {
            $mapping = Find-CodexProtocolPackageByAppUserModelID -Packages $capablePackages `
                -EffectiveProgId $effectiveProgId `
                -EffectiveAppUserModelID $effectiveAppUserModelID
        }
    }
    if ($null -ne $mapping) {
        $package = $mapping.Package
        return New-ChatGPTCodexDetection -Status 'ready' -Installed $true `
            -Version ([string]$package.Version) `
            -PackageFamilyName ([string]$package.PackageFamilyName) `
            -InstallLocation ([string]$package.InstallLocation) `
            -AppUserModelID ([string]$mapping.AppUserModelID) `
            -SchemeRegistered $true -SchemeTargetValid $true
    }

    # Preference order is diagnostic only; it never decides which package is ready.
    $diagnosticPackage = Get-DiagnosticChatGPTCodexPackage -Packages $capablePackages
    $diagnosticVersion = [string]$diagnosticPackage.Version
    $diagnosticFamily = [string]$diagnosticPackage.PackageFamilyName
    $diagnosticLocation = [string]$diagnosticPackage.InstallLocation
    if (-not $schemeRegistered) {
        return New-ChatGPTCodexDetection -Status 'scheme_missing' -Installed $true `
            -Version $diagnosticVersion -PackageFamilyName $diagnosticFamily `
            -InstallLocation $diagnosticLocation
    }

    return New-ChatGPTCodexDetection -Status 'scheme_target_invalid' -Installed $true `
        -Version $diagnosticVersion -PackageFamilyName $diagnosticFamily `
        -InstallLocation $diagnosticLocation -SchemeRegistered $true
}
