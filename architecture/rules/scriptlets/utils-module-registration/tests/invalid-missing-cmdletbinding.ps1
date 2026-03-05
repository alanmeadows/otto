# Test: missing CmdletBinding attribute
# Expected: FAIL — exported function lacks [CmdletBinding()] attribute

param()

function Get-UnsafeData {
    param(
        [string]$Name
    )

    return @{ Name = $Name }
}
