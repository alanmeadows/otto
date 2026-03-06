// Otto Dashboard — Client-side application
'use strict';

const state = {
    ws: null,
    sessions: [],
    persistedSessions: [],
    trackedPRs: [],
    trackedRepos: [],
    selectedPR: null,
    selectedRepo: null,
    activeSession: null,
    reconnectDelay: 1000,
    reconnectTimer: null,
    prPollTimer: null,
    streamingContent: {},  // sessionName -> accumulated content
    renderedMessageCount: 0,  // how many history messages we've rendered
    tunnelRunning: false,
    tunnelURL: '',
    ownerNickname: 'owner',
    userScrolledUp: false,  // true when user has scrolled away from bottom
    pendingPrompt: null,    // prompt text awaiting server broadcast
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
        // Re-fetch active session history to catch messages missed while disconnected.
        if (state.activeSession) {
            send('get_history', { session_name: state.activeSession });
        }
        fetchPRs();
        fetchRepos();
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

// Reconnect immediately when the page becomes visible again (e.g. mobile
// browser returning from background). Mobile OSes freeze timers and kill
// WebSockets, so the normal reconnect loop may not fire promptly.
document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible' && (!state.ws || state.ws.readyState !== WebSocket.OPEN)) {
        if (state.reconnectTimer) { clearTimeout(state.reconnectTimer); state.reconnectTimer = null; }
        state.reconnectDelay = 1000;
        connect();
    }
});

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
        case 'dashboard_config': handleDashboardConfig(msg.payload); break;
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

    // Clean up any pending message that might still be showing.
    const pending = document.getElementById('pending-message');
    if (pending) pending.remove();

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
            detail = args.intent || args.path || args.command || args.pattern || args.description || args.query || args.file_text?.substring(0,30) || '';
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
    const id = payload.call_id;
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

var _allWorktrees = [];

function handleWorktreesList(payload) {
    _allWorktrees = (payload.worktrees || []).map(wt => ({
        path: wt.path,
        label: wt.repo_name + '/' + wt.branch + ' — ' + wt.path,
        repo: wt.repo_name,
        branch: wt.branch,
    }));

    // Show/hide the "no repos" hint.
    const hint = document.getElementById('no-repos-hint');
    if (hint) hint.classList.toggle('hidden', _allWorktrees.length > 0);

    // Reset hidden value.
    document.getElementById('session-workdir').value = '';
    document.getElementById('session-workdir-search').value = '';
}

function filterWorktrees(query) {
    const dropdown = document.getElementById('workdir-dropdown');
    const q = query.toLowerCase();

    // Always include "Default" as first option.
    var matches = [{ path: '', label: 'Default (current directory)', repo: '', branch: '' }];
    _allWorktrees.forEach(wt => {
        if (!q || wt.label.toLowerCase().includes(q) || wt.path.toLowerCase().includes(q)) {
            matches.push(wt);
        }
    });

    if (matches.length === 0) {
        dropdown.classList.add('hidden');
        return;
    }

    dropdown.innerHTML = '';
    matches.forEach(m => {
        const div = document.createElement('div');
        div.className = 'workdir-option';
        div.textContent = m.label;
        div.addEventListener('click', () => {
            document.getElementById('session-workdir').value = m.path;
            document.getElementById('session-workdir-search').value = m.path ? m.label : '';
            dropdown.classList.add('hidden');
        });
        dropdown.appendChild(div);
    });
    dropdown.classList.remove('hidden');
}

function addRepoFromDialog() {
    const name = document.getElementById('add-repo-name').value.trim();
    const dir = document.getElementById('add-repo-dir').value.trim();
    if (!name || !dir) return;

    const keyParam = new URLSearchParams(location.search).get('key');
    const url = '/api/repos' + (keyParam ? '?key=' + encodeURIComponent(keyParam) : '');
    fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: name, primary_dir: dir }),
    })
    .then(r => {
        if (!r.ok) return r.text().then(t => { throw new Error(t); });
        return r.json();
    })
    .then(() => {
        document.getElementById('add-repo-name').value = '';
        document.getElementById('add-repo-dir').value = '';
        // Server broadcasts updated worktrees list automatically.
        send('list_worktrees', {});
    })
    .catch(err => alert('Failed to add repo: ' + err.message));
}

function handleReasoningDelta(payload) {
    if (payload.session_name === state.activeSession) {
        updateActivity('🧠 Reasoning...');
    }
}

function handleUserMessage(payload) {
    if (payload.session_name !== state.activeSession) return;

    // Check if there's a pending message placeholder to replace.
    const pending = document.getElementById('pending-message');
    if (pending) {
        pending.remove();
    }

    appendChatMessage('user', payload.content, false);
    // Keep renderedMessageCount in sync so fetchAndAppendNew (turn_end
    // fallback) doesn't re-render this message from the history slice.
    state.renderedMessageCount++;
}

function handlePersistedSessionsList(payload) {
    state.persistedSessions = payload.sessions || [];
    renderPersistedSessionList();
}

function handleDashboardConfig(payload) {
    if (payload.owner_nickname) {
        state.ownerNickname = payload.owner_nickname;
    }
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
    state.selectedRepo = null;
    state.activeSession = null;
    renderSessionList();
    renderPRs();
    renderRepos();
    document.getElementById('empty-state').classList.add('hidden');
    document.getElementById('chat-view').classList.add('hidden');
    document.getElementById('pr-detail-view').classList.remove('hidden');
    document.getElementById('repo-detail-view').classList.add('hidden');
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

    // Branch info
    const branchEl = document.getElementById('pr-detail-branches');
    if (pr.branch || pr.target) {
        branchEl.innerHTML = '<span class="branch-label">' + escapeHtml(pr.branch || '?') +
            '</span> <span class="branch-arrow">→</span> <span class="branch-label">' +
            escapeHtml(pr.target || '?') + '</span>';
    } else {
        branchEl.innerHTML = '';
    }

    // Compact meta tags (provider, repo, last checked)
    const meta = [
        `<span class="pr-detail-tag">${escapeHtml(pr.provider)}</span>`,
        `<span class="pr-detail-tag">${escapeHtml(pr.repo || '')}</span>`,
        pr.last_checked ? `<span class="pr-detail-tag">checked ${timeAgo(pr.last_checked)}</span>` : '',
    ].filter(Boolean).join(' ');
    document.getElementById('pr-detail-meta').innerHTML = meta;

    // Status grid — visual cards for each stage dimension
    const grid = document.getElementById('pr-detail-status-grid');
    grid.innerHTML = renderStatusGrid(pr);

    // Fix attempts progress
    const progressEl = document.getElementById('pr-detail-progress');
    if (pr.max_fix_attempts > 0) {
        const pct = Math.min(100, Math.round((pr.fix_attempts / pr.max_fix_attempts) * 100));
        const barColor = pr.fix_attempts >= pr.max_fix_attempts ? 'var(--red)' : 'var(--accent)';
        progressEl.innerHTML = `
            <div class="progress-header">
                <span>Fix attempts</span>
                <span>${pr.fix_attempts} / ${pr.max_fix_attempts}</span>
            </div>
            <div class="progress-bar-track">
                <div class="progress-bar-fill" style="width:${pct}%;background:${barColor}"></div>
            </div>`;
    } else {
        progressEl.innerHTML = '';
    }

    // Timeline — parse attempt entries from body
    const timelineEl = document.getElementById('pr-detail-timeline');
    timelineEl.innerHTML = renderFixTimeline(pr.body || '');

    // Remaining body (rendered with full markdown)
    const body = stripTimelineEntries(pr.body || '');
    const bodyEl = document.getElementById('pr-detail-body');
    if (body.trim()) {
        bodyEl.innerHTML = renderMarkdown(body);
    } else {
        bodyEl.innerHTML = '';
    }
}

function renderStatusGrid(pr) {
    const cards = [];

    // Overall status
    const statusColors = {
        merged: 'var(--green)', abandoned: 'var(--text-muted)', fixing: 'var(--yellow)',
        failed: 'var(--red)', green: 'var(--green)', watching: 'var(--accent)',
    };
    cards.push(statusCard('Status', prStatusIcon(pr.status, pr.pipeline_state),
        pr.status || 'unknown', statusColors[pr.status] || 'var(--text-secondary)'));

    // Pipeline
    const pipeIcons = { succeeded: '✅', failed: '❌', running: '⏳', inProgress: '⏳', pending: '⏳', unknown: '❓' };
    const pipeColors = { succeeded: 'var(--green)', failed: 'var(--red)', running: 'var(--yellow)', inProgress: 'var(--yellow)' };
    cards.push(statusCard('Pipeline', pipeIcons[pr.pipeline_state] || '❓',
        pr.pipeline_state || 'unknown', pipeColors[pr.pipeline_state] || 'var(--text-secondary)'));

    // Review feedback
    cards.push(statusCard('Reviews', pr.feedback_done ? '✅' : '💬',
        pr.feedback_done ? 'resolved' : 'pending', pr.feedback_done ? 'var(--green)' : 'var(--yellow)'));

    // Conflicts
    cards.push(statusCard('Conflicts', pr.has_conflicts ? '⚠️' : '✅',
        pr.has_conflicts ? 'conflicts' : 'clean', pr.has_conflicts ? 'var(--red)' : 'var(--green)'));

    // MerlinBot (only show if relevant — when not done)
    if (!pr.merlinbot_done || pr.provider === 'ado') {
        cards.push(statusCard('MerlinBot', pr.merlinbot_done ? '✅' : '🤖',
            pr.merlinbot_done ? 'clear' : 'pending', pr.merlinbot_done ? 'var(--green)' : 'var(--yellow)'));
    }

    return cards.join('');
}

function statusCard(label, icon, value, color) {
    return `<div class="status-card">
        <div class="status-card-icon">${icon}</div>
        <div class="status-card-label">${escapeHtml(label)}</div>
        <div class="status-card-value" style="color:${color}">${escapeHtml(value)}</div>
    </div>`;
}

function renderFixTimeline(body) {
    // Parse "### Attempt N" or "### Infra Retry" entries
    const regex = /^### (Attempt \d+|Infra Retry)\s*[-–—]\s*(.+)$/gm;
    const entries = [];
    let match;
    while ((match = regex.exec(body)) !== null) {
        const title = match[1];
        const timestamp = match[2].trim();
        // Grab lines until next heading or end
        const startIdx = match.index + match[0].length;
        const nextHeading = body.indexOf('\n###', startIdx);
        const block = body.substring(startIdx, nextHeading > -1 ? nextHeading : undefined).trim();
        entries.push({ title, timestamp, block });
    }
    if (entries.length === 0) return '';

    let html = '<h4 class="timeline-heading">Fix History</h4>';
    html += '<div class="timeline">';
    entries.forEach((e, i) => {
        const isLast = i === entries.length - 1;
        const timeStr = formatTimelineDate(e.timestamp);
        // Parse key-value lines (e.g. "- **Trigger**: pipeline failure")
        const details = parseTimelineDetails(e.block);
        html += `<div class="timeline-entry${isLast ? ' latest' : ''}">
            <div class="timeline-dot"></div>
            <div class="timeline-content">
                <div class="timeline-title">${escapeHtml(e.title)}</div>
                <div class="timeline-time">${escapeHtml(timeStr)}</div>
                ${details}
            </div>
        </div>`;
    });
    html += '</div>';
    return html;
}

function parseTimelineDetails(block) {
    if (!block) return '';
    const lines = block.split('\n').filter(l => l.trim().startsWith('-'));
    if (lines.length === 0) return '<div class="timeline-detail">' + escapeHtml(block.substring(0, 200)) + '</div>';
    let html = '<div class="timeline-details">';
    lines.forEach(line => {
        const cleaned = line.replace(/^-\s*/, '');
        // Try to parse **Key**: Value
        const kvMatch = cleaned.match(/^\*\*(.+?)\*\*:\s*(.+)$/);
        if (kvMatch) {
            html += `<div class="timeline-kv"><span class="timeline-key">${escapeHtml(kvMatch[1])}</span> ${escapeHtml(kvMatch[2])}</div>`;
        } else {
            html += `<div class="timeline-kv">${escapeHtml(cleaned)}</div>`;
        }
    });
    html += '</div>';
    return html;
}

function formatTimelineDate(str) {
    try {
        const d = new Date(str);
        if (isNaN(d.getTime())) return str;
        return d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
    } catch (e) {
        return str;
    }
}

function stripTimelineEntries(body) {
    // Remove ### Attempt / ### Infra Retry blocks (heading + following non-heading lines).
    return body.replace(/^### (?:Attempt \d+|Infra Retry)[^\n]*\n(?:(?!^###)[^\n]*\n?)*/gm, '').trim();
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

function addPRFromDashboard() {
    var prURL = prompt('PR URL (GitHub or Azure DevOps):');
    if (!prURL) return;
    const keyParam = new URLSearchParams(location.search).get('key');
    const url = '/api/prs' + (keyParam ? '?key=' + encodeURIComponent(keyParam) : '');
    fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url: prURL }),
    })
    .then(r => {
        if (!r.ok) return r.text().then(t => { throw new Error(t); });
        return r.json();
    })
    .then(pr => {
        fetchPRs();
        if (pr && pr.id) selectPR(pr.id);
    })
    .catch(err => alert('Failed to add PR: ' + err.message));
}

function removePR(id) {
    if (!confirm('Stop tracking PR #' + id + '?')) return;
    const keyParam = new URLSearchParams(location.search).get('key');
    const url = '/api/prs/' + encodeURIComponent(id) + (keyParam ? '?key=' + encodeURIComponent(keyParam) : '');
    fetch(url, { method: 'DELETE' })
    .then(r => {
        if (!r.ok) return r.text().then(t => { throw new Error(t); });
        state.selectedPR = null;
        document.getElementById('pr-detail-view').classList.add('hidden');
        document.getElementById('empty-state').classList.remove('hidden');
        fetchPRs();
    })
    .catch(err => alert('Failed to remove PR: ' + err.message));
}

// --- Repositories ---

function fetchRepos() {
    const keyParam = new URLSearchParams(location.search).get('key');
    const url = '/api/repos' + (keyParam ? '?key=' + encodeURIComponent(keyParam) : '');
    fetch(url)
        .then(r => r.json())
        .then(repos => {
            state.trackedRepos = repos || [];
            renderRepos();
        })
        .catch(() => {
            state.trackedRepos = [];
            renderRepos();
        });
}

function renderRepos() {
    const container = document.getElementById('repo-list');
    if (!state.trackedRepos || state.trackedRepos.length === 0) {
        container.innerHTML = '<div class="empty-hint" style="color:var(--text-muted);font-size:0.85em;padding:0.3em 0.5em">No repositories</div>';
        return;
    }
    container.innerHTML = '';
    for (const repo of state.trackedRepos) {
        const el = document.createElement('div');
        el.className = 'repo-item' + (state.selectedRepo === repo.name ? ' active' : '');
        const strategyIcon = repo.git_strategy === 'worktree' ? '🌿' : (repo.git_strategy === 'branch' ? '🔀' : '👁️');
        el.innerHTML = `
            <div class="repo-item-header">
                <span class="repo-status-icon">${strategyIcon}</span>
                <span class="repo-name">${escapeHtml(repo.name)}</span>
            </div>
            <div class="repo-meta">
                <span>${escapeHtml(repo.primary_dir.replace(/^\/home\/[^/]+\//, '~/'))}</span>
            </div>`;
        el.addEventListener('click', () => selectRepo(repo.name));
        container.appendChild(el);
    }
}

function selectRepo(name) {
    state.selectedRepo = name;
    state.selectedPR = null;
    state.activeSession = null;
    renderSessionList();
    renderPRs();
    renderRepos();
    document.getElementById('empty-state').classList.add('hidden');
    document.getElementById('chat-view').classList.add('hidden');
    document.getElementById('pr-detail-view').classList.add('hidden');
    document.getElementById('repo-detail-view').classList.remove('hidden');

    const repo = state.trackedRepos.find(r => r.name === name);
    if (repo) renderRepoDetail(repo);

    if (window.innerWidth <= 768) {
        document.getElementById('sidebar').classList.remove('open');
    }
}

function renderRepoDetail(repo) {
    document.getElementById('repo-detail-name').textContent = repo.name;
    document.getElementById('repo-detail-path').textContent = repo.primary_dir;

    const fields = document.getElementById('repo-detail-fields');
    const strategy = repo.git_strategy || 'worktree';
    const strategyLabel = { worktree: 'Worktree (recommended)', branch: 'Branch', 'hands-off': 'Hands-off (read only)' };

    fields.innerHTML = `
        <div class="repo-field-group">
            <div class="repo-field">
                <label>Primary Directory</label>
                <input id="repo-edit-dir" type="text" value="${escapeHtml(repo.primary_dir)}" class="repo-field-input">
            </div>
            <div class="repo-field">
                <label>Worktree Directory</label>
                <input id="repo-edit-wt" type="text" value="${escapeHtml(repo.worktree_dir || '')}" placeholder="(optional)" class="repo-field-input">
            </div>
            <div class="repo-field">
                <label>Git Strategy</label>
                <select id="repo-edit-strategy" class="repo-field-input">
                    <option value="worktree"${strategy === 'worktree' ? ' selected' : ''}>Worktree</option>
                    <option value="branch"${strategy === 'branch' ? ' selected' : ''}>Branch</option>
                    <option value="hands-off"${strategy === 'hands-off' ? ' selected' : ''}>Hands-off</option>
                </select>
            </div>
            <div class="repo-field">
                <label>Branch Template</label>
                <input id="repo-edit-branch" type="text" value="${escapeHtml(repo.branch_template || 'otto/{{.Name}}')}" class="repo-field-input">
            </div>
            <div style="margin-top:12px;display:flex;justify-content:flex-end">
                <button class="btn btn-primary btn-sm" onclick="saveRepoEdits('${escapeHtml(repo.name)}')">Save Changes</button>
            </div>
        </div>`;
}

function saveRepoEdits(name) {
    const updates = {
        primary_dir: document.getElementById('repo-edit-dir').value,
        worktree_dir: document.getElementById('repo-edit-wt').value,
        git_strategy: document.getElementById('repo-edit-strategy').value,
        branch_template: document.getElementById('repo-edit-branch').value,
    };
    const keyParam = new URLSearchParams(location.search).get('key');
    const url = '/api/repos/' + encodeURIComponent(name) + (keyParam ? '?key=' + encodeURIComponent(keyParam) : '');
    fetch(url, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(updates),
    })
    .then(r => {
        if (!r.ok) return r.text().then(t => { throw new Error(t); });
        return r.json();
    })
    .then(() => {
        fetchRepos();
        send('list_worktrees', {});
    })
    .catch(err => alert('Failed to save: ' + err.message));
}

function removeRepo(name) {
    if (!confirm('Remove repository "' + name + '"?')) return;
    const keyParam = new URLSearchParams(location.search).get('key');
    const url = '/api/repos/' + encodeURIComponent(name) + (keyParam ? '?key=' + encodeURIComponent(keyParam) : '');
    fetch(url, { method: 'DELETE' })
    .then(r => {
        if (!r.ok) return r.text().then(t => { throw new Error(t); });
        state.selectedRepo = null;
        document.getElementById('repo-detail-view').classList.add('hidden');
        document.getElementById('empty-state').classList.remove('hidden');
        fetchRepos();
        send('list_worktrees', {});
    })
    .catch(err => alert('Failed to remove: ' + err.message));
}

function showAddRepoDialog() {
    var name = prompt('Repository name:');
    if (!name) return;
    var dir = prompt('Path to repository:', '/home/' + (state.ownerNickname || 'user') + '/repos/' + name);
    if (!dir) return;
    const keyParam = new URLSearchParams(location.search).get('key');
    const url = '/api/repos' + (keyParam ? '?key=' + encodeURIComponent(keyParam) : '');
    fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: name, primary_dir: dir }),
    })
    .then(r => {
        if (!r.ok) return r.text().then(t => { throw new Error(t); });
        return r.json();
    })
    .then(() => {
        fetchRepos();
        send('list_worktrees', {});
    })
    .catch(err => alert('Failed to add repo: ' + err.message));
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

    var html = '';
    if (role === 'assistant') {
        html = '<div class="message-sender assistant">Copilot</div>';
        html += renderMarkdown(content);
    } else if (role === 'user' && content && content.indexOf(': ') > 0 && content.indexOf(': ') < 20) {
        // Prefixed message from a shared user (e.g. "phil: tell me a joke")
        var parts = content.split(': ');
        var sender = parts.shift();
        var body = parts.join(': ');
        html = '<div class="message-sender guest">' + esc(sender) + '</div>';
        html += renderMarkdown(body);
    } else if (role === 'user') {
        html = '<div class="message-sender owner">' + esc(state.ownerNickname) + '</div>';
        html += renderMarkdown(content);
    } else {
        html = renderMarkdown(content);
    }
    div.innerHTML = html;
    container.appendChild(div);
    scrollToBottom();
}

function updateStreamingMessage(content) {
    const el = document.getElementById('streaming-message');
    if (el) {
        // Preserve sender label, only update the content portion.
        const senderEl = el.querySelector('.message-sender');
        const senderHtml = senderEl ? senderEl.outerHTML : '';
        el.innerHTML = senderHtml + renderMarkdown(content);
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

    // Find or create the current tool group at the bottom of the chat.
    var group = container.lastElementChild;
    if (!group || !group.classList.contains('tool-group')) {
        group = document.createElement('div');
        group.className = 'tool-group expanded';
        group.innerHTML = '<div class="tool-group-header has-running" onclick="this.parentElement.classList.toggle(\'expanded\')">' +
            '<span class="tool-group-icon">▶</span> <span class="tool-group-count">⚙️ 1 tool call</span></div>' +
            '<div class="tool-group-body"></div>';
        container.appendChild(group);
    }

    const body = group.querySelector('.tool-group-body');
    const div = document.createElement('div');
    div.className = `tool-indicator ${status}`;
    div.id = `tool-${callId}`;
    const icon = status === 'running' ? '⏳' : (status === 'completed' ? '✅' : '❌');
    div.innerHTML = `<span>${icon}</span> <span class="tool-name">${esc(toolName)}</span>`;
    body.appendChild(div);

    // Update the count in the group header.
    const count = body.querySelectorAll('.tool-indicator').length;
    group.querySelector('.tool-group-count').textContent = `⚙️ ${count} tool call${count !== 1 ? 's' : ''}`;

    scrollToBottom();
}

function updateToolIndicator(callId, status) {
    const el = document.getElementById(`tool-${callId}`);
    if (!el) return;
    el.className = `tool-indicator ${status}`;
    const icon = status === 'completed' ? '✅' : '❌';
    const nameEl = el.querySelector('.tool-name');
    const name = nameEl ? nameEl.textContent : '';
    el.innerHTML = `<span>${icon}</span> <span class="tool-name">${esc(name)}</span>`;

    // Update group header status and auto-collapse when all done.
    const group = el.closest('.tool-group');
    if (group) {
        const body = group.querySelector('.tool-group-body');
        const header = group.querySelector('.tool-group-header');
        if (body && header) {
            const running = body.querySelectorAll('.tool-indicator.running').length;
            const failed = body.querySelectorAll('.tool-indicator.failed').length;
            header.classList.toggle('has-running', running > 0);
            header.classList.toggle('has-failed', failed > 0);
            if (running === 0) {
                group.classList.remove('expanded');
            }
        }
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
    if (state.userScrolledUp) {
        // User is reading history — don't hijack their scroll. Show the
        // "new messages" pill instead.
        showNewMessagesPill(true);
        return;
    }
    const container = document.getElementById('chat-messages');
    requestAnimationFrame(() => {
        container.scrollTop = container.scrollHeight;
    });
}

function isNearBottom(container) {
    // Consider "near bottom" if within 80px of the end.
    return container.scrollHeight - container.scrollTop - container.clientHeight < 80;
}

function showNewMessagesPill(show) {
    const pill = document.getElementById('new-messages-pill');
    if (pill) pill.classList.toggle('hidden', !show);
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
    state.userScrolledUp = false;
    state.pendingPrompt = null;
    showNewMessagesPill(false);
    renderSessionList();
    renderPRs();
    renderRepos();
    document.getElementById('empty-state').classList.add('hidden');
    document.getElementById('chat-view').classList.remove('hidden');
    document.getElementById('pr-detail-view').classList.add('hidden');
    document.getElementById('repo-detail-view').classList.add('hidden');
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
    // Clear error state so the session can recover on retry.
    const s = state.sessions.find(s => s.name === state.activeSession);
    if (s && s.state === 'error') {
        updateSessionState(state.activeSession, 'processing');
        showActivity(true);
        updateActivity('💭 Thinking...');
    }
    send('send_message', { session_name: state.activeSession, prompt });

    // Show an optimistic pending bubble so the user gets immediate feedback.
    state.pendingPrompt = prompt;
    const isQueued = s && s.is_processing;
    appendPendingMessage(prompt, isQueued);

    input.value = '';
    input.style.height = 'auto';
    document.getElementById('send-btn').disabled = true;
}

function appendPendingMessage(content, queued) {
    const container = document.getElementById('chat-messages');
    const div = document.createElement('div');
    div.className = 'message user pending';
    div.id = 'pending-message';
    const label = queued ? 'Queued' : 'Sending';
    div.innerHTML = '<div class="message-sender owner">' + esc(state.ownerNickname) + '</div>' +
        renderMarkdown(content) +
        '<div class="pending-badge">' + label + '…</div>';
    container.appendChild(div);
    // Pending messages should always scroll down — the user just typed this.
    state.userScrolledUp = false;
    showNewMessagesPill(false);
    const msgs = document.getElementById('chat-messages');
    requestAnimationFrame(() => { msgs.scrollTop = msgs.scrollHeight; });
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
        var origin = (state.tunnelRunning && state.tunnelKeyedURL) ? state.tunnelKeyedURL.split('?')[0] : location.origin;
        var fullUrl = origin + data.url;
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

    // Scroll tracking — detect when user scrolls away from bottom.
    const chatMessages = document.getElementById('chat-messages');
    chatMessages.addEventListener('scroll', () => {
        const wasScrolledUp = state.userScrolledUp;
        state.userScrolledUp = !isNearBottom(chatMessages);
        // If user scrolled back to bottom, dismiss the pill.
        if (wasScrolledUp && !state.userScrolledUp) {
            showNewMessagesPill(false);
        }
    });

    // "New messages" pill — click to jump to bottom.
    document.getElementById('new-messages-pill').addEventListener('click', () => {
        state.userScrolledUp = false;
        showNewMessagesPill(false);
        chatMessages.scrollTop = chatMessages.scrollHeight;
    });

    // New session button
    document.getElementById('new-session-btn').addEventListener('click', showNewSessionDialog);

    // Repo management
    document.getElementById('add-repo-btn').addEventListener('click', showAddRepoDialog);

    // PR management
    document.getElementById('add-pr-btn').addEventListener('click', addPRFromDashboard);
    document.getElementById('pr-remove-btn').addEventListener('click', () => {
        if (state.selectedPR) removePR(state.selectedPR);
    });
    document.getElementById('repo-remove-btn').addEventListener('click', () => {
        if (state.selectedRepo) removeRepo(state.selectedRepo);
    });

    // Workdir search picker
    const workdirSearch = document.getElementById('session-workdir-search');
    workdirSearch.addEventListener('input', () => filterWorktrees(workdirSearch.value));
    workdirSearch.addEventListener('focus', () => filterWorktrees(workdirSearch.value));
    // Allow typing a custom path — set hidden input on blur if no dropdown selection was made.
    workdirSearch.addEventListener('blur', () => {
        // Delay to allow dropdown click to fire first.
        setTimeout(() => {
            const dropdown = document.getElementById('workdir-dropdown');
            dropdown.classList.add('hidden');
            const hidden = document.getElementById('session-workdir');
            // If user typed a raw path that doesn't match a selection, use it directly.
            if (workdirSearch.value && !hidden.value) {
                hidden.value = workdirSearch.value;
            }
        }, 200);
    });

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
