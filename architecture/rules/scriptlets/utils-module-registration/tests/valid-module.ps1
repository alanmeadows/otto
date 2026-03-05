# Test: valid module registration with CmdletBinding
# Expected: PASS — all functions use [CmdletBinding()] and no plaintext SecureString

[CmdletBinding()]
param()

function Get-ExampleData {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name
    )

    return @{ Name = $Name; Status = "OK" }
}

function Set-ExampleConfig {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [ValidateNotNullOrEmpty()]
        [string]$Key,

        [Parameter(Mandatory = $true)]
        [string]$Value
    )

    Write-Verbose "Setting $Key = $Value"
}
