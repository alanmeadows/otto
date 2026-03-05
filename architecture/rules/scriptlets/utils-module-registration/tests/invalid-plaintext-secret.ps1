# Test: plaintext ConvertTo-SecureString usage
# Expected: FAIL — contains ConvertTo-SecureString with -AsPlainText pattern (security violation)
# The violation pattern is stored as a string to avoid triggering PSScriptAnalyzer
# while still matching the architecture rule's regex scanner.

[CmdletBinding()]
param()

function Initialize-BadCredential {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Username
    )

    # VIOLATION: architecture rule no-plaintext-securestring.
    # Pattern below matches: ConvertTo-SecureString.*-AsPlainText
    $insecureCommand = 'ConvertTo-SecureString "P@ssw0rd!" -AsPlainText -Force'
    Write-Warning "Prohibited pattern detected in credential helper: $insecureCommand"
}
