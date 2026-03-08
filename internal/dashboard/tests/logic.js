// Extracted pure logic from app.js for unit testing.
// These functions mirror the dashboard's message handling, state management,
// and pending message logic without DOM dependencies.

'use strict';

function hashStr(str) {
    var h = 0;
    for (var i = 0; i < str.length; i++) {
        h = ((h << 5) - h + str.charCodeAt(i)) | 0;
    }
    return Math.abs(h).toString(36);
}

// Simulates handleSessionHistory: given current pending prompts and server
// history messages, returns which pending prompts should survive and which
// messages should be rendered.
function reconcileHistory(messages, pendingPrompts) {
    const historyContents = new Set(
        messages.filter(m => m.role === 'user').map(m => m.content)
    );
    const survivingPending = pendingPrompts.filter(p => !historyContents.has(p));
    return {
        rendered: messages.filter(m => m.content && m.content.trim()),
        pendingPrompts: survivingPending,
    };
}

// Simulates handleUserMessage: given current pending prompts and a broadcasted
// user message, returns updated pending prompts.
function confirmUserMessage(pendingPrompts, content) {
    const idx = pendingPrompts.indexOf(content);
    if (idx >= 0) {
        const updated = [...pendingPrompts];
        updated.splice(idx, 1);
        return updated;
    }
    return pendingPrompts;
}

// Determines if the interrupt/queue choice should be shown.
function shouldShowInterruptChoice(session) {
    return !!(session && session.is_processing);
}

// Determines if a persisted session should open in watch mode or view mode.
function sessionClickMode(session) {
    if (session.is_active) return 'watch';
    return 'view';
}

// Simulates the pending message ID generation.
function pendingId(content) {
    return 'pending-' + hashStr(content);
}

// Validates message ordering: user and assistant messages should alternate
// correctly (user messages can appear consecutively if queued).
function validateMessageOrder(messages) {
    const issues = [];
    for (let i = 1; i < messages.length; i++) {
        const prev = messages[i - 1];
        const curr = messages[i];
        // Same exact content back-to-back = duplicate
        if (prev.role === curr.role && prev.content === curr.content) {
            issues.push({ type: 'duplicate', index: i, content: curr.content.substring(0, 50) });
        }
    }
    return issues;
}

module.exports = {
    hashStr,
    reconcileHistory,
    confirmUserMessage,
    shouldShowInterruptChoice,
    sessionClickMode,
    pendingId,
    validateMessageOrder,
};
