// Otto Dashboard — Client-side application
'use strict';

const state = {
    ws: null,
    sessions: [],
    persistedSessions: [],
    activeSession: null,
    reconnectDelay: 1000,
    reconnectTimer: null,
    streamingContent: {},  // sessionName -> accumulated content
    tunnelRunning: false,
    tunnelURL: '',
};

// --- WebSocket ---

function connect() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const url = `${proto}//${location.host}/ws`;
    setConnectionStatus('connecting');

    const ws = new WebSocket(url);
    state.ws = ws;

    ws.onopen = () => {
        setConnectionStatus('connected');
        state.reconnectDelay = 1000;
        send('get_sessions', {});
    };

    ws.onmessage = (evt) => {
        try {
            const msg = JSON.parse(evt.data);
            handleMessage(msg);
        } catch (e) {
            console.error('Failed to parse message:', e);
        }
    };

    ws.onclose = () => {
        setConnectionStatus('disconnected');
        scheduleReconnect();
    };

    ws.onerror = () => {
        ws.close();
    };
}

function scheduleReconnect() {
    if (state.reconnectTimer) return;
    state.reconnectTimer = setTimeout(() => {
        state.reconnectTimer = null;
        connect();
    }, state.reconnectDelay);
    state.reconnectDelay = Math.min(state.reconnectDelay * 1.5, 10000);
}

function send(type, payload) {
    if (state.ws?.readyState === WebSocket.OPEN) {
        state.ws.send(JSON.stringify({ type, payload }));
    }
}

function setConnectionStatus(status) {
    const dot = document.getElementById('connection-status');
    dot.className = `status-dot ${status}`;
    dot.title = status.charAt(0).toUpperCase() + status.slice(1);
}

// --- Message Handlers ---

function handleMessage(msg) {
    switch (msg.type) {
        case 'sessions_list': handleSessionsList(msg.payload); break;
        case 'session_history': handleSessionHistory(msg.payload); break;
        case 'content_delta': handleContentDelta(msg.payload); break;
        case 'tool_started': handleToolStarted(msg.payload); break;
        case 'tool_completed': handleToolCompleted(msg.payload); break;
        case 'intent_changed': handleIntentChanged(msg.payload); break;
        case 'turn_start': handleTurnStart(msg.payload); break;
        case 'turn_end': handleTurnEnd(msg.payload); break;
        case 'error': handleError(msg.payload); break;
        case 'tunnel_status': handleTunnelStatus(msg.payload); break;
        case 'worktrees_list': handleWorktreesList(msg.payload); break;
        case 'persisted_sessions_list': handlePersistedSessionsList(msg.payload); break;
        case 'reasoning_delta': handleReasoningDelta(msg.payload); break;
    }
}

function handleSessionsList(payload) {
    state.sessions = payload.sessions || [];
    if (payload.active_session && !state.activeSession) {
        state.activeSession = payload.active_session;
    }
    renderSessionList();
    if (state.activeSession) {
        updateChatHeader();
    }
}

function handleSessionHistory(payload) {
    const container = document.getElementById('chat-messages');
    container.innerHTML = '';
    (payload.messages || []).forEach(msg => {
        appendChatMessage(msg.role, msg.content, false);
    });
    scrollToBottom();
}

function handleContentDelta(payload) {
    if (payload.session_name !== state.activeSession) return;
    const key = payload.session_name;
    if (!state.streamingContent[key]) {
        state.streamingContent[key] = '';
        appendChatMessage('assistant', '', true);
    }
    state.streamingContent[key] += payload.content;
    updateStreamingMessage(state.streamingContent[key]);
}

function handleToolStarted(payload) {
    if (payload.session_name !== state.activeSession) return;
    appendToolIndicator(payload.call_id || payload.call_id, payload.tool_name, 'running');
    updateActivity(`🔧 ${payload.tool_name}...`);
    updateSessionState(payload.session_name, 'processing');
}

function handleToolCompleted(payload) {
    if (payload.session_name !== state.activeSession) return;
    const id = payload.call_id || payload.call_id;
    updateToolIndicator(id, payload.success ? 'completed' : 'failed');
}

function handleIntentChanged(payload) {
    updateSessionIntent(payload.session_name, payload.intent);
    if (payload.session_name === state.activeSession) {
        updateActivity(`💭 ${payload.intent}`);
    }
}

function handleTurnStart(payload) {
    state.streamingContent[payload.session_name] = '';
    updateSessionState(payload.session_name, 'processing');
    if (payload.session_name === state.activeSession) {
        updateActivity('💭 Thinking...');
        showActivity(true);
    }
}

function handleTurnEnd(payload) {
    const key = payload.session_name;
    if (state.streamingContent[key]) {
        finalizeStreamingMessage();
        delete state.streamingContent[key];
    } else if (payload.session_name === state.activeSession) {
        // No streaming deltas were received — the response came as a complete
        // assistant.message. Refetch history to display it.
        send('get_history', { session_name: payload.session_name });
    }
    updateSessionState(payload.session_name, 'idle');
    if (payload.session_name === state.activeSession) {
        showActivity(false);
    }
    updateSessionMessageCount(payload.session_name);
}

function handleError(payload) {
    if (payload.session_name === state.activeSession) {
        appendChatMessage('system', `⚠️ Error: ${payload.message}`, false);
        scrollToBottom();
    }
    updateSessionState(payload.session_name, 'error');
}

function handleTunnelStatus(payload) {
    state.tunnelRunning = payload.running;
    state.tunnelURL = payload.url || '';
    renderTunnelStatus();
}

function handleWorktreesList(payload) {
    const select = document.getElementById('session-workdir');
    select.innerHTML = '<option value="">Default (current directory)</option>';
    (payload.worktrees || []).forEach(wt => {
        const opt = document.createElement('option');
        opt.value = wt.path;
        opt.textContent = `${wt.repo_name}/${wt.branch} — ${wt.path}`;
        select.appendChild(opt);
    });
}

function handleReasoningDelta(payload) {
    // Could render reasoning blocks; for now just update activity
    if (payload.session_name === state.activeSession) {
        updateActivity('🧠 Reasoning...');
    }
}

function handlePersistedSessionsList(payload) {
    state.persistedSessions = payload.sessions || [];
    renderPersistedSessionList();
}

// --- Rendering ---

function renderSessionList() {
    const container = document.getElementById('session-list');
    container.innerHTML = '';
    if (state.sessions.length === 0) {
        container.innerHTML = '<div style="padding:12px;color:var(--text-muted);font-size:12px;">No active sessions</div>';
        return;
    }
    state.sessions.forEach(s => {
        const card = document.createElement('div');
        card.className = `session-card${s.name === state.activeSession ? ' active' : ''}`;
        card.onclick = () => selectSession(s.name);
        const stateClass = s.is_processing ? 'processing' : (s.state === 'error' ? 'error' : 'idle');
        card.innerHTML = `
            <div class="session-card-header">
                <span class="session-card-name">${esc(s.name)}</span>
                <span class="session-card-dot ${stateClass}"></span>
            </div>
            <div class="session-card-meta">
                <span>${esc(s.model)}</span>
                <span>·</span>
                <span>${s.message_count} msgs</span>
            </div>
            ${s.intent ? `<div class="session-card-intent">${esc(s.intent)}</div>` : ''}
        `;
        container.appendChild(card);
    });
}

function renderPersistedSessionList() {
    const container = document.getElementById('persisted-session-list');
    container.innerHTML = '';
    if (state.persistedSessions.length === 0) {
        container.innerHTML = '<div style="padding:12px;color:var(--text-muted);font-size:12px;">No saved sessions</div>';
        return;
    }
    // Filter out sessions that are already active
    const activeIds = new Set(state.sessions.map(s => s.session_id || s.session_id));
    const available = state.persistedSessions.filter(p => !activeIds.has(p.session_id));
    if (available.length === 0) {
        container.innerHTML = '<div style="padding:12px;color:var(--text-muted);font-size:12px;">All sessions are active</div>';
        return;
    }
    available.forEach(p => {
        const card = document.createElement('div');
        card.className = 'persisted-card';
        card.onclick = () => resumePersistedSession(p.session_id);
        const shortId = p.session_id.substring(0, 8);
        const summary = p.summary || 'No summary';
        const ago = p.updated_at ? timeAgo(p.updated_at) : '';
        const agoClass = isRecentlyActive(p.updated_at) ? 'recently-active' : '';
        card.innerHTML = `
            <div class="persisted-card-header">
                <span class="persisted-card-id">${esc(shortId)}…</span>
                ${ago ? `<span class="persisted-card-ago ${agoClass}">${esc(ago)}</span>` : ''}
            </div>
            <div class="persisted-card-summary" title="${esc(p.summary || '')}">${esc(summary.length > 60 ? summary.substring(0, 57) + '...' : summary)}</div>
        `;
        container.appendChild(card);
    });
}

function resumePersistedSession(sessionId) {
    const session = state.persistedSessions.find(p => p.session_id === sessionId);
    const displayName = session?.summary || sessionId.substring(0, 8);
    // Truncate long names for the sidebar
    const name = displayName.length > 40 ? displayName.substring(0, 37) + '...' : displayName;
    send('resume_session', { session_id: sessionId, display_name: name });
    // Wait for sessions_list update then select
    setTimeout(() => {
        selectSession(name);
        send('get_persisted_sessions', {});
    }, 2000);
}

function renderTunnelStatus() {
    const stateEl = document.getElementById('tunnel-state');
    const urlDisplay = document.getElementById('tunnel-url-display');
    const urlInput = document.getElementById('tunnel-url-input');
    const startBtn = document.getElementById('start-tunnel-btn');
    const stopBtn = document.getElementById('stop-tunnel-btn');
    const badge = document.getElementById('tunnel-status');

    if (state.tunnelRunning) {
        stateEl.textContent = 'Running';
        stateEl.style.color = 'var(--green)';
        urlDisplay.classList.remove('hidden');
        urlInput.value = state.tunnelURL;
        startBtn.classList.add('hidden');
        stopBtn.classList.remove('hidden');
        badge.classList.remove('hidden');
        badge.textContent = '🔗 Tunnel';
        badge.title = state.tunnelURL;
    } else {
        stateEl.textContent = state.tunnelURL || 'Not running';
        stateEl.style.color = state.tunnelURL ? 'var(--red)' : 'var(--text-muted)';
        urlDisplay.classList.add('hidden');
        startBtn.classList.remove('hidden');
        stopBtn.classList.add('hidden');
        badge.classList.add('hidden');
    }
}

// --- Chat rendering ---

function appendChatMessage(role, content, streaming) {
    const container = document.getElementById('chat-messages');
    const div = document.createElement('div');
    div.className = `message ${role}`;
    if (streaming) {
        div.id = 'streaming-message';
        div.classList.add('streaming-cursor');
    }
    div.innerHTML = renderMarkdown(content);
    container.appendChild(div);
    scrollToBottom();
}

function updateStreamingMessage(content) {
    const el = document.getElementById('streaming-message');
    if (el) {
        el.innerHTML = renderMarkdown(content);
        scrollToBottom();
    }
}

function finalizeStreamingMessage() {
    const el = document.getElementById('streaming-message');
    if (el) {
        el.id = '';
        el.classList.remove('streaming-cursor');
    }
}

function appendToolIndicator(callId, toolName, status) {
    const container = document.getElementById('chat-messages');
    const div = document.createElement('div');
    div.className = `tool-indicator ${status}`;
    div.id = `tool-${callId}`;
    const icon = status === 'running' ? '⏳' : (status === 'completed' ? '✅' : '❌');
    div.innerHTML = `<span>${icon}</span> <span class="tool-name">${esc(toolName)}</span>`;
    container.appendChild(div);
    scrollToBottom();
}

function updateToolIndicator(callId, status) {
    const el = document.getElementById(`tool-${callId}`);
    if (el) {
        el.className = `tool-indicator ${status}`;
        const icon = status === 'completed' ? '✅' : '❌';
        const name = el.querySelector('.tool-name')?.textContent || '';
        el.innerHTML = `<span>${icon}</span> <span class="tool-name">${name}</span>`;
    }
}

function updateChatHeader() {
    const s = state.sessions.find(s => s.name === state.activeSession);
    if (!s) return;
    document.getElementById('chat-session-name').textContent = s.name;
    document.getElementById('chat-session-model').textContent = s.model;
    const statusEl = document.getElementById('chat-session-status');
    const stateStr = s.is_processing ? 'processing' : (s.state === 'error' ? 'error' : 'idle');
    statusEl.className = `status-badge ${stateStr}`;
    statusEl.textContent = stateStr;
}

function updateActivity(text) {
    const el = document.querySelector('.activity-text');
    if (el) el.textContent = text;
}

function showActivity(show) {
    const el = document.getElementById('chat-activity');
    if (el) el.classList.toggle('hidden', !show);
}

function updateSessionState(name, newState) {
    const s = state.sessions.find(s => s.name === name);
    if (s) {
        s.is_processing = newState === 'processing';
        s.state = newState;
        renderSessionList();
        if (name === state.activeSession) updateChatHeader();
    }
}

function updateSessionIntent(name, intent) {
    const s = state.sessions.find(s => s.name === name);
    if (s) {
        s.intent = intent;
        renderSessionList();
    }
}

function updateSessionMessageCount(name) {
    const s = state.sessions.find(s => s.name === name);
    if (s) {
        s.message_count++;
        renderSessionList();
    }
}

function scrollToBottom() {
    const container = document.getElementById('chat-messages');
    requestAnimationFrame(() => {
        container.scrollTop = container.scrollHeight;
    });
}

// --- Simple Markdown Renderer ---

function renderMarkdown(text) {
    if (!text) return '';
    let html = esc(text);

    // Code blocks
    html = html.replace(/```(\w*)\n([\s\S]*?)```/g, (_, lang, code) =>
        `<pre><code class="language-${lang}">${code}</code></pre>`
    );

    // Inline code
    html = html.replace(/`([^`]+)`/g, '<code>$1</code>');

    // Bold
    html = html.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');

    // Italic
    html = html.replace(/\*([^*]+)\*/g, '<em>$1</em>');

    // Links
    html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');

    // Line breaks
    html = html.replace(/\n/g, '<br>');

    return html;
}

function esc(str) {
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

function timeAgo(isoStr) {
    if (!isoStr) return '';
    const then = new Date(isoStr);
    const now = new Date();
    const secs = Math.floor((now - then) / 1000);
    if (secs < 10) return 'just now';
    if (secs < 60) return `${secs}s ago`;
    const mins = Math.floor(secs / 60);
    if (mins < 60) return `${mins}m ago`;
    const hours = Math.floor(mins / 60);
    if (hours < 24) return `${hours}h ago`;
    const days = Math.floor(hours / 24);
    return `${days}d ago`;
}

function isRecentlyActive(isoStr) {
    if (!isoStr) return false;
    const secs = (new Date() - new Date(isoStr)) / 1000;
    return secs < 120; // active within last 2 minutes
}

// --- Actions ---

function selectSession(name) {
    state.activeSession = name;
    renderSessionList();
    document.getElementById('empty-state').classList.add('hidden');
    document.getElementById('chat-view').classList.remove('hidden');
    document.getElementById('chat-messages').innerHTML = '';
    updateChatHeader();
    showActivity(false);
    send('get_history', { session_name: name });
    // Close sidebar on mobile
    if (window.innerWidth <= 768) {
        document.getElementById('sidebar').classList.remove('open');
    }
}

function showNewSessionDialog() {
    document.getElementById('new-session-dialog').classList.remove('hidden');
    document.getElementById('session-name').focus();
    send('list_worktrees', {});
}

function hideNewSessionDialog() {
    document.getElementById('new-session-dialog').classList.add('hidden');
}

function handleCreateSession(e) {
    e.preventDefault();
    const name = document.getElementById('session-name').value.trim();
    const model = document.getElementById('session-model').value;
    const workDir = document.getElementById('session-workdir').value;
    if (!name) return false;
    send('create_session', { name, model, working_dir: workDir });
    hideNewSessionDialog();
    document.getElementById('session-name').value = '';
    // Auto-select after a brief delay
    setTimeout(() => selectSession(name), 500);
    return false;
}

function sendMessage() {
    const input = document.getElementById('chat-input');
    const prompt = input.value.trim();
    if (!prompt || !state.activeSession) return;
    send('send_message', { session_name: state.activeSession, prompt });
    appendChatMessage('user', prompt, false);
    input.value = '';
    input.style.height = 'auto';
    document.getElementById('send-btn').disabled = true;
}

// --- Event Listeners ---

document.addEventListener('DOMContentLoaded', () => {
    connect();

    // Re-render relative timestamps every 10s.
    setInterval(() => { renderPersistedSessionList(); }, 10000);

    // Sidebar toggle
    document.getElementById('sidebar-toggle').addEventListener('click', () => {
        document.getElementById('sidebar').classList.toggle('open');
    });

    // Chat input
    const input = document.getElementById('chat-input');
    const sendBtn = document.getElementById('send-btn');

    input.addEventListener('input', () => {
        sendBtn.disabled = !input.value.trim();
        // Auto-resize
        input.style.height = 'auto';
        input.style.height = Math.min(input.scrollHeight, 120) + 'px';
    });

    input.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            sendMessage();
        }
    });

    sendBtn.addEventListener('click', sendMessage);

    // New session button
    document.getElementById('new-session-btn').addEventListener('click', showNewSessionDialog);

    // Tunnel controls
    document.getElementById('start-tunnel-btn').addEventListener('click', () => {
        send('start_tunnel', {});
    });
    document.getElementById('stop-tunnel-btn').addEventListener('click', () => {
        send('stop_tunnel', {});
    });
    document.getElementById('copy-tunnel-url').addEventListener('click', () => {
        const url = document.getElementById('tunnel-url-input').value;
        navigator.clipboard.writeText(url).catch(() => {});
    });

    // Escape to close modal
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') hideNewSessionDialog();
    });
});
