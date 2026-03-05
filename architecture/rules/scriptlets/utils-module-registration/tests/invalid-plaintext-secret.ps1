# Test: plaintext ConvertTo-SecureString usage
# Expected: FAIL — uses ConvertTo-SecureString with -AsPlainText (security violation)

[CmdletBinding()]
param()

function Initialize-BadCredential {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Username
    )

    # VIOLATION: plaintext SecureString conversion
    $password = ConvertTo-SecureString "P@ssw0rd!" -AsPlainText -Force
    $credential = New-Object System.Management.Automation.PSCredential($Username, $password)
    return $credential
}
