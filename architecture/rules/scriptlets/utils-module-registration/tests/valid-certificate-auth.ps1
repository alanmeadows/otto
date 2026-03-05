# Test: certificate-based credential handling
# Expected: PASS — uses certificate from store, no plaintext secrets

[CmdletBinding()]
param()

function Get-ServiceCertificate {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [ValidateNotNullOrEmpty()]
        [string]$Thumbprint
    )

    $cert = Get-ChildItem -Path "Cert:\LocalMachine\My" |
        Where-Object { $_.Thumbprint -eq $Thumbprint }

    if (-not $cert) {
        throw "Certificate '$Thumbprint' not found."
    }

    return $cert
}

function New-ServiceGuardian {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [System.Security.Cryptography.X509Certificates.X509Certificate2]$SigningCert
    )

    return New-HgsGuardian -Name "TestGuardian" -SigningCertificate $SigningCert
}
