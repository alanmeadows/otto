// Unit tests for dashboard message handling logic.
// Run: node --test internal/dashboard/tests/logic.test.js

'use strict';

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const {
    hashStr,
    reconcileHistory,
    confirmUserMessage,
    shouldShowInterruptChoice,
    sessionClickMode,
    pendingId,
    validateMessageOrder,
} = require('./logic.js');

// --- hashStr ---

describe('hashStr', () => {
    test('returns consistent hash for same input', () => {
        assert.equal(hashStr('hello'), hashStr('hello'));
    });

    test('returns different hashes for different inputs', () => {
        assert.notEqual(hashStr('hello'), hashStr('world'));
    });

    test('handles empty string', () => {
        assert.equal(typeof hashStr(''), 'string');
    });
});

// --- reconcileHistory ---

describe('reconcileHistory', () => {
    test('removes pending prompts that appear in history', () => {
        const messages = [
            { role: 'user', content: 'hello' },
            { role: 'assistant', content: 'hi there' },
        ];
        const pending = ['hello', 'second question'];
        const result = reconcileHistory(messages, pending);
        assert.deepEqual(result.pendingPrompts, ['second question']);
    });

    test('keeps all pending when none match history', () => {
        const messages = [
            { role: 'assistant', content: 'welcome' },
        ];
        const pending = ['first', 'second'];
        const result = reconcileHistory(messages, pending);
        assert.deepEqual(result.pendingPrompts, ['first', 'second']);
    });

    test('clears all pending when all match history', () => {
        const messages = [
            { role: 'user', content: 'a' },
            { role: 'user', content: 'b' },
        ];
        const pending = ['a', 'b'];
        const result = reconcileHistory(messages, pending);
        assert.deepEqual(result.pendingPrompts, []);
    });

    test('filters out empty messages from rendered output', () => {
        const messages = [
            { role: 'user', content: 'hello' },
            { role: 'assistant', content: '' },
            { role: 'assistant', content: '  ' },
            { role: 'assistant', content: 'response' },
        ];
        const result = reconcileHistory(messages, []);
        assert.equal(result.rendered.length, 2);
    });

    test('handles empty history and pending', () => {
        const result = reconcileHistory([], []);
        assert.deepEqual(result.rendered, []);
        assert.deepEqual(result.pendingPrompts, []);
    });

    test('preserves multiple pending when only some appear in history', () => {
        const messages = [
            { role: 'user', content: 'a' },
            { role: 'assistant', content: 'resp-a' },
        ];
        const pending = ['a', 'b', 'c'];
        const result = reconcileHistory(messages, pending);
        assert.deepEqual(result.pendingPrompts, ['b', 'c']);
    });
});

// --- confirmUserMessage ---

describe('confirmUserMessage', () => {
    test('removes confirmed message from pending', () => {
        const pending = ['first', 'second', 'third'];
        const result = confirmUserMessage(pending, 'second');
        assert.deepEqual(result, ['first', 'third']);
    });

    test('removes only first occurrence of duplicate', () => {
        const pending = ['same', 'same', 'other'];
        const result = confirmUserMessage(pending, 'same');
        assert.deepEqual(result, ['same', 'other']);
    });

    test('returns unchanged array when message not found', () => {
        const pending = ['a', 'b'];
        const result = confirmUserMessage(pending, 'c');
        assert.deepEqual(result, ['a', 'b']);
    });

    test('handles empty pending array', () => {
        const result = confirmUserMessage([], 'hello');
        assert.deepEqual(result, []);
    });

    test('processes multiple queued confirmations in order', () => {
        let pending = ['msg-A', 'msg-B', 'msg-C'];
        pending = confirmUserMessage(pending, 'msg-A');
        assert.deepEqual(pending, ['msg-B', 'msg-C']);
        pending = confirmUserMessage(pending, 'msg-B');
        assert.deepEqual(pending, ['msg-C']);
        pending = confirmUserMessage(pending, 'msg-C');
        assert.deepEqual(pending, []);
    });
});

// --- shouldShowInterruptChoice ---

describe('shouldShowInterruptChoice', () => {
    test('returns true when session is processing', () => {
        assert.equal(shouldShowInterruptChoice({ is_processing: true }), true);
    });

    test('returns false when session is idle', () => {
        assert.equal(shouldShowInterruptChoice({ is_processing: false }), false);
    });

    test('returns false for null session', () => {
        assert.equal(shouldShowInterruptChoice(null), false);
    });
});

// --- sessionClickMode ---

describe('sessionClickMode', () => {
    test('returns watch for active sessions', () => {
        assert.equal(sessionClickMode({ is_active: true }), 'watch');
    });

    test('returns view for idle sessions', () => {
        assert.equal(sessionClickMode({ is_active: false }), 'view');
    });
});

// --- pendingId ---

describe('pendingId', () => {
    test('returns unique IDs for different messages', () => {
        assert.notEqual(pendingId('hello'), pendingId('world'));
    });

    test('returns consistent ID for same message', () => {
        assert.equal(pendingId('test'), pendingId('test'));
    });

    test('starts with pending- prefix', () => {
        assert.ok(pendingId('hello').startsWith('pending-'));
    });
});

// --- validateMessageOrder ---

describe('validateMessageOrder', () => {
    test('detects duplicate consecutive messages', () => {
        const messages = [
            { role: 'user', content: 'hello' },
            { role: 'user', content: 'hello' },
            { role: 'assistant', content: 'hi' },
        ];
        const issues = validateMessageOrder(messages);
        assert.equal(issues.length, 1);
        assert.equal(issues[0].type, 'duplicate');
        assert.equal(issues[0].index, 1);
    });

    test('allows consecutive user messages with different content', () => {
        const messages = [
            { role: 'user', content: 'first' },
            { role: 'user', content: 'second' },
            { role: 'assistant', content: 'response' },
        ];
        const issues = validateMessageOrder(messages);
        assert.equal(issues.length, 0);
    });

    test('no issues for normal alternating conversation', () => {
        const messages = [
            { role: 'user', content: 'hello' },
            { role: 'assistant', content: 'hi' },
            { role: 'user', content: 'how are you' },
            { role: 'assistant', content: 'good' },
        ];
        const issues = validateMessageOrder(messages);
        assert.equal(issues.length, 0);
    });

    test('handles empty message list', () => {
        const issues = validateMessageOrder([]);
        assert.equal(issues.length, 0);
    });

    test('handles single message', () => {
        const issues = validateMessageOrder([{ role: 'user', content: 'solo' }]);
        assert.equal(issues.length, 0);
    });
});
