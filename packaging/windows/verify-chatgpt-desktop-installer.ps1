param(
    [Parameter(Mandatory = $true)][string]$Path
)

$ErrorActionPreference = 'Stop'

if (-not ('ChatGPTInstaller.CertificateNames' -as [type])) {
    $null = Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Security.Cryptography.X509Certificates;
using System.Text;

namespace ChatGPTInstaller {
    public static class CertificateNames {
        private const uint CertNameAttrType = 3;

        [DllImport("crypt32.dll", CharSet = CharSet.Unicode, ExactSpelling = true, SetLastError = true)]
        private static extern uint CertGetNameStringW(
            IntPtr certificateContext,
            uint type,
            uint flags,
            [MarshalAs(UnmanagedType.LPStr)] string attributeOid,
            StringBuilder output,
            uint outputLength);

        public static string GetSubjectAttribute(X509Certificate2 certificate, string attributeOid) {
            if (certificate == null) throw new ArgumentNullException("certificate");
            if (String.IsNullOrWhiteSpace(attributeOid)) throw new ArgumentException("attribute OID is required", "attributeOid");
            uint required = CertGetNameStringW(certificate.Handle, CertNameAttrType, 0, attributeOid, null, 0);
            if (required <= 1) return String.Empty;
            var output = new StringBuilder((int)required);
            uint written = CertGetNameStringW(certificate.Handle, CertNameAttrType, 0, attributeOid, output, required);
            if (written <= 1) return String.Empty;
            return output.ToString();
        }
    }
}
'@ -ErrorAction Stop
}

function Get-CertificateSubjectAttribute {
    param(
        [Parameter(Mandatory = $true)][System.Security.Cryptography.X509Certificates.X509Certificate2]$Certificate,
        [Parameter(Mandatory = $true)][string]$Oid
    )

    return [string][ChatGPTInstaller.CertificateNames]::GetSubjectAttribute($Certificate, $Oid)
}

function Assert-CertificateEnhancedKeyUsage {
    param(
        [Parameter(Mandatory = $true)][System.Security.Cryptography.X509Certificates.X509Certificate2]$Certificate,
        [Parameter(Mandatory = $true)][string]$RequiredOid,
        [Parameter(Mandatory = $true)][string]$Purpose
    )

    $extensions = @($Certificate.Extensions | Where-Object { $_.Oid.Value -ceq '2.5.29.37' })
    if ($extensions.Count -ne 1) {
        throw "ChatGPT desktop installer $Purpose certificate has no unambiguous enhanced-key-usage extension"
    }
    $enhancedKeyUsage = [System.Security.Cryptography.X509Certificates.X509EnhancedKeyUsageExtension]$extensions[0]
    $usages = @($enhancedKeyUsage.EnhancedKeyUsages | ForEach-Object { [string]$_.Value })
    if (-not ($usages -ccontains $RequiredOid)) {
        throw "ChatGPT desktop installer $Purpose certificate lacks required EKU $RequiredOid"
    }
}

function New-OnlineCertificateChain {
    param([bool]$IgnoreNotTimeValid = $false)

    $chain = New-Object System.Security.Cryptography.X509Certificates.X509Chain
    $chain.ChainPolicy.RevocationMode = [System.Security.Cryptography.X509Certificates.X509RevocationMode]::Online
    # Root trust/revocation is maintained by the Windows trusted-root program;
    # requiring an online CRL for a root frequently yields false offline errors.
    $chain.ChainPolicy.RevocationFlag = [System.Security.Cryptography.X509Certificates.X509RevocationFlag]::ExcludeRoot
    $chain.ChainPolicy.UrlRetrievalTimeout = [TimeSpan]::FromSeconds(15)
    if ($IgnoreNotTimeValid) {
        $chain.ChainPolicy.VerificationFlags = [System.Security.Cryptography.X509Certificates.X509VerificationFlags]::IgnoreNotTimeValid
    } else {
        $chain.ChainPolicy.VerificationFlags = [System.Security.Cryptography.X509Certificates.X509VerificationFlags]::NoFlag
    }
    return $chain
}

function Assert-TrustedMicrosoftRoot {
    param(
        [Parameter(Mandatory = $true)]$Chain,
        [Parameter(Mandatory = $true)][string[]]$AllowedFingerprints
    )

    if ($Chain.ChainElements.Count -lt 2) {
        throw 'ChatGPT desktop installer signer chain is incomplete'
    }
    $root = $Chain.ChainElements[$Chain.ChainElements.Count - 1].Certificate
    $sha256 = [System.Security.Cryptography.SHA256]::Create()
    try {
        $rootFingerprint = ([BitConverter]::ToString($sha256.ComputeHash($root.RawData))).Replace('-', '')
    } finally {
        $sha256.Dispose()
    }
    if (-not ($AllowedFingerprints -ccontains $rootFingerprint)) {
        throw 'ChatGPT desktop installer signer chain terminates at an unexpected root certificate'
    }
}

function Test-OnlyNotTimeValidChainStatus {
    param([Parameter(Mandatory = $true)]$Chain)

    $notTimeValid = [int][System.Security.Cryptography.X509Certificates.X509ChainStatusFlags]::NotTimeValid
    $sawNotTimeValid = $false
    foreach ($entry in @($Chain.ChainStatus)) {
        $status = [int]$entry.Status
        if (($status -band $notTimeValid) -ne 0) {
            $sawNotTimeValid = $true
        }
        $nonTimeStatus = $status -band (-bnot $notTimeValid)
        if ($nonTimeStatus -ne 0) { return $false }
    }
    return $sawNotTimeValid
}

if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
    throw "ChatGPT desktop installer missing"
}

$sig = Get-AuthenticodeSignature -LiteralPath $Path
if ($sig.Status -ne 'Valid') {
    throw "ChatGPT desktop installer Authenticode signature is $($sig.Status)"
}
if ($null -eq $sig.SignerCertificate) {
    throw 'ChatGPT desktop installer has no signer certificate'
}

$signer = $sig.SignerCertificate
$trustedSignerRootFingerprints = @(
    # Microsoft Root Certificate Authority 2011, SHA-256 fingerprint.
    # Add a replacement here only after verifying a current official
    # Product ID 9NT1R1C2HH7J bootstrapper chain out of band.
    '847DF6A78497943F27FC72EB93F9A637320A02B561D0A91B09E87A7807ED7C61'
)
$trustedTimestampRootFingerprints = @(
    # Microsoft Root Certificate Authority 2010, used by the current
    # Microsoft Time-Stamp PCA 2010 chain.
    'DF545BF919A2439C36983B54CDFC903DFA4F37D3996D8D84B4C31EEC6F3C163E'
)
$organization = Get-CertificateSubjectAttribute -Certificate $signer -Oid '2.5.4.10'
$country = Get-CertificateSubjectAttribute -Certificate $signer -Oid '2.5.4.6'
$commonName = Get-CertificateSubjectAttribute -Certificate $signer -Oid '2.5.4.3'
if ($organization -cne 'Microsoft Corporation' -or $country -cne 'US' -or $commonName -cne 'Microsoft Corporation') {
    throw 'ChatGPT desktop installer signer publisher identity is not the expected Microsoft Corporation identity'
}
Assert-CertificateEnhancedKeyUsage -Certificate $signer -RequiredOid '1.3.6.1.5.5.7.3.3' -Purpose 'signer'

$chain = New-OnlineCertificateChain
try {
    $chainValidNow = $chain.Build($signer)
    if (-not $chainValidNow) {
        if (-not (Test-OnlyNotTimeValidChainStatus -Chain $chain)) {
            $statuses = (@($chain.ChainStatus) | ForEach-Object { [string]$_.Status }) -join ', '
            throw "ChatGPT desktop installer signer chain or online revocation check is invalid: $statuses"
        }
        if ($null -eq $sig.TimeStamperCertificate) {
            throw 'ChatGPT desktop installer signer is outside its validity period and has no valid Authenticode timestamp'
        }
        Assert-CertificateEnhancedKeyUsage -Certificate $sig.TimeStamperCertificate `
            -RequiredOid '1.3.6.1.5.5.7.3.8' -Purpose 'timestamp'

        $timestampChain = New-OnlineCertificateChain
        try {
            if (-not $timestampChain.Build($sig.TimeStamperCertificate)) {
                $statuses = (@($timestampChain.ChainStatus) | ForEach-Object { [string]$_.Status }) -join ', '
                throw "ChatGPT desktop installer timestamp certificate chain or online revocation check is invalid: $statuses"
            }
            Assert-TrustedMicrosoftRoot -Chain $timestampChain `
                -AllowedFingerprints $trustedTimestampRootFingerprints
        } finally {
            $timestampChain.Dispose()
        }

        # Get-AuthenticodeSignature Status=Valid proves the RFC3161/countersignature
        # was valid at signing time. Rebuild the signer chain online while allowing
        # only the already-isolated current-time failure; all trust and revocation
        # failures remain blocking.
        $timestampTolerantChain = New-OnlineCertificateChain -IgnoreNotTimeValid $true
        try {
            if (-not $timestampTolerantChain.Build($signer)) {
                $statuses = (@($timestampTolerantChain.ChainStatus) | ForEach-Object { [string]$_.Status }) -join ', '
                throw "ChatGPT desktop installer timestamp-tolerant signer chain is invalid: $statuses"
            }
            Assert-TrustedMicrosoftRoot -Chain $timestampTolerantChain `
                -AllowedFingerprints $trustedSignerRootFingerprints
        } finally {
            $timestampTolerantChain.Dispose()
        }
    } else {
        Assert-TrustedMicrosoftRoot -Chain $chain `
            -AllowedFingerprints $trustedSignerRootFingerprints
    }
} finally {
    $chain.Dispose()
}
