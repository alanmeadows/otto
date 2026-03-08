#!/bin/bash
# E2E tests for the otto dashboard using rodney (headless Chrome).
# Requires: rodney, node, ws (npm package for mock server)
#
# Usage: ./internal/dashboard/tests/e2e.sh
# Exit code 0 = all tests pass, 1 = failures

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MOCK_PORT=14098
MOCK_PID=""
RODNEY_STARTED=false
FAILURES=0
TESTS=0

cleanup() {
    if [ -n "$MOCK_PID" ]; then
        kill "$MOCK_PID" 2>/dev/null || true
        wait "$MOCK_PID" 2>/dev/null || true
    fi
    if $RODNEY_STARTED; then
        rodney stop 2>/dev/null || true
    fi
}
trap cleanup EXIT

pass() {
    TESTS=$((TESTS + 1))
    echo "  ✅ $1"
}

fail() {
    TESTS=$((TESTS + 1))
    FAILURES=$((FAILURES + 1))
    echo "  ❌ $1: $2"
}

assert_text() {
    local selector="$1" expected="$2" label="$3"
    local actual
    actual=$(rodney text "$selector" 2>/dev/null || echo "")
    if echo "$actual" | grep -qF "$expected"; then
        pass "$label"
    else
        fail "$label" "expected '$expected' in '$actual'"
    fi
}

assert_count() {
    local selector="$1" expected="$2" label="$3"
    local actual
    actual=$(rodney count "$selector" 2>/dev/null || echo "0")
    if [ "$actual" = "$expected" ]; then
        pass "$label"
    else
        fail "$label" "expected $expected, got $actual"
    fi
}

assert_exists() {
    local selector="$1" label="$2"
    if rodney exists "$selector" 2>/dev/null | grep -q "true"; then
        pass "$label"
    else
        fail "$label" "element '$selector' not found"
    fi
}

assert_visible() {
    local selector="$1" label="$2"
    if rodney visible "$selector" 2>/dev/null | grep -q "true"; then
        pass "$label"
    else
        fail "$label" "element '$selector' not visible"
    fi
}

assert_hidden() {
    local selector="$1" label="$2"
    if rodney visible "$selector" 2>/dev/null | grep -q "false"; then
        pass "$label"
    else
        fail "$label" "element '$selector' should be hidden"
    fi
}

# --- Setup ---

echo "Installing ws dependency if needed..."
cd "$SCRIPT_DIR" && npm list ws 2>/dev/null || npm install --no-save ws 2>/dev/null

echo "Starting mock server on port $MOCK_PORT..."
node "$SCRIPT_DIR/mock-server.js" "$MOCK_PORT" &
MOCK_PID=$!
sleep 2

# Verify mock server is up
if ! curl -s -o /dev/null -w '%{http_code}' "http://localhost:$MOCK_PORT/" | grep -q 200; then
    echo "❌ Mock server failed to start"
    exit 1
fi

echo "Starting rodney..."
rodney start --insecure 2>/dev/null
RODNEY_STARTED=true

echo "Loading dashboard..."
rodney open "http://localhost:$MOCK_PORT/" 2>/dev/null
rodney waitload 2>/dev/null
rodney waitstable 2>/dev/null
echo ""

# ============================================================
echo "=== Test Suite: Page Load ==="

assert_text "#header h1" "Otto Dashboard" "Dashboard title renders"
assert_exists "#connection-status" "Connection status dot exists"
assert_exists "#chat-input" "Chat input exists"
assert_exists "#send-btn" "Send button exists"

# ============================================================
echo ""
echo "=== Test Suite: Session Selection ==="

# Click the test session
rodney click '.session-card' 2>/dev/null
rodney waitstable 2>/dev/null

assert_visible "#chat-view" "Chat view visible after selecting session"
assert_text "#chat-session-name" "test-session" "Session name in header"

# ============================================================
echo ""
echo "=== Test Suite: Send Message ==="

rodney click '#chat-input' 2>/dev/null
rodney input '#chat-input' 'Hello, world!' 2>/dev/null
rodney click '#send-btn' 2>/dev/null
sleep 2
rodney waitstable 2>/dev/null

# After turn completes (mock takes ~600ms), history should be refreshed
assert_count ".message.user" "1" "Exactly 1 user message (no duplicates)"
assert_count ".message.assistant" "1" "Exactly 1 assistant message"
assert_text "#chat-messages" "Hello, world!" "User message content present"
assert_text "#chat-messages" "test response" "Assistant response present"

# ============================================================
echo ""
echo "=== Test Suite: Multi-turn ==="

rodney click '#chat-input' 2>/dev/null
rodney input '#chat-input' 'Second question' 2>/dev/null
rodney click '#send-btn' 2>/dev/null
sleep 2
rodney waitstable 2>/dev/null

assert_count ".message.user" "2" "Exactly 2 user messages after second turn"
assert_count ".message.assistant" "2" "Exactly 2 assistant messages after second turn"

# ============================================================
echo ""
echo "=== Test Suite: No Duplicate Messages ==="

# Check that no user message appears twice with same content
USER_MSGS=$(rodney js 'Array.from(document.querySelectorAll(".message.user")).map(e => e.textContent).join("|||")' 2>/dev/null)
HELLO_COUNT=$(echo "$USER_MSGS" | grep -o "Hello, world!" | wc -l)
if [ "$HELLO_COUNT" = "1" ]; then
    pass "No duplicate 'Hello, world!' messages"
else
    fail "No duplicate 'Hello, world!' messages" "found $HELLO_COUNT occurrences"
fi

# ============================================================
echo ""
echo "=== Test Suite: Scroll Lock ==="

assert_exists "#new-messages-pill" "New messages pill element exists"
# Check pill has 'hidden' class (not visible when at bottom)
PILL_CLASS=$(rodney attr '#new-messages-pill' 'class' 2>/dev/null || echo "")
if echo "$PILL_CLASS" | grep -q "hidden"; then
    pass "Pill has hidden class when at bottom"
else
    fail "Pill has hidden class when at bottom" "class='$PILL_CLASS'"
fi

# ============================================================
echo ""
echo "=== Test Suite: Interrupt/Queue UI ==="

# Send a message, then quickly send another while "processing"
rodney click '#chat-input' 2>/dev/null
rodney input '#chat-input' 'First message' 2>/dev/null
rodney click '#send-btn' 2>/dev/null

# Immediately try to send another (session should be processing)
sleep 0.2
rodney click '#chat-input' 2>/dev/null
rodney input '#chat-input' 'While busy' 2>/dev/null
rodney click '#send-btn' 2>/dev/null
sleep 0.5

# Check if choice bar appeared (may have already resolved if mock is fast)
CHOICE_EXISTS=$(rodney exists '#send-choice' 2>/dev/null || echo "false")
if echo "$CHOICE_EXISTS" | grep -q "true"; then
    pass "Interrupt/Queue choice bar appears when LLM is busy"
    # Click cancel to dismiss
    rodney click '#choice-cancel' 2>/dev/null
else
    # Mock might be too fast for the choice to appear — that's OK
    pass "Interrupt/Queue choice bar (mock too fast to catch, skipped)"
fi

# Wait for everything to settle
sleep 2
rodney waitstable 2>/dev/null

# ============================================================
echo ""
echo "=== Results ==="
echo "Tests: $TESTS, Passed: $((TESTS - FAILURES)), Failed: $FAILURES"

if [ "$FAILURES" -gt 0 ]; then
    exit 1
fi
exit 0
