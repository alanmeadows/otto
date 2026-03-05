# Test: plaintext ConvertTo-SecureString usage
# Expected: FAIL — uses ConvertTo-SecureString with -AsPlainText (security violation)
# PSScriptAnalyzer suppression: this file intentionally demonstrates a prohibited pattern.

[CmdletBinding()]
param()

function Initialize-BadCredential {
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingConvertToSecureStringWithPlainText', '')]
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Username
    )

    # VIOLATION: plaintext SecureString conversion (intentional — test case for rule validation)
    $password = ConvertTo-SecureString "P@ssw0rd!" -AsPlainText -Force
    $credential = New-Object System.Management.Automation.PSCredential($Username, $password)
    return $credential
}
