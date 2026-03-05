# utils-module-registration

This architecture rule validates that all PowerShell utility modules (scriptlets)
are properly registered via the standard module registration pattern.

## Rule

Every `.ps1` file under `scripts/` that defines reusable functions must:

1. Declare a module manifest or registration entry.
2. Use `[CmdletBinding()]` on all exported functions.
3. Not use `ConvertTo-SecureString` with `-AsPlainText` parameter.

## Enforcement

The rule is checked by scanning PowerShell files for:
- Missing `[CmdletBinding()]` attributes on public functions
- Plaintext `ConvertTo-SecureString` usage (security violation)
- Functions without proper parameter validation attributes
