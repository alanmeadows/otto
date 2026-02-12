You are evaluating MerlinBot review comments on a pull request.

## MerlinBot Comments

{{.Comments}}

## Instructions

For each MerlinBot comment, decide ONE of these actions:

1. **FIX** — The comment identifies a real issue. Describe the fix needed.
2. **WONT_FIX** — The comment is a false positive or not applicable. Explain why.
3. **BY_DESIGN** — The behavior is intentional. Explain the design decision.

Output your evaluation as a structured list:

THREAD <thread_id>: <FIX|WONT_FIX|BY_DESIGN>
REASON: <one-line explanation>
ACTION: <for FIX: describe the code change needed; for others: explain rationale>

Be conservative — prefer FIX when genuinely uncertain. Only use WONT_FIX or BY_DESIGN
when you are confident the comment does not identify a real problem.
