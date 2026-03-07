#!/bin/bash
# Simulates: otto pr review <url> "focus on ..."
fake_type() {
    local text="$1"
    for ((i=0; i<${#text}; i++)); do
        printf "%s" "${text:$i:1}"
        sleep 0.03
    done
}

printf "\033[1;32m❯\033[0m "
fake_type 'otto pr review https://dev.azure.com/contoso/Fabric/_git/attestation/pullrequest/1847 \'
echo ""
printf "  "
fake_type '"focus on error handling, concurrency safety, and resource cleanup"'
echo ""
sleep 0.8
echo ""
printf "Reviewing PR \033[1m#1847\033[0m: Implement TPM attestation flow for CVM guests\n"
sleep 0.5
echo "Cloning repo and analyzing 14 changed files against main..."
sleep 1.5
echo ""
printf "Focus: \033[3merror handling, concurrency safety, and resource cleanup\033[0m\n"
sleep 0.8
echo ""
printf "Found \033[1m6\033[0m review comments:\n"
echo ""
printf "┌───┬──────────┬──────────────────────────┬──────┬──────────────────────────────────────────────────────────────┐\n"
printf "│ # │ SEVERITY │ FILE                     │ LINE │ COMMENT                                                      │\n"
printf "├───┼──────────┼──────────────────────────┼──────┼──────────────────────────────────────────────────────────────┤\n"
sleep 0.15
printf "│ 1 │ \033[31mcritical\033[0m │ internal/tpm/attest.go   │  142 │ TPM session not closed on error path — resource leak          │\n"
sleep 0.15
printf "│ 2 │ \033[31mcritical\033[0m │ internal/tpm/attest.go   │  178 │ Race condition: concurrent access to attestMap without lock   │\n"
sleep 0.15
printf "│ 3 │ \033[33mwarning \033[0m │ internal/tpm/quote.go    │   63 │ Nonce reuse across retry attempts weakens replay protection   │\n"
sleep 0.15
printf "│ 4 │ \033[33mwarning \033[0m │ internal/tpm/pcr.go      │   91 │ PCR bank selection should validate against TPM capabilities   │\n"
sleep 0.15
printf "│ 5 │ \033[36minfo    \033[0m │ internal/cvm/guest.go    │  215 │ Context timeout of 30s may be too short for remote attestat...│\n"
sleep 0.15
printf "│ 6 │ \033[36minfo    \033[0m │ cmd/attestd/main.go      │   34 │ Consider structured logging instead of fmt.Printf            │\n"
printf "└───┴──────────┴──────────────────────────┴──────┴──────────────────────────────────────────────────────────────┘\n"
echo ""
sleep 1.5
printf "Posting \033[1m6\033[0m comments to PR #1847...\n"
sleep 0.6
printf "\033[32m✓\033[0m Posted 6 inline comments to PR #1847\n"
sleep 3
