#!/bin/bash
# Combined otto demo: server start → pr review → pr lifecycle
# Designed to be run via VHS with Hide/Show wrapping

fake_type() {
    local text="$1"
    for ((i=0; i<${#text}; i++)); do
        printf "%s" "${text:$i:1}"
        sleep 0.03
    done
}

# ─── Section 1: Server Start ───
printf "\033[1;32m❯\033[0m "
fake_type "otto server start"
echo ""
sleep 0.5
echo "Starting otto daemon..."
sleep 0.2
printf "  \033[32m✓\033[0m API server on :4097\n"
sleep 0.15
printf "  \033[32m✓\033[0m Dashboard on :4098\n"
sleep 0.15
printf "  \033[32m✓\033[0m Copilot server started (port 4321)\n"
sleep 0.15
printf "  \033[32m✓\033[0m Tunnel connected\n"
sleep 0.8

echo ""
printf "\033[1;32m❯\033[0m "
fake_type "otto server status"
echo ""
sleep 0.3
echo "daemon is running (PID 48291, uptime 2m14s)"
echo "  api:       http://localhost:4097"
echo "  dashboard: http://localhost:4098"
echo "  tunnel:    https://0mwbqhhp-4098.usw3.devtunnels.ms?key=a8f3..."
sleep 2.5

# ─── Section 2: PR Review ───
clear
printf "\033[1;32m❯\033[0m "
fake_type 'otto pr review https://dev.azure.com/.../pullrequest/1847 \'
echo ""
printf "  "
fake_type '"focus on error handling and concurrency safety"'
echo ""
sleep 0.6
echo ""
printf "Reviewing PR \033[1m#1847\033[0m: Implement TPM attestation flow\n"
sleep 0.4
echo "Analyzing 14 changed files against main..."
sleep 1.2
echo ""
printf "Found \033[1m6\033[0m review comments:\n"
echo ""
printf " # │ Severity │ File                   │ Line │ Comment\n"
printf "───┼──────────┼────────────────────────┼──────┼──────────────────────────────────────────\n"
sleep 0.12
printf " 1 │ \033[31mcritical\033[0m │ tpm/attest.go          │  142 │ TPM session not closed on error — leak\n"
sleep 0.12
printf " 2 │ \033[31mcritical\033[0m │ tpm/attest.go          │  178 │ Race: attestMap access without lock\n"
sleep 0.12
printf " 3 │ \033[33mwarning \033[0m │ tpm/quote.go           │   63 │ Nonce reuse weakens replay protection\n"
sleep 0.12
printf " 4 │ \033[33mwarning \033[0m │ tpm/pcr.go             │   91 │ Validate PCR bank against TPM caps\n"
sleep 0.12
printf " 5 │ \033[36minfo    \033[0m │ cvm/guest.go           │  215 │ 30s timeout may be too short for remote\n"
sleep 0.12
printf " 6 │ \033[36minfo    \033[0m │ cmd/attestd/main.go    │   34 │ Use structured logging, not fmt.Printf\n"
echo ""
sleep 1.2

# Interactive selection
printf "Select comments to post (space to toggle, enter to confirm):\n"
echo ""

# Show initial state — all selected
printf "  \033[32m◆\033[0m [\033[31mcritical\033[0m] tpm/attest.go:142 — TPM session not closed on error\n"
printf "  \033[32m◆\033[0m [\033[31mcritical\033[0m] tpm/attest.go:178 — Race: attestMap access without lock\n"
printf "  \033[32m◆\033[0m [\033[33mwarning\033[0m]  tpm/quote.go:63  — Nonce reuse weakens replay protection\n"
printf "  \033[32m◆\033[0m [\033[33mwarning\033[0m]  tpm/pcr.go:91    — Validate PCR bank against TPM caps\n"
printf "  \033[32m◆\033[0m [\033[36minfo\033[0m]     cvm/guest.go:215 — 30s timeout may be too short\n"
printf " ▸\033[32m◆\033[0m [\033[36minfo\033[0m]     attestd/main:34  — Use structured logging\n"
sleep 1

# Simulate deselecting item 6 (space)
printf "\033[1A\r"
printf " ▸\033[90m◇\033[0m [\033[36minfo\033[0m]     attestd/main:34  — Use structured logging\n"
sleep 0.6

# Simulate moving up and deselecting item 5
printf "\033[2A\r"
printf " ▸\033[90m◇\033[0m [\033[36minfo\033[0m]     cvm/guest.go:215 — 30s timeout may be too short\n"
printf "  \033[90m◇\033[0m [\033[36minfo\033[0m]     attestd/main:34  — Use structured logging\n"
sleep 0.8

# Confirm
echo ""
printf "Posting \033[1m4\033[0m comments to PR #1847...\n"
sleep 0.4
printf "\033[32m✓\033[0m Posted 4 inline comments\n"
sleep 2.5

# ─── Section 3: PR Lifecycle ───
clear
printf "\033[1;32m❯\033[0m "
fake_type "otto pr add https://dev.azure.com/.../pullrequest/1847"
echo ""
sleep 0.4
echo ""
printf "Added PR \033[1m#1847\033[0m (ADO) — Implement TPM attestation flow\n"
printf "  \033[32m✓\033[0m Daemon notified, immediate poll triggered\n"
sleep 1

echo ""
printf "\033[1;32m❯\033[0m "
fake_type "otto pr list"
echo ""
sleep 0.3
echo ""
printf "  ID │ Status   │ Stages                           │ Waiting on          │ Branch              │ Fixes\n"
printf "─────┼──────────┼──────────────────────────────────┼─────────────────────┼─────────────────────┼──────\n"
sleep 0.1
printf " 1847│ \033[33mfixing  \033[0m │ \033[32m✓\033[0m merlin  \033[32m✓\033[0m feedback  \033[33m○\033[0m pipeline │ fix in progress     │ users/alan/tpm-att  │ 2/5\n"
sleep 0.1
printf " 1832│ \033[32mgreen   \033[0m │ \033[32m✓\033[0m merlin  \033[32m✓\033[0m feedback  \033[32m✓\033[0m pipeline │ all clear           │ users/alan/sdn-ref  │ 0/5\n"
sleep 0.1
printf " 1801│ watching │ \033[32m✓\033[0m merlin  \033[33m○\033[0m feedback  \033[32m✓\033[0m pipeline │ feedback            │ users/alan/vmgs-v2  │ 1/5\n"
sleep 0.1
printf " 1798│ \033[31merror   \033[0m │ \033[32m✓\033[0m merlin  \033[32m✓\033[0m feedback  \033[31m✗\033[0m pipeline │ manual intervention │ users/alan/cvm-sec  │ 5/5\n"
echo ""
printf "4 tracked PRs (1 green, 1 fixing, 1 watching, 1 error)\n"
sleep 3
