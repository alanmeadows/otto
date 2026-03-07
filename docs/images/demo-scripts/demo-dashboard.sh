#!/bin/bash
# Simulates: otto server start && otto server status
fake_type() {
    local text="$1"
    for ((i=0; i<${#text}; i++)); do
        printf "%s" "${text:$i:1}"
        sleep 0.04
    done
}

printf "\033[1;32m❯\033[0m "
fake_type "otto server start"
echo ""
sleep 0.8
echo "Starting otto daemon..."
sleep 0.3
printf "  \033[32m✓\033[0m API server listening on :4097\n"
sleep 0.2
printf "  \033[32m✓\033[0m Dashboard listening on :4098\n"
sleep 0.2
printf "  \033[32m✓\033[0m Copilot server started (bgtask, port 4321)\n"
sleep 0.2
printf "  \033[32m✓\033[0m Tunnel connected\n"
sleep 1

echo ""
printf "\033[1;32m❯\033[0m "
fake_type "otto server status"
echo ""
sleep 0.5
echo "daemon is running (PID 48291, uptime 14m22s)"
echo "  api:       http://localhost:4097"
echo "  dashboard: http://localhost:4098"
echo "  tunnel:    https://0mwbqhhp-4098.usw3.devtunnels.ms?key=a8f3b2c1..."
sleep 3
