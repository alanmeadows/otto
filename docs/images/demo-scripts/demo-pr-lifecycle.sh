#!/bin/bash
# Simulates: otto pr add <url> && otto pr list
fake_type() {
    local text="$1"
    for ((i=0; i<${#text}; i++)); do
        printf "%s" "${text:$i:1}"
        sleep 0.03
    done
}

printf "\033[1;32m❯\033[0m "
fake_type "otto pr add https://dev.azure.com/contoso/Fabric/_git/attestation/pullrequest/1847"
echo ""
sleep 0.6
echo ""
printf "Added PR \033[1m#1847\033[0m (ADO) — Implement TPM attestation flow for CVM guests\n"
sleep 0.3
printf "  \033[32m✓\033[0m Daemon notified, immediate poll triggered\n"
sleep 0.2
printf "  \033[32m✓\033[0m Work item trigger posted (thread 48)\n"
sleep 0.2
printf "  \033[32m✓\033[0m Closed work item trigger thread\n"
echo ""
sleep 1.2

printf "\033[1;32m❯\033[0m "
fake_type "otto pr list"
echo ""
sleep 0.5
echo ""
printf "┌──────┬──────────┬───────────────────────────────────────────────┬────────────────────┬──────────────────────┬───────┐\n"
printf "│  ID  │ STATUS   │ STAGES                                        │ WAITING ON         │ BRANCH               │ FIXES │\n"
printf "├──────┼──────────┼───────────────────────────────────────────────┼────────────────────┼──────────────────────┼───────┤\n"
sleep 0.1
printf "│ 1847 │ \033[33mfixing  \033[0m │ \033[32m✓\033[0m merlinbot │ \033[32m✓\033[0m feedback │ \033[33m○\033[0m pipelines   │ fix in progress    │ users/alan/tpm-att │ 2/5   │\n"
sleep 0.1
printf "│ 1832 │ \033[32mgreen   \033[0m │ \033[32m✓\033[0m merlinbot │ \033[32m✓\033[0m feedback │ \033[32m✓\033[0m pipelines   │ all clear          │ users/alan/sdn-ref │ 0/5   │\n"
sleep 0.1
printf "│ 1801 │ watching │ \033[32m✓\033[0m merlinbot │ \033[33m○\033[0m feedback │ \033[32m✓\033[0m pipelines   │ feedback           │ users/alan/vmgs-v2 │ 1/5   │\n"
sleep 0.1
printf "│ 1798 │ \033[31merror   \033[0m │ \033[32m✓\033[0m merlinbot │ \033[32m✓\033[0m feedback │ \033[31m✗\033[0m pipelines   │ manual intervention│ users/alan/cvm-sec │ 5/5   │\n"
printf "└──────┴──────────┴───────────────────────────────────────────────┴────────────────────┴──────────────────────┴───────┘\n"
echo ""
printf "4 tracked PRs (1 green, 1 fixing, 1 watching, 1 error)\n"
sleep 3
