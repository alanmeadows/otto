<#
.SYNOPSIS
    Initializes a cluster-shared HGS guardian for TrustedLaunch / vTPM support.

.DESCRIPTION
    Creates or retrieves an HGS guardian backed by a certificate from the
    local machine certificate store. The guardian is configured as the
    cluster-shared guardian for shielded and TrustedLaunch VMs.

    This script does NOT use ConvertTo-SecureString with plaintext strings.
    All credentials are handled via certificate-based protection.

.PARAMETER GuardianName
    Friendly name for the cluster-shared guardian.

.PARAMETER CertificateThumbprint
    Thumbprint of the signing certificate in the local machine store.

.PARAMETER AttestationURL
    The HGS attestation service endpoint URL.

.PARAMETER KeyProtectionURL
    The HGS key protection service endpoint URL.

.EXAMPLE
    .\Initialize-Guardian.ps1 -GuardianName "ClusterGuardian" `
        -CertificateThumbprint "A1B2C3D4E5F6..." `
        -AttestationURL "https://hgs.contoso.com/Attestation" `
        -KeyProtectionURL "https://hgs.contoso.com/KeyProtection"
#>

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$GuardianName,

    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$CertificateThumbprint,

    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [uri]$AttestationURL,

    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [uri]$KeyProtectionURL
)

$ErrorActionPreference = 'Stop'

function Get-GuardianCertificate {
    <#
    .SYNOPSIS
        Retrieves the guardian signing certificate from the local machine store.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Thumbprint
    )

    $cert = Get-ChildItem -Path "Cert:\LocalMachine\My" |
        Where-Object { $_.Thumbprint -eq $Thumbprint }

    if (-not $cert) {
        throw "Certificate with thumbprint '$Thumbprint' not found in LocalMachine\My store."
    }

    if (-not $cert.HasPrivateKey) {
        throw "Certificate '$Thumbprint' does not have an associated private key."
    }

    return $cert
}

function New-ClusterGuardian {
    <#
    .SYNOPSIS
        Creates or retrieves a cluster-shared HGS guardian.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name,

        [Parameter(Mandatory = $true)]
        [System.Security.Cryptography.X509Certificates.X509Certificate2]$SigningCertificate,

        [Parameter(Mandatory = $true)]
        [uri]$AttestationServiceURL,

        [Parameter(Mandatory = $true)]
        [uri]$KeyProtectionServiceURL
    )

    $existing = Get-HgsGuardian -Name $Name -ErrorAction SilentlyContinue
    if ($existing) {
        Write-Verbose "Guardian '$Name' already exists — returning existing guardian."
        return $existing
    }

    Write-Verbose "Creating new guardian '$Name' with certificate '$($SigningCertificate.Thumbprint)'."

    # Use the certificate object directly — no plaintext password conversion needed.
    # The certificate's private key is protected by the machine key store and
    # accessed via the X509Certificate2 object, which keeps credentials secure.
    $guardian = New-HgsGuardian `
        -Name $Name `
        -SigningCertificate $SigningCertificate `
        -AllowExpired:$false `
        -AllowUntrustedRoot:$false

    return $guardian
}

function Register-AttestationPolicy {
    <#
    .SYNOPSIS
        Configures the HGS attestation and key protection endpoints.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [uri]$AttestationServiceURL,

        [Parameter(Mandatory = $true)]
        [uri]$KeyProtectionServiceURL
    )

    Write-Verbose "Setting HGS client configuration: Attestation=$AttestationServiceURL, KeyProtection=$KeyProtectionServiceURL"

    Set-HgsClientConfiguration `
        -AttestationServerUrl $AttestationServiceURL `
        -KeyProtectionServerUrl $KeyProtectionServiceURL
}

function Main {
    Write-Output "Initializing cluster-shared HGS guardian '$GuardianName'..."

    # Step 1: Retrieve the signing certificate from the machine store.
    $cert = Get-GuardianCertificate -Thumbprint $CertificateThumbprint
    Write-Output "Found signing certificate: Subject=$($cert.Subject), Expires=$($cert.NotAfter)"

    # Step 2: Create or retrieve the guardian using the certificate directly.
    $guardian = New-ClusterGuardian `
        -Name $GuardianName `
        -SigningCertificate $cert `
        -AttestationServiceURL $AttestationURL `
        -KeyProtectionServiceURL $KeyProtectionURL

    Write-Output "Guardian '$($guardian.Name)' is ready."

    # Step 3: Configure the HGS client endpoints.
    Register-AttestationPolicy `
        -AttestationServiceURL $AttestationURL `
        -KeyProtectionServiceURL $KeyProtectionURL

    Write-Output "HGS client configuration updated."
    Write-Output "Guardian initialization complete."

    return $guardian
}

Main
