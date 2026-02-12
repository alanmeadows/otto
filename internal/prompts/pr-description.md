You are generating a pull request description for an Azure DevOps PR.

## Branch

{{.BranchName}}

## Commit Log

{{.CommitLog}}

{{if .SpecRequirements}}
## Specification Requirements

{{.SpecRequirements}}
{{end}}

{{if .SpecDesign}}
## Specification Design

{{.SpecDesign}}
{{end}}

## Instructions

CRITICAL: Your response must begin IMMEDIATELY with the PR title on line 1. Do NOT include any preamble, commentary, acknowledgment, or thinking. The very first character of your response must be the start of the PR title.

Output format (strict):
- **Line 1**: A short, plain-text PR title (NO markdown headings, NO `#` symbols). Keep it under 80 characters. This line must be a concrete PR title like "Add retry logic for pipeline polling" — NOT a conversational sentence.
- **Line 2**: Empty line.
- **Lines 3+**: PR description body starting with `## Summary`.

The description body should:
1. Summarize what changes were made and why
2. Highlight key implementation decisions
3. Note any risks or areas requiring careful review
4. Use markdown formatting appropriate for ADO

Keep the description focused and professional. Do not include disclaimers about being an AI.
Output ONLY the title and description — no wrapping, no code fences, no preamble.
