// Mock WebSocket server for E2E dashboard tests.
// Serves the dashboard static files and simulates the otto WebSocket protocol
// with canned event sequences. No LLM or copilot server needed.
//
// Usage: node internal/dashboard/tests/mock-server.js [port]

'use strict';

const http = require('http');
const fs = require('fs');
const path = require('path');
const { WebSocketServer } = require('ws');

const PORT = parseInt(process.argv[2] || '14098');
const STATIC_DIR = path.join(__dirname, '..', 'static');

// --- HTTP server for static files ---

const MIME = {
    '.html': 'text/html',
    '.js': 'application/javascript',
    '.css': 'text/css',
};

const server = http.createServer((req, res) => {
    let filePath = req.url === '/' ? '/index.html' : req.url;
    filePath = filePath.split('?')[0]; // strip query params

    // API stubs
    if (filePath === '/api/prs') {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify([]));
        return;
    }
    if (filePath === '/api/repos') {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify([]));
        return;
    }
    if (filePath.startsWith('/api/')) {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end('{}');
        return;
    }

    const fullPath = path.join(STATIC_DIR, filePath);
    const ext = path.extname(fullPath);
    const mime = MIME[ext] || 'application/octet-stream';

    fs.readFile(fullPath, (err, data) => {
        if (err) {
            res.writeHead(404);
            res.end('Not found');
            return;
        }
        res.writeHead(200, { 'Content-Type': mime });
        res.end(data);
    });
});

// --- WebSocket server ---

const wss = new WebSocketServer({ server });

// Canned sessions list
const sessions = [
    {
        name: 'test-session',
        model: 'claude-opus-4.6',
        session_id: 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee',
        working_dir: '/tmp',
        created_at: new Date().toISOString(),
        message_count: 0,
        is_processing: false,
        intent: '',
        state: 'idle',
    }
];

// Conversation history (grows as messages are sent)
let history = [];
let processing = false;
let processingQueue = false;
const messageQueue = [];

function sendJSON(ws, type, payload) {
    ws.send(JSON.stringify({ type, payload }));
}

// Broadcast to all connected clients.
function broadcastJSON(type, payload) {
    const data = JSON.stringify({ type, payload });
    wss.clients.forEach(c => { if (c.readyState === 1) c.send(data); });
}

// Process queued messages sequentially with realistic delays.
function processNextMessage() {
    if (messageQueue.length === 0) {
        processingQueue = false;
        return;
    }
    processingQueue = true;
    const { prompt, sessionName } = messageQueue.shift();

    // Broadcast user message immediately.
    broadcastJSON('user_message', { session_name: sessionName, content: prompt });
    history.push({ role: 'user', content: prompt, timestamp: new Date().toISOString() });

    processing = true;
    sessions[0].is_processing = true;
    sessions[0].state = 'processing';
    broadcastJSON('sessions_list', { sessions, active_session: sessionName });
    broadcastJSON('turn_start', { session_name: sessionName });

    // Simulate tool call + response.
    setTimeout(() => {
        const callId = 'call-' + Date.now();
        broadcastJSON('tool_started', {
            session_name: sessionName,
            tool_name: 'bash',
            call_id: callId,
            tool_input: JSON.stringify({ command: 'echo test' }),
        });

        setTimeout(() => {
            broadcastJSON('tool_completed', {
                session_name: sessionName,
                call_id: callId,
                result: 'test\n',
                success: true,
            });

            const response = 'This is a test response to: ' + prompt;
            broadcastJSON('content_delta', { session_name: sessionName, content: response });
            history.push({ role: 'assistant', content: response, timestamp: new Date().toISOString() });

            setTimeout(() => {
                processing = false;
                sessions[0].is_processing = false;
                sessions[0].state = 'idle';
                sessions[0].message_count = history.length;
                broadcastJSON('turn_end', { session_name: sessionName });
                broadcastJSON('sessions_list', { sessions, active_session: sessionName });
                // Process next queued message.
                processNextMessage();
            }, 100);
        }, 200);
    }, 300);
}

wss.on('connection', (ws) => {
    // Send initial state
    sendJSON(ws, 'dashboard_config', { owner_nickname: 'testuser' });
    sendJSON(ws, 'sessions_list', { sessions, active_session: '' });
    sendJSON(ws, 'persisted_sessions_list', { sessions: [] });
    sendJSON(ws, 'tunnel_status', { running: false, url: '' });

    ws.on('message', (data) => {
        const msg = JSON.parse(data.toString());

        switch (msg.type) {
            case 'get_sessions':
                sendJSON(ws, 'sessions_list', { sessions, active_session: sessions[0]?.name || '' });
                break;

            case 'get_history':
                sendJSON(ws, 'session_history', {
                    session_name: msg.payload.session_name,
                    messages: history,
                });
                break;

            case 'send_message': {
                const prompt = msg.payload.prompt;
                const sessionName = msg.payload.session_name;

                // Server-side queue: add to queue, process sequentially.
                messageQueue.push({ prompt, sessionName, ws });
                if (!processingQueue) {
                    processNextMessage();
                }
                break;
            }

            case 'create_session': {
                const name = msg.payload.name;
                sessions.push({
                    name,
                    model: msg.payload.model || 'claude-opus-4.6',
                    session_id: 'new-' + Date.now(),
                    working_dir: msg.payload.working_dir || '',
                    created_at: new Date().toISOString(),
                    message_count: 0,
                    is_processing: false,
                    intent: '',
                    state: 'idle',
                });
                sendJSON(ws, 'sessions_list', { sessions, active_session: name });
                break;
            }

            case 'list_worktrees':
                sendJSON(ws, 'worktrees_list', { worktrees: [] });
                break;

            case 'get_persisted_sessions':
                sendJSON(ws, 'persisted_sessions_list', { sessions: [] });
                break;

            case 'abort_session':
                // Simulate abort
                if (processing) {
                    processing = false;
                    sessions[0].is_processing = false;
                    sessions[0].state = 'idle';
                    sendJSON(ws, 'turn_end', { session_name: msg.payload.session_name });
                }
                break;
        }
    });
});

server.listen(PORT, () => {
    console.log(`Mock otto dashboard server listening on http://localhost:${PORT}`);
});
