// Otto Dashboard — Client-side application
'use strict';

const state = {
    ws: null,
    sessions: [],
    persistedSessions: [],
    trackedPRs: [],
    selectedPR: null,
    activeSession: null,
    reconnectDelay: 1000,
    reconnectTimer: null,
    prPollTimer: null,
    streamingContent: {},  // sessionName -> accumulated content
    renderedMessageCount: 0,  // how many history messages we've rendered
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
        fetchPRs();
        // Refresh PRs every 60 seconds.
        if (state.prPollTimer) clearInterval(state.prPollTimer);
        state.prPollTimer = setInterval(fetchPRs, 60000);
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
        if (state.prPollTimer) { clearInterval(state.prPollTimer); state.prPollTimer = null; }
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
        case 'session_history':
            if (state._nextHistoryAppendOnly) {
                msg.payload._appendOnly = true;
                state._nextHistoryAppendOnly = false;
            }
            handleSessionHistory(msg.payload); break;
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
        case 'user_message': handleUserMessage(msg.payload); break;
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
    // Auto-select a session that was just resumed.
    if (state._pendingResume) {
        const found = state.sessions.find(s => s.name === state._pendingResume);
        if (found) {
            const name = state._pendingResume;
            state._pendingResume = null;
            selectSession(name);
            send('get_persisted_sessions', {});
        }
    }
}

function handleSessionHistory(payload) {
    const container = document.getElementById('chat-messages');
    const messages = (payload.messages || []).filter(msg => msg.content && msg.content.trim());

    if (payload._appendOnly) {
        // Only append messages we haven't rendered yet.
        const newMessages = messages.slice(state.renderedMessageCount);
        newMessages.forEach(msg => appendChatMessage(msg.role, msg.content, false));
    } else {
        container.innerHTML = '';
        messages.forEach(msg => appendChatMessage(msg.role, msg.content, false));
    }
    state.renderedMessageCount = messages.length;
    scrollToBottom();
}

function fetchAndAppendNew(sessionName) {
    // We'll handle this by sending get_history and marking it as append-only
    // via a one-shot flag.
    state._nextHistoryAppendOnly = true;
    send('get_history', { session_name: sessionName });
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
    // Extract a short description from tool input JSON
    var detail = '';
    if (payload.tool_input) {
        try {
            var args = JSON.parse(payload.tool_input);
            detail = args.path || args.command || args.pattern || args.description || args.file_text?.substring(0,30) || '';
            if (detail.length > 60) detail = detail.substring(0, 57) + '...';
        } catch(e) {}
    }
    var label = payload.tool_name + (detail ? ': ' + detail : '');
    appendToolIndicator(payload.call_id, label, 'running');
    updateActivity('🔧 ' + payload.tool_name + '...');
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
        // assistant.message. Fetch history and append only new messages
        // (without clearing existing tool indicators and content).
        fetchAndAppendNew(payload.session_name);
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
    if (payload.session_name === state.activeSession) {
        updateActivity('🧠 Reasoning...');
    }
}

function handleUserMessage(payload) {
    if (payload.session_name !== state.activeSession) return;
    appendChatMessage('user', payload.content, false);
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
    state._pendingResume = name;
    send('resume_session', { session_id: sessionId, display_name: name });
}

function handleTunnelStatus(payload) {
    state.tunnelRunning = payload.running;
    state.tunnelURL = payload.url || '';
    state.tunnelKeyedURL = payload.keyed_url || '';
    renderTunnelStatus();
}

function renderTunnelStatus() {
    const activeEl = document.getElementById('tunnel-active');
    const inactiveEl = document.getElementById('tunnel-inactive');
    const badge = document.getElementById('tunnel-status');

    if (state.tunnelRunning && state.tunnelURL) {
        activeEl.classList.remove('hidden');
        inactiveEl.classList.add('hidden');
        badge.classList.remove('hidden');
        badge.textContent = '🔗 Tunnel';
        badge.title = 'Click to copy tunnel URL';
    } else {
        activeEl.classList.add('hidden');
        inactiveEl.classList.remove('hidden');
        badge.classList.add('hidden');
    }
}

// --- Tracked PRs ---

function escapeHtml(str) {
    if (!str) return '';
    return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function fetchPRs() {
    const keyParam = new URLSearchParams(location.search).get('key');
    const url = '/api/prs' + (keyParam ? '?key=' + encodeURIComponent(keyParam) : '');
    fetch(url)
        .then(r => r.json())
        .then(prs => {
            state.trackedPRs = prs || [];
            renderPRs();
        })
        .catch(() => {
            state.trackedPRs = [];
            renderPRs();
        });
}

function renderPRs() {
    const container = document.getElementById('pr-list');
    if (!state.trackedPRs || state.trackedPRs.length === 0) {
        container.innerHTML = '<div class="empty-hint" style="color:var(--text-muted);font-size:0.85em;padding:0.3em 0.5em">No tracked PRs</div>';
        return;
    }
    container.innerHTML = '';
    for (const pr of state.trackedPRs) {
        const el = document.createElement('div');
        el.className = 'pr-item' + (state.selectedPR === pr.id ? ' active' : '');
        el.dataset.prId = pr.id;
        const statusIcon = prStatusIcon(pr.status, pr.pipeline_state);
        const waitingOn = prWaitingOn(pr);
        el.innerHTML = `
            <div class="pr-item-header">
                <span class="pr-status-icon">${statusIcon}</span>
                <span class="pr-title">${escapeHtml(pr.title || 'PR #' + pr.id)}</span>
            </div>
            <div class="pr-meta">
                <span class="pr-id">#${escapeHtml(pr.id)}</span>
                <span class="pr-repo">${escapeHtml(pr.repo || '')}</span>
                ${waitingOn ? `<span class="pr-waiting">${escapeHtml(waitingOn)}</span>` : ''}
            </div>`;
        el.addEventListener('click', () => selectPR(pr.id));
        container.appendChild(el);
    }
}

function selectPR(id) {
    state.selectedPR = id;
    state.activeSession = null;
    renderSessionList();
    renderPRs();
    document.getElementById('empty-state').classList.add('hidden');
    document.getElementById('chat-view').classList.add('hidden');
    document.getElementById('pr-detail-view').classList.remove('hidden');
    document.getElementById('pr-detail-body').innerHTML = '<div style="color:var(--text-muted);padding:20px">Loading…</div>';

    const keyParam = new URLSearchParams(location.search).get('key');
    const url = '/api/prs/' + encodeURIComponent(id) + (keyParam ? '?key=' + encodeURIComponent(keyParam) : '');
    fetch(url)
        .then(r => { if (!r.ok) throw new Error('Not found'); return r.json(); })
        .then(pr => renderPRDetail(pr))
        .catch(err => {
            document.getElementById('pr-detail-body').innerHTML =
                '<div style="color:var(--red);padding:20px">Failed to load PR: ' + escapeHtml(err.message) + '</div>';
        });

    if (window.innerWidth <= 768) {
        document.getElementById('sidebar').classList.remove('open');
    }
}

function renderPRDetail(pr) {
    const icon = prStatusIcon(pr.status, pr.pipeline_state);
    document.getElementById('pr-detail-icon').textContent = icon;
    document.getElementById('pr-detail-title').textContent = pr.title || 'PR #' + pr.id;
    document.getElementById('pr-detail-link').href = pr.url || '#';

    const waitingOn = prWaitingOn(pr);
    const meta = [
        `<span class="pr-detail-tag">${escapeHtml(pr.provider)}</span>`,
        `<span class="pr-detail-tag">${escapeHtml(pr.repo)}</span>`,
        `<span class="pr-detail-tag">${escapeHtml(pr.status)}</span>`,
        pr.pipeline_state ? `<span class="pr-detail-tag">pipeline: ${escapeHtml(pr.pipeline_state)}</span>` : '',
        waitingOn ? `<span class="pr-detail-tag pr-waiting">waiting: ${escapeHtml(waitingOn)}</span>` : '',
        pr.fix_attempts > 0 ? `<span class="pr-detail-tag">fixes: ${pr.fix_attempts}/${pr.max_fix_attempts}</span>` : '',
        pr.last_checked ? `<span class="pr-detail-tag">checked: ${new Date(pr.last_checked).toLocaleTimeString()}</span>` : '',
    ].filter(Boolean).join(' ');
    document.getElementById('pr-detail-meta').innerHTML = meta;

    // Render body as simple formatted HTML.
    const body = pr.body || '*No activity recorded yet.*';
    document.getElementById('pr-detail-body').innerHTML = renderMarkdownSimple(body);
}

function renderMarkdownSimple(md) {
    // Lightweight markdown → HTML (headings, bold, italic, code, links, lists).
    return md
        .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
        .replace(/^### (.+)$/gm, '<h3>$1</h3>')
        .replace(/^## (.+)$/gm, '<h2>$1</h2>')
        .replace(/^# (.+)$/gm, '<h1>$1</h1>')
        .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
        .replace(/\*(.+?)\*/g, '<em>$1</em>')
        .replace(/`([^`]+)`/g, '<code>$1</code>')
        .replace(/^- (.+)$/gm, '<li>$1</li>')
        .replace(/(<li>.*<\/li>)/gs, '<ul>$1</ul>')
        .replace(/<\/ul>\s*<ul>/g, '')
        .replace(/\n\n/g, '<br><br>')
        .replace(/\n/g, '<br>');
}

function prStatusIcon(status, pipelineState) {
    switch (status) {
        case 'merged': return '✅';
        case 'abandoned': return '🚫';
        case 'fixing': return '🔧';
        case 'failed': return '❌';
        case 'green': return '🟢';
        default:
            if (pipelineState === 'failed') return '❌';
            if (pipelineState === 'inProgress' || pipelineState === 'running') return '⏳';
            return '👁️';
    }
}

function prWaitingOn(pr) {
    const parts = [];
    if (pr.status === 'merged') return 'merged';
    if (pr.status === 'abandoned') return 'abandoned';
    if (pr.has_conflicts) parts.push('conflicts');
    if (pr.pipeline_state && pr.pipeline_state !== 'succeeded') parts.push('pipeline');
    if (!pr.feedback_done) parts.push('feedback');
    if (!pr.merlinbot_done) parts.push('merlinbot');
    return parts.length > 0 ? parts.join(', ') : '';
}

// --- Session Search ---

let _searchDebounce = null;
function onSearchInput(e) {
    const q = e.target.value.trim();
    clearTimeout(_searchDebounce);
    const container = document.getElementById('search-results');
    if (!q) {
        container.classList.add('hidden');
        container.innerHTML = '';
        return;
    }
    _searchDebounce = setTimeout(() => searchSessions(q), 300);
}

function searchSessions(query) {
    const keyParam = new URLSearchParams(location.search).get('key');
    const url = '/api/sessions/search?q=' + encodeURIComponent(query) + (keyParam ? '&key=' + encodeURIComponent(keyParam) : '');
    fetch(url)
        .then(r => r.json())
        .then(results => renderSearchResults(results))
        .catch(() => renderSearchResults([]));
}

function renderSearchResults(results) {
    const container = document.getElementById('search-results');
    if (!results || results.length === 0) {
        container.innerHTML = '<div class="empty-hint" style="padding:6px 0;color:var(--text-muted);font-size:12px">No results</div>';
        container.classList.remove('hidden');
        return;
    }
    container.innerHTML = '';
    container.classList.remove('hidden');
    for (const r of results) {
        const el = document.createElement('div');
        el.className = 'search-result-item';
        el.innerHTML = `
            <div class="search-result-title">${escapeHtml(r.summary || r.session_id.substring(0, 8))}</div>
            <div class="search-result-snippet">${escapeHtml(r.snippet).replace(/»/g, '<mark>').replace(/«/g, '</mark>')}</div>
            <div class="search-result-meta">${r.hits} hit${r.hits !== 1 ? 's' : ''}${r.updated_at ? ' · ' + new Date(r.updated_at).toLocaleDateString() : ''}</div>`;
        el.addEventListener('click', () => {
            document.getElementById('session-search').value = '';
            container.classList.add('hidden');
            resumePersistedSession(r.session_id);
        });
        container.appendChild(el);
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

    // First, extract fenced code blocks to protect them from further processing.
    var codeBlocks = [];
    text = text.replace(/```(\w*)\n([\s\S]*?)```/g, function(_, lang, code) {
        var idx = codeBlocks.length;
        codeBlocks.push('<pre class="code-block"><div class="code-lang">' + esc(lang || 'text') + '</div><code>' + esc(code) + '</code></pre>');
        return '\x00CODEBLOCK' + idx + '\x00';
    });

    // Process blocks line by line.
    var lines = text.split('\n');
    var result = [];
    var inList = false;

    for (var i = 0; i < lines.length; i++) {
        var line = lines[i];

        // Code block placeholder
        var cbMatch = line.match(/^\x00CODEBLOCK(\d+)\x00$/);
        if (cbMatch) {
            if (inList) { result.push('</ul>'); inList = false; }
            result.push(codeBlocks[parseInt(cbMatch[1])]);
            continue;
        }

        // Headers
        if (/^#### (.+)/.test(line)) {
            if (inList) { result.push('</ul>'); inList = false; }
            result.push('<h4>' + inlineMarkdown(line.replace(/^#### /, '')) + '</h4>');
            continue;
        }
        if (/^### (.+)/.test(line)) {
            if (inList) { result.push('</ul>'); inList = false; }
            result.push('<h3>' + inlineMarkdown(line.replace(/^### /, '')) + '</h3>');
            continue;
        }
        if (/^## (.+)/.test(line)) {
            if (inList) { result.push('</ul>'); inList = false; }
            result.push('<h2>' + inlineMarkdown(line.replace(/^## /, '')) + '</h2>');
            continue;
        }
        if (/^# (.+)/.test(line)) {
            if (inList) { result.push('</ul>'); inList = false; }
            result.push('<h1>' + inlineMarkdown(line.replace(/^# /, '')) + '</h1>');
            continue;
        }

        // Unordered list items
        if (/^[\-\*] (.+)/.test(line)) {
            if (!inList) { result.push('<ul>'); inList = true; }
            result.push('<li>' + inlineMarkdown(line.replace(/^[\-\*] /, '')) + '</li>');
            continue;
        }

        // Ordered list items
        if (/^\d+\. (.+)/.test(line)) {
            if (!inList) { result.push('<ol>'); inList = true; }
            result.push('<li>' + inlineMarkdown(line.replace(/^\d+\. /, '')) + '</li>');
            continue;
        }

        // Blockquote
        if (/^> (.+)/.test(line)) {
            if (inList) { result.push('</ul>'); inList = false; }
            result.push('<blockquote>' + inlineMarkdown(line.replace(/^> /, '')) + '</blockquote>');
            continue;
        }

        // End list if we hit a non-list line
        if (inList) { result.push('</ul>'); inList = false; }

        // Empty line = paragraph break
        if (line.trim() === '') {
            result.push('<div class="md-spacer"></div>');
            continue;
        }

        // Regular text
        result.push('<p>' + inlineMarkdown(line) + '</p>');
    }
    if (inList) result.push('</ul>');

    return result.join('');
}

// Inline markdown processing (bold, italic, code, links)
function inlineMarkdown(text) {
    var h = esc(text);
    // Inline code (must be before bold/italic to avoid conflicts)
    h = h.replace(/`([^`]+)`/g, '<code class="inline-code">$1</code>');
    // Bold
    h = h.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
    // Italic
    h = h.replace(/\*([^*]+)\*/g, '<em>$1</em>');
    // Links
    h = h.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');
    return h;
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
    state.selectedPR = null;
    state.renderedMessageCount = 0;
    renderSessionList();
    renderPRs();
    document.getElementById('empty-state').classList.add('hidden');
    document.getElementById('chat-view').classList.remove('hidden');
    document.getElementById('pr-detail-view').classList.add('hidden');
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
    // Don't append locally — the server broadcasts user_message to all clients
    // including us, so we'll get it back via handleUserMessage.
    input.value = '';
    input.style.height = 'auto';
    document.getElementById('send-btn').disabled = true;
}

function shareSession() {
    if (!state.activeSession) return;

    // Build a simple share dialog inline.
    var mode = 'readonly';
    var dur = 60;
    var html = '<div style="background:var(--bg-secondary);border:1px solid var(--border);border-radius:8px;padding:20px;max-width:380px;margin:auto;position:fixed;top:50%;left:50%;transform:translate(-50%,-50%);z-index:200">';
    html += '<h3 style="margin:0 0 12px;font-size:15px">Share Session</h3>';
    html += '<div style="margin-bottom:10px"><label style="font-size:12px;color:var(--text-secondary)">Mode</label><br>';
    html += '<select id="share-mode" style="width:100%;padding:6px;background:var(--bg-tertiary);border:1px solid var(--border);border-radius:4px;color:var(--text-primary);font-size:13px">';
    html += '<option value="readonly">🔒 Read-only (view only)</option>';
    html += '<option value="readwrite">✏️ Read-write (can send prompts)</option>';
    html += '</select></div>';
    html += '<div style="margin-bottom:12px"><label style="font-size:12px;color:var(--text-secondary)">Expires in</label><br>';
    html += '<select id="share-duration" style="width:100%;padding:6px;background:var(--bg-tertiary);border:1px solid var(--border);border-radius:4px;color:var(--text-primary);font-size:13px">';
    html += '<option value="15">15 minutes</option>';
    html += '<option value="60" selected>1 hour</option>';
    html += '<option value="240">4 hours</option>';
    html += '<option value="1440">24 hours</option>';
    html += '</select></div>';
    html += '<div style="display:flex;gap:8px;justify-content:flex-end">';
    html += '<button onclick="this.closest(\'div\').parentElement.remove();document.getElementById(\'share-backdrop\')?.remove()" class="btn">Cancel</button>';
    html += '<button onclick="doShare()" class="btn btn-primary">Create Link</button>';
    html += '</div></div>';
    html += '<div id="share-backdrop" onclick="document.getElementById(\'share-dialog\')?.remove();this.remove()" style="position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,0.6);z-index:199"></div>';

    var container = document.createElement('div');
    container.id = 'share-dialog';
    container.innerHTML = html;
    document.body.appendChild(container);
}

function doShare() {
    var mode = document.getElementById('share-mode').value;
    var dur = parseInt(document.getElementById('share-duration').value);
    document.getElementById('share-dialog')?.remove();
    document.getElementById('share-backdrop')?.remove();

    fetch('/api/share', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_name: state.activeSession, duration_min: dur, mode: mode })
    })
    .then(function(r) { return r.json(); })
    .then(function(data) {
        var fullUrl = location.origin + data.url;
        var modeLabel = data.mode === 'readwrite' ? '✏️ Read-write' : '🔒 Read-only';
        navigator.clipboard.writeText(fullUrl).then(function() {
            alert(modeLabel + ' share link copied!\n\nExpires: ' + new Date(data.expires).toLocaleTimeString() + '\n\n' + fullUrl);
        }).catch(function() {
            prompt(modeLabel + ' share link:', fullUrl);
        });
    })
    .catch(function(err) { alert('Failed: ' + err); });
}

// --- Event Listeners ---

document.addEventListener('DOMContentLoaded', () => {
    connect();

    // Re-render relative timestamps every 10s.
    setInterval(() => { renderPersistedSessionList(); }, 10000);

    // Session search
    document.getElementById('session-search').addEventListener('input', onSearchInput);

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

    // Share button
    document.getElementById('share-btn').addEventListener('click', shareSession);

    // Tunnel badge — click to copy URL
    document.getElementById('tunnel-status').addEventListener('click', () => {
        const url = state.tunnelKeyedURL || state.tunnelURL;
        if (url) {
            navigator.clipboard.writeText(url).then(() => {
                const badge = document.getElementById('tunnel-status');
                badge.textContent = '✅ Copied!';
                setTimeout(() => { badge.textContent = '🔗 Tunnel'; }, 1500);
            }).catch(() => {});
        }
    });

    // Escape to close modal
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') hideNewSessionDialog();
    });
});
