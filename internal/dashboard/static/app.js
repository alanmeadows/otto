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
        case 'watch_history': handleWatchHistory(msg.payload); break;
        case 'watch_event': handleWatchEvent(msg.payload); break;
        // Subagent lifecycle.
        case 'subagent_started': handleSubagentStarted(msg.payload); break;
        case 'subagent_completed': handleSubagentCompleted(msg.payload); break;
        case 'subagent_failed': handleSubagentFailed(msg.payload); break;
        case 'subagent_selected': handleSubagentSelected(msg.payload); break;
        case 'subagent_deselected': handleSubagentDeselected(msg.payload); break;
        // Tool progress.
        case 'tool_progress': handleToolProgress(msg.payload); break;
        case 'tool_partial_result': handleToolPartialResult(msg.payload); break;
        // Session lifecycle.
        case 'title_changed': handleTitleChanged(msg.payload); break;
        case 'compaction_start': handleCompactionStart(msg.payload); break;
        case 'compaction_complete': handleCompactionComplete(msg.payload); break;
        case 'plan_changed': handlePlanChanged(msg.payload); break;
        case 'task_complete': handleTaskComplete(msg.payload); break;
        case 'context_changed': break; // handled via sessions_list refresh
        case 'model_change': handleModelChange(msg.payload); break;
        case 'mode_changed': handleModeChanged(msg.payload); break;
        case 'session_warning': handleSessionWarning(msg.payload); break;
        case 'session_info': handleSessionInfoMsg(msg.payload); break;
        // User input / elicitation.
        case 'user_input_requested': handleUserInputRequested(msg.payload); break;
        case 'user_input_completed': handleUserInputCompleted(msg.payload); break;
        case 'elicitation_requested': handleElicitationRequested(msg.payload); break;
        case 'elicitation_completed': handleElicitationCompleted(msg.payload); break;
        // Permissions.
        case 'permission_requested': handlePermissionRequested(msg.payload); break;
        case 'permission_completed': handlePermissionCompleted(msg.payload); break;
        // Hooks & skills.
        case 'hook_start': handleHookStart(msg.payload); break;
        case 'hook_end': handleHookEnd(msg.payload); break;
        case 'skill_invoked': handleSkillInvoked(msg.payload); break;
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
    // If a resume was triggered by sending a message, send the queued prompt.
    if (state._pendingResumeName) {
        const found = state.sessions.find(s => s.name === state._pendingResumeName);
        if (found) {
            const name = state._pendingResumeName;
            const prompt = state._pendingResumePrompt;
            state._pendingResumeName = null;
            state._pendingResumePrompt = null;
            selectSession(name);
            send('get_persisted_sessions', {});
            if (prompt) {
                setTimeout(() => {
                    send('send_message', { session_name: name, prompt: prompt });
                }, 500);
            }
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
        card.onclick = () => handlePersistedSessionClick(p.session_id);
        const shortId = p.session_id.substring(0, 8);
        const summary = p.summary || 'No summary';
        // Use last_modified (filesystem-based) for recency, not updated_at (DB).
        const agoSource = p.last_modified || p.updated_at;
        const ago = agoSource ? timeAgo(agoSource) : '';
        const agoClass = p.is_active ? 'recently-active' : (isRecentlyActive(agoSource) ? 'recently-active' : '');
        const activeDot = p.is_active ? '<span class="persisted-card-active" title="Session is actively running"></span>' : '';
        card.innerHTML = `
            <div class="persisted-card-header">
                <span class="persisted-card-id">${activeDot}${esc(shortId)}…</span>
                ${ago ? `<span class="persisted-card-ago ${agoClass}">${esc(ago)}</span>` : ''}
            </div>
            <div class="persisted-card-summary" title="${esc(p.summary || '')}">${esc(summary.length > 60 ? summary.substring(0, 57) + '...' : summary)}</div>
        `;
        container.appendChild(card);
    });
}

function handlePersistedSessionClick(sessionId) {
    const session = state.persistedSessions.find(p => p.session_id === sessionId);
    if (!session) return;

    const title = session.summary || sessionId.substring(0, 8);
    if (session.is_active) {
        // Active session: watch mode with live tailing.
        watchSession(sessionId, title);
    } else {
        // Idle session: view history (read-only). User can resume by
        // sending a message, which triggers resumeAndSend().
        viewSavedSession(sessionId, title);
    }
}

function watchSession(sessionId, title) {
    state.activeSession = null;
    state.selectedPR = null;
    state.selectedRepo = null;
    state._watchingSession = sessionId;
    renderSessionList();
    renderPRs();
    renderRepos();
    document.getElementById('empty-state').classList.add('hidden');
    document.getElementById('pr-detail-view').classList.add('hidden');
    document.getElementById('repo-detail-view').classList.add('hidden');
    document.getElementById('chat-view').classList.remove('hidden');

    // Update header for watch mode.
    document.getElementById('chat-session-name').textContent = '👁 ' + (title.length > 30 ? title.substring(0, 27) + '...' : title);
    document.getElementById('chat-session-model').textContent = 'watching';
    const statusEl = document.getElementById('chat-session-status');
    statusEl.className = 'status-badge processing';
    statusEl.textContent = 'live';

    // Show fork button, hide share button, disable input.
    document.getElementById('share-btn').classList.add('hidden');
    document.getElementById('fork-btn').classList.remove('hidden');
    document.getElementById('chat-input').disabled = true;
    document.getElementById('chat-input').placeholder = 'Watching session (read-only). Fork to interact.';
    document.getElementById('send-btn').disabled = true;

    document.getElementById('chat-messages').innerHTML = '<div style="color:var(--text-muted);padding:20px;text-align:center">Loading session history...</div>';

    send('watch_session', { session_id: sessionId });

    if (window.innerWidth <= 768) {
        document.getElementById('sidebar').classList.remove('open');
    }
}

function handleWatchHistory(payload) {
    if (payload.session_name !== state._watchingSession && payload.session_name !== state._viewingSession) return;
    const container = document.getElementById('chat-messages');
    container.innerHTML = '';
    (payload.messages || []).filter(m => m.content && m.content.trim()).forEach(msg => {
        appendChatMessage(msg.role, msg.content, false);
    });
    state.renderedMessageCount = (payload.messages || []).length;
    scrollToBottom();
}

function handleWatchEvent(payload) {
    if (payload.session_name !== state._watchingSession) return;
    // Parse "role: content" format from watch events.
    const colonIdx = payload.content.indexOf(': ');
    if (colonIdx > 0 && colonIdx < 12) {
        const role = payload.content.substring(0, colonIdx);
        const content = payload.content.substring(colonIdx + 2);
        appendChatMessage(role, content, false);
        state.renderedMessageCount++;
    }
}

// --- Subagent lifecycle handlers ---

function handleSubagentStarted(payload) {
    if (payload.session_name !== state.activeSession) return;
    var label = payload.agent_display_name || payload.agent_name || 'sub-agent';
    if (payload.agent_description) {
        var desc = payload.agent_description;
        if (desc.length > 60) desc = desc.substring(0, 57) + '...';
        label += ': ' + desc;
    }
    appendSubagentIndicator(payload.tool_call_id, label, 'running');
    updateActivity('🤖 ' + (payload.agent_display_name || payload.agent_name || 'sub-agent') + '...');
}

function handleSubagentCompleted(payload) {
    if (payload.session_name !== state.activeSession) return;
    updateSubagentIndicator(payload.tool_call_id, 'completed');
}

function handleSubagentFailed(payload) {
    if (payload.session_name !== state.activeSession) return;
    updateSubagentIndicator(payload.tool_call_id, 'failed');
}

function handleSubagentSelected(payload) {
    if (payload.session_name !== state.activeSession) return;
    var name = payload.agent_display_name || payload.agent_name || 'custom agent';
    appendSessionBanner('info', '🤖 Agent: ' + name);
}

function handleSubagentDeselected(payload) {
    if (payload.session_name !== state.activeSession) return;
    appendSessionBanner('info', '🤖 Returned to default agent');
}

function appendSubagentIndicator(callId, label, status) {
    var container = document.getElementById('chat-messages');
    var group = container.lastElementChild;
    if (!group || !group.classList.contains('subagent-group')) {
        group = document.createElement('div');
        group.className = 'subagent-group expanded';
        group.innerHTML = '<div class="subagent-group-header has-running" onclick="this.parentElement.classList.toggle(\'expanded\')">' +
            '<span class="subagent-group-icon">▶</span> <span class="subagent-group-count">🤖 1 sub-agent</span></div>' +
            '<div class="subagent-group-body"></div>';
        container.appendChild(group);
    }
    var body = group.querySelector('.subagent-group-body');
    var div = document.createElement('div');
    div.className = 'subagent-indicator ' + status;
    div.id = 'subagent-' + callId;
    var icon = status === 'running' ? '⏳' : (status === 'completed' ? '✅' : '❌');
    div.innerHTML = '<span>' + icon + '</span> <span class="subagent-name">' + esc(label) + '</span>';
    body.appendChild(div);
    var count = body.querySelectorAll('.subagent-indicator').length;
    group.querySelector('.subagent-group-count').textContent = '🤖 ' + count + ' sub-agent' + (count !== 1 ? 's' : '');
    scrollToBottom();
}

function updateSubagentIndicator(callId, status) {
    var el = document.getElementById('subagent-' + callId);
    if (!el) return;
    el.className = 'subagent-indicator ' + status;
    var icon = status === 'completed' ? '✅' : '❌';
    var nameEl = el.querySelector('.subagent-name');
    var name = nameEl ? nameEl.textContent : '';
    el.innerHTML = '<span>' + icon + '</span> <span class="subagent-name">' + esc(name) + '</span>';
    var group = el.closest('.subagent-group');
    if (group) {
        var body = group.querySelector('.subagent-group-body');
        var header = group.querySelector('.subagent-group-header');
        if (body && header) {
            var running = body.querySelectorAll('.subagent-indicator.running').length;
            var failed = body.querySelectorAll('.subagent-indicator.failed').length;
            header.classList.toggle('has-running', running > 0);
            header.classList.toggle('has-failed', failed > 0);
            if (running === 0) group.classList.remove('expanded');
        }
    }
}

// --- Tool progress handlers ---

function handleToolProgress(payload) {
    if (payload.session_name !== state.activeSession) return;
    var el = document.getElementById('tool-' + payload.call_id);
    if (!el) return;
    var nameEl = el.querySelector('.tool-name');
    if (nameEl && payload.progress_message) {
        var base = nameEl.textContent.split(' — ')[0];
        nameEl.textContent = base + ' — ' + payload.progress_message;
    }
}

function handleToolPartialResult(payload) {
    if (payload.session_name !== state.activeSession) return;
    var el = document.getElementById('tool-' + payload.call_id);
    if (!el) return;
    var nameEl = el.querySelector('.tool-name');
    if (nameEl && payload.partial_output) {
        var base = nameEl.textContent.split(' — ')[0];
        var partial = payload.partial_output;
        if (partial.length > 80) partial = partial.substring(0, 77) + '...';
        nameEl.textContent = base + ' — ' + partial;
    }
}

// --- Session lifecycle handlers ---

function handleTitleChanged(payload) {
    if (payload.session_name !== state.activeSession) return;
    if (payload.title) {
        document.getElementById('chat-session-name').textContent = payload.title.length > 30 ? payload.title.substring(0, 27) + '...' : payload.title;
    }
}

function handleCompactionStart(payload) {
    if (payload.session_name !== state.activeSession) return;
    appendSessionBanner('info', '📦 Compacting context...');
}

function handleCompactionComplete(payload) {
    if (payload.session_name !== state.activeSession) return;
    if (payload.success) {
        appendSessionBanner('success', '📦 Context compacted');
    } else {
        appendSessionBanner('warning', '📦 Compaction failed');
    }
}

function handlePlanChanged(payload) {
    if (payload.session_name !== state.activeSession) return;
    var msg = '📋 Plan updated';
    if (payload.summary) {
        var s = payload.summary;
        if (s.length > 60) s = s.substring(0, 57) + '...';
        msg += ': ' + s;
    }
    appendSessionBanner('info', msg);
}

function handleTaskComplete(payload) {
    if (payload.session_name !== state.activeSession) return;
    var msg = '✅ Task complete';
    if (payload.summary) {
        var s = payload.summary;
        if (s.length > 60) s = s.substring(0, 57) + '...';
        msg += ': ' + s;
    }
    appendSessionBanner('success', msg);
}

function handleModelChange(payload) {
    if (payload.session_name !== state.activeSession) return;
    var msg = '🔄 Model → ' + (payload.new_model || 'unknown');
    appendSessionBanner('info', msg);
    document.getElementById('chat-session-model').textContent = payload.new_model || '';
}

function handleModeChanged(payload) {
    if (payload.session_name !== state.activeSession) return;
    appendSessionBanner('info', '🔀 Mode → ' + (payload.new_mode || 'unknown'));
}

function handleSessionWarning(payload) {
    if (payload.session_name !== state.activeSession) return;
    var msg = '⚠️ ' + (payload.message || 'Warning');
    appendSessionBanner('warning', msg);
}

function handleSessionInfoMsg(payload) {
    if (payload.session_name !== state.activeSession) return;
    var msg = 'ℹ️ ' + (payload.message || 'Info');
    appendSessionBanner('info', msg);
}

// --- User input / elicitation handlers ---

function handleUserInputRequested(payload) {
    if (payload.session_name !== state.activeSession) return;
    appendUserInputCard(payload.request_id, payload.question, payload.choices);
}

function handleUserInputCompleted(payload) {
    if (payload.session_name !== state.activeSession) return;
    resolveUserInputCard(payload.request_id);
}

function handleElicitationRequested(payload) {
    if (payload.session_name !== state.activeSession) return;
    appendUserInputCard(payload.request_id, payload.message || 'Form input requested', []);
}

function handleElicitationCompleted(payload) {
    if (payload.session_name !== state.activeSession) return;
    resolveUserInputCard(payload.request_id);
}

function appendUserInputCard(requestId, question, choices) {
    var container = document.getElementById('chat-messages');
    var card = document.createElement('div');
    card.className = 'user-input-card';
    card.id = 'uic-' + requestId;
    var html = '<div class="uic-question">❓ ' + esc(question || '') + '</div>';
    if (choices && choices.length > 0) {
        html += '<div class="uic-choices">';
        for (var i = 0; i < choices.length; i++) {
            html += '<span class="uic-choice">' + esc(choices[i]) + '</span>';
        }
        html += '</div>';
    }
    card.innerHTML = html;
    container.appendChild(card);
    scrollToBottom();
}

function resolveUserInputCard(requestId) {
    var el = document.getElementById('uic-' + requestId);
    if (el) el.classList.add('resolved');
}

// --- Permission handlers ---

function handlePermissionRequested(payload) {
    if (payload.session_name !== state.activeSession) return;
    var label = payload.tool_name || payload.permission_kind || 'permission';
    appendToolIndicator('perm-' + payload.request_id, '🔐 ' + label, 'running');
}

function handlePermissionCompleted(payload) {
    if (payload.session_name !== state.activeSession) return;
    updateToolIndicator('perm-' + payload.request_id, 'completed');
}

// --- Hook & skill handlers ---

function handleHookStart(payload) {
    if (payload.session_name !== state.activeSession) return;
    appendToolIndicator('hook-' + payload.hook_id, '🪝 ' + (payload.hook_type || 'hook'), 'running');
}

function handleHookEnd(payload) {
    if (payload.session_name !== state.activeSession) return;
    updateToolIndicator('hook-' + payload.hook_id, payload.success ? 'completed' : 'failed');
}

function handleSkillInvoked(payload) {
    if (payload.session_name !== state.activeSession) return;
    appendSessionBanner('info', '⚡ Skill: ' + (payload.skill_name || 'unknown'));
}

// --- Session banner helper ---

function appendSessionBanner(type, message) {
    var container = document.getElementById('chat-messages');
    var div = document.createElement('div');
    div.className = 'session-banner ' + type;
    div.textContent = message;
    container.appendChild(div);
    scrollToBottom();
}

function forkSession(sessionId) {
    send('fork_session', { session_id: sessionId, model: 'claude-opus-4.6' });
    state._watchingSession = null;
    document.getElementById('chat-input').disabled = false;
    document.getElementById('chat-input').placeholder = 'Send a message...';
    document.getElementById('share-btn').classList.remove('hidden');
    document.getElementById('fork-btn').classList.add('hidden');
}

function viewSavedSession(sessionId, title) {
    state.activeSession = null;
    state.selectedPR = null;
    state.selectedRepo = null;
    state._watchingSession = null;
    state._viewingSession = sessionId;
    state.renderedMessageCount = 0;
    renderSessionList();
    renderPRs();
    renderRepos();
    document.getElementById('empty-state').classList.add('hidden');
    document.getElementById('pr-detail-view').classList.add('hidden');
    document.getElementById('repo-detail-view').classList.add('hidden');
    document.getElementById('chat-view').classList.remove('hidden');

    document.getElementById('chat-session-name').textContent = title.length > 30 ? title.substring(0, 27) + '...' : title;
    document.getElementById('chat-session-model').textContent = 'saved';
    const statusEl = document.getElementById('chat-session-status');
    statusEl.className = 'status-badge idle';
    statusEl.textContent = 'idle';

    // Input is enabled — sending a message will resume the session.
    document.getElementById('chat-input').disabled = false;
    document.getElementById('chat-input').placeholder = 'Send a message to resume this session...';
    document.getElementById('share-btn').classList.add('hidden');
    document.getElementById('fork-btn').classList.remove('hidden');

    document.getElementById('chat-messages').innerHTML = '<div style="color:var(--text-muted);padding:20px;text-align:center">Loading session history...</div>';

    // Load history via watch_session (reuse same mechanism, just no tailing for idle).
    send('watch_session', { session_id: sessionId });

    if (window.innerWidth <= 768) {
        document.getElementById('sidebar').classList.remove('open');
    }
}

function resumePersistedSession(sessionId) {
    const session = state.persistedSessions.find(p => p.session_id === sessionId);
    const displayName = session?.summary || sessionId.substring(0, 8);
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
    const qrEl = document.getElementById('tunnel-qr');

    if (state.tunnelRunning && state.tunnelURL) {
        activeEl.classList.remove('hidden');
        inactiveEl.classList.add('hidden');
        badge.classList.remove('hidden');
        badge.textContent = '🔗 Tunnel';
        badge.title = 'Click to copy tunnel URL';
        var url = state.tunnelKeyedURL || state.tunnelURL;
        if (qrEl) {
            qrEl.innerHTML = '';
            try {
                var canvas = generateQRCode(url, 160);
                qrEl.appendChild(canvas);
            } catch (e) { /* QR generation failed silently */ }
        }
    } else {
        activeEl.classList.add('hidden');
        inactiveEl.classList.remove('hidden');
        badge.classList.add('hidden');
        if (qrEl) qrEl.innerHTML = '';
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
    // Parse "### Attempt N", "### Infra Retry", and "### Comment by" entries
    const regex = /^### (Attempt \d+|Infra Retry|Comment by .+?)\s*[-–—]\s*(.+)$/gm;
    const entries = [];
    let match;
    while ((match = regex.exec(body)) !== null) {
        const title = match[1];
        const timestamp = match[2].trim();
        const startIdx = match.index + match[0].length;
        const nextHeading = body.indexOf('\n###', startIdx);
        const block = body.substring(startIdx, nextHeading > -1 ? nextHeading : undefined).trim();
        entries.push({ title, timestamp, block });
    }
    if (entries.length === 0) return '';

    // Reverse: latest activity first.
    entries.reverse();

    let html = '<h4 class="timeline-heading">Activity</h4>';
    html += '<div class="timeline">';
    entries.forEach((e, i) => {
        const isFirst = i === 0;
        const timeStr = formatTimelineDate(e.timestamp);
        const details = parseTimelineDetails(e.block);
        html += `<div class="timeline-entry${isFirst ? ' latest' : ''}">
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
    // Remove ### Attempt / ### Infra Retry / ### Comment by blocks.
    return body.replace(/^### (?:Attempt \d+|Infra Retry|Comment by .+?)[^\n]*\n(?:(?!^###)[^\n]*\n?)*/gm, '').trim();
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
    state.selectedRepo = null;
    state._watchingSession = null;
    state.renderedMessageCount = 0;
    state.userScrolledUp = false;
    state.pendingPrompt = null;
    showNewMessagesPill(false);
    // Restore interactive mode (in case we were watching).
    document.getElementById('chat-input').disabled = false;
    document.getElementById('chat-input').placeholder = 'Send a message...';
    document.getElementById('share-btn').classList.remove('hidden');
    document.getElementById('fork-btn').classList.add('hidden');
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
    if (!prompt) return;

    // If viewing a saved session, resume it first then send.
    if (state._viewingSession && !state.activeSession) {
        const sessionId = state._viewingSession;
        state._viewingSession = null;
        state._pendingResume = null;
        // Resume via SDK, then send the message.
        const displayName = document.getElementById('chat-session-name').textContent;
        const name = displayName.length > 40 ? displayName.substring(0, 37) + '...' : displayName;
        state._pendingResumePrompt = prompt;
        state._pendingResumeName = name;
        send('resume_session', { session_id: sessionId, display_name: name });
        appendPendingMessage(prompt, true);
        input.value = '';
        input.style.height = 'auto';
        document.getElementById('send-btn').disabled = true;
        document.getElementById('chat-input').placeholder = 'Send a message...';
        document.getElementById('fork-btn').classList.add('hidden');
        document.getElementById('share-btn').classList.remove('hidden');
        return;
    }

    if (!state.activeSession) return;
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

    // Fork button (watch mode).
    document.getElementById('fork-btn').addEventListener('click', () => {
        if (state._watchingSession) forkSession(state._watchingSession);
    });

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

// ---------------------------------------------------------------------------
// Minimal QR Code generator (no external dependencies)
// Generates a QR code on a canvas element. Supports alphanumeric/byte mode.
// ---------------------------------------------------------------------------

function generateQRCode(text, size) {
    var modules = qrEncodeText(text);
    var n = modules.length;
    var canvas = document.createElement('canvas');
    canvas.width = size;
    canvas.height = size;
    var ctx = canvas.getContext('2d');
    var cellSize = size / (n + 8); // quiet zone of 4 on each side
    ctx.fillStyle = '#ffffff';
    ctx.fillRect(0, 0, size, size);
    ctx.fillStyle = '#000000';
    for (var y = 0; y < n; y++) {
        for (var x = 0; x < n; x++) {
            if (modules[y][x]) {
                ctx.fillRect((x + 4) * cellSize, (y + 4) * cellSize, cellSize + 0.5, cellSize + 0.5);
            }
        }
    }
    return canvas;
}

// QR encoding using byte mode, error correction level L.
function qrEncodeText(text) {
    var data = [];
    for (var i = 0; i < text.length; i++) {
        data.push(text.charCodeAt(i));
    }
    // Determine minimum version (1-40) for byte mode, EC level L.
    var version = 1;
    var capacities = [0,17,32,53,78,106,134,154,192,230,271,321,367,425,458,520,586,644,718,792,858,929,1003,1091,1171,1273,1367,1465,1528,1628,1732,1840,1952,2068,2188,2303,2431,2563,2699,2809,2953];
    while (version < 40 && data.length > capacities[version]) version++;
    var size = version * 4 + 17;
    var modules = [];
    var isFunction = [];
    for (var y = 0; y < size; y++) {
        modules.push(new Array(size).fill(false));
        isFunction.push(new Array(size).fill(false));
    }

    // Place finder patterns.
    function placeFinderPattern(row, col) {
        for (var dy = -4; dy <= 4; dy++) {
            for (var dx = -4; dx <= 4; dx++) {
                var yy = row + dy, xx = col + dx;
                if (yy < 0 || yy >= size || xx < 0 || xx >= size) continue;
                var dist = Math.max(Math.abs(dy), Math.abs(dx));
                modules[yy][xx] = dist !== 4 && (dist !== 2 || (dy === 0 && dx === 0) || dist === 0 || dist === 3);
                isFunction[yy][xx] = true;
            }
        }
    }
    placeFinderPattern(3, 3);
    placeFinderPattern(3, size - 4);
    placeFinderPattern(size - 4, 3);

    // Timing patterns.
    for (var i = 8; i < size - 8; i++) {
        modules[6][i] = i % 2 === 0;
        isFunction[6][i] = true;
        modules[i][6] = i % 2 === 0;
        isFunction[i][6] = true;
    }

    // Alignment patterns.
    var alignPos = qrAlignmentPositions(version);
    for (var ai = 0; ai < alignPos.length; ai++) {
        for (var aj = 0; aj < alignPos.length; aj++) {
            var ay = alignPos[ai], ax = alignPos[aj];
            if ((ay < 9 && ax < 9) || (ay < 9 && ax > size - 9) || (ay > size - 9 && ax < 9)) continue;
            for (var dy = -2; dy <= 2; dy++) {
                for (var dx = -2; dx <= 2; dx++) {
                    modules[ay + dy][ax + dx] = Math.abs(dy) === 2 || Math.abs(dx) === 2 || (dy === 0 && dx === 0);
                    isFunction[ay + dy][ax + dx] = true;
                }
            }
        }
    }

    // Reserve format/version areas.
    for (var i = 0; i < 9; i++) {
        isFunction[8][i] = true; isFunction[i][8] = true;
        isFunction[8][size - 1 - i] = true; isFunction[size - 1 - i][8] = true;
    }
    modules[size - 8][8] = true; isFunction[size - 8][8] = true; // dark module
    if (version >= 7) {
        for (var i = 0; i < 6; i++) {
            for (var j = 0; j < 3; j++) {
                isFunction[i][size - 11 + j] = true;
                isFunction[size - 11 + j][i] = true;
            }
        }
    }

    // Encode data.
    var ecInfo = qrECInfo(version);
    var dataCodewords = ecInfo.dataCodewords;
    var bits = [];
    // Mode indicator (byte = 0100).
    bits.push(0,1,0,0);
    // Character count.
    var ccBits = version <= 9 ? 8 : 16;
    for (var i = ccBits - 1; i >= 0; i--) bits.push((data.length >> i) & 1);
    // Data bytes.
    for (var i = 0; i < data.length; i++) {
        for (var j = 7; j >= 0; j--) bits.push((data[i] >> j) & 1);
    }
    // Terminator.
    for (var i = 0; i < 4 && bits.length < dataCodewords * 8; i++) bits.push(0);
    while (bits.length % 8 !== 0) bits.push(0);
    // Pad codewords.
    var pads = [0xEC, 0x11];
    var pi = 0;
    while (bits.length < dataCodewords * 8) {
        for (var j = 7; j >= 0; j--) bits.push((pads[pi] >> j) & 1);
        pi ^= 1;
    }

    // Convert to codeword bytes.
    var codewords = [];
    for (var i = 0; i < bits.length; i += 8) {
        var byte = 0;
        for (var j = 0; j < 8; j++) byte = (byte << 1) | bits[i + j];
        codewords.push(byte);
    }

    // RS error correction.
    var ecCodewords = qrRSEncode(codewords, ecInfo);

    // Interleave and combine.
    var allCodewords = qrInterleave(codewords, ecCodewords, ecInfo);

    // Place data bits.
    var bitIdx = 0;
    for (var right = size - 1; right >= 1; right -= 2) {
        if (right === 6) right = 5;
        for (var vert = 0; vert < size; vert++) {
            for (var j = 0; j < 2; j++) {
                var x = right - j;
                var upward = ((right + 1) & 2) === 0;
                var y = upward ? size - 1 - vert : vert;
                if (!isFunction[y][x] && bitIdx < allCodewords.length * 8) {
                    modules[y][x] = ((allCodewords[bitIdx >> 3] >> (7 - (bitIdx & 7))) & 1) === 1;
                    bitIdx++;
                }
            }
        }
    }

    // Apply best mask and format info.
    var bestMask = 0, bestPenalty = Infinity;
    for (var mask = 0; mask < 8; mask++) {
        var trial = modules.map(function(r) { return r.slice(); });
        qrApplyMask(trial, isFunction, mask, size);
        qrPlaceFormatBits(trial, mask, size, version);
        var penalty = qrPenalty(trial, size);
        if (penalty < bestPenalty) { bestPenalty = penalty; bestMask = mask; }
    }
    qrApplyMask(modules, isFunction, bestMask, size);
    qrPlaceFormatBits(modules, bestMask, size, version);

    return modules;
}

function qrAlignmentPositions(version) {
    if (version === 1) return [];
    var n = Math.floor(version / 7) + 2;
    var first = 6;
    var last = version * 4 + 10;
    var positions = [first];
    if (n > 2) {
        var step = Math.ceil((last - first) / (n - 1));
        if (step % 2 !== 0) step++;
        for (var i = n - 2; i >= 1; i--) positions.push(last - i * step);
    }
    positions.push(last);
    return positions;
}

function qrECInfo(version) {
    // EC level L lookup: [totalCodewords, ecCodewordsPerBlock, numBlocks1, dataPerBlock1, numBlocks2, dataPerBlock2]
    var table = [
        null,
        [26,7,1,19,0,0],[44,10,1,34,0,0],[70,15,1,55,0,0],[100,20,1,80,0,0],[134,26,1,108,0,0],
        [172,18,2,68,0,0],[196,20,2,78,0,0],[242,24,2,97,0,0],[292,30,2,116,0,0],[346,18,2,68,2,69],
        [404,20,4,81,0,0],[466,24,2,92,2,93],[532,26,4,107,0,0],[581,30,3,115,1,116],[655,22,5,87,1,88],
        [733,24,5,98,1,99],[815,28,1,107,5,108],[901,30,5,120,1,121],[991,28,3,113,4,114],[1085,28,4,107,5,108],
        [1156,28,4,116,4,117],[1258,28,2,111,7,112],[1364,30,4,121,5,122],[1474,30,6,117,4,118],[1588,26,8,106,4,107],
        [1706,28,10,114,2,115],[1828,30,8,122,4,123],[1921,30,3,117,10,118],[2051,30,7,116,7,117],[2185,30,5,115,10,116],
        [2323,30,13,115,3,116],[2465,30,17,115,0,0],[2611,30,17,115,1,116],[2761,30,13,115,6,116],[2876,30,12,121,7,122],
        [3034,30,6,121,14,122],[3196,30,17,122,4,123],[3362,30,4,122,18,123],[3532,30,20,117,4,118]
    ];
    var t = table[version];
    var totalCodewords = t[0];
    var ecPerBlock = t[1];
    var numBlocks1 = t[2], dataPerBlock1 = t[3];
    var numBlocks2 = t[4], dataPerBlock2 = t[5];
    var totalBlocks = numBlocks1 + numBlocks2;
    var dataCodewords = numBlocks1 * dataPerBlock1 + numBlocks2 * dataPerBlock2;
    return { totalCodewords: totalCodewords, ecPerBlock: ecPerBlock, numBlocks1: numBlocks1, dataPerBlock1: dataPerBlock1, numBlocks2: numBlocks2, dataPerBlock2: dataPerBlock2, totalBlocks: totalBlocks, dataCodewords: dataCodewords };
}

function qrRSEncode(dataWords, ecInfo) {
    var ecPerBlock = ecInfo.ecPerBlock;
    var gen = qrRSGenerator(ecPerBlock);
    var blocks1 = [], blocks2 = [];
    var offset = 0;
    for (var i = 0; i < ecInfo.numBlocks1; i++) {
        blocks1.push(dataWords.slice(offset, offset + ecInfo.dataPerBlock1));
        offset += ecInfo.dataPerBlock1;
    }
    for (var i = 0; i < ecInfo.numBlocks2; i++) {
        blocks2.push(dataWords.slice(offset, offset + ecInfo.dataPerBlock2));
        offset += ecInfo.dataPerBlock2;
    }
    var ecBlocks = [];
    var allBlocks = blocks1.concat(blocks2);
    for (var i = 0; i < allBlocks.length; i++) {
        ecBlocks.push(qrRSDivide(allBlocks[i], gen));
    }
    return ecBlocks;
}

function qrInterleave(dataWords, ecBlocks, ecInfo) {
    var blocks = [];
    var offset = 0;
    for (var i = 0; i < ecInfo.numBlocks1; i++) {
        blocks.push(dataWords.slice(offset, offset + ecInfo.dataPerBlock1));
        offset += ecInfo.dataPerBlock1;
    }
    for (var i = 0; i < ecInfo.numBlocks2; i++) {
        blocks.push(dataWords.slice(offset, offset + ecInfo.dataPerBlock2));
        offset += ecInfo.dataPerBlock2;
    }
    var result = [];
    var maxData = Math.max(ecInfo.dataPerBlock1, ecInfo.dataPerBlock2);
    for (var i = 0; i < maxData; i++) {
        for (var j = 0; j < blocks.length; j++) {
            if (i < blocks[j].length) result.push(blocks[j][i]);
        }
    }
    for (var i = 0; i < ecInfo.ecPerBlock; i++) {
        for (var j = 0; j < ecBlocks.length; j++) {
            result.push(ecBlocks[j][i]);
        }
    }
    return result;
}

function qrRSGenerator(degree) {
    var result = [1];
    for (var i = 0; i < degree; i++) {
        var newResult = new Array(result.length + 1).fill(0);
        var factor = qrGFExp(i);
        for (var j = 0; j < result.length; j++) {
            newResult[j] ^= result[j];
            newResult[j + 1] ^= qrGFMultiply(result[j], factor);
        }
        result = newResult;
    }
    return result;
}

function qrRSDivide(data, generator) {
    var result = new Array(generator.length - 1).fill(0);
    for (var i = 0; i < data.length; i++) {
        var factor = data[i] ^ result[0];
        result.shift();
        result.push(0);
        for (var j = 0; j < result.length; j++) {
            result[j] ^= qrGFMultiply(generator[j + 1], factor);
        }
    }
    return result;
}

var _qrExpTable = null, _qrLogTable = null;
function qrInitGF() {
    if (_qrExpTable) return;
    _qrExpTable = new Array(256);
    _qrLogTable = new Array(256);
    var x = 1;
    for (var i = 0; i < 256; i++) {
        _qrExpTable[i] = x;
        _qrLogTable[x] = i;
        x <<= 1;
        if (x >= 256) x ^= 0x11D;
    }
}

function qrGFExp(n) { qrInitGF(); return _qrExpTable[n % 255]; }
function qrGFMultiply(a, b) {
    if (a === 0 || b === 0) return 0;
    qrInitGF();
    return _qrExpTable[(_qrLogTable[a] + _qrLogTable[b]) % 255];
}

function qrApplyMask(modules, isFunction, mask, size) {
    for (var y = 0; y < size; y++) {
        for (var x = 0; x < size; x++) {
            if (isFunction[y][x]) continue;
            var invert = false;
            switch (mask) {
                case 0: invert = (y + x) % 2 === 0; break;
                case 1: invert = y % 2 === 0; break;
                case 2: invert = x % 3 === 0; break;
                case 3: invert = (y + x) % 3 === 0; break;
                case 4: invert = (Math.floor(y / 2) + Math.floor(x / 3)) % 2 === 0; break;
                case 5: invert = (y * x) % 2 + (y * x) % 3 === 0; break;
                case 6: invert = ((y * x) % 2 + (y * x) % 3) % 2 === 0; break;
                case 7: invert = ((y + x) % 2 + (y * x) % 3) % 2 === 0; break;
            }
            if (invert) modules[y][x] = !modules[y][x];
        }
    }
}

function qrPlaceFormatBits(modules, mask, size, version) {
    var formatBits = qrFormatBits(mask);
    for (var i = 0; i < 15; i++) {
        var bit = ((formatBits >> (14 - i)) & 1) === 1;
        // Around top-left finder.
        if (i < 6) modules[8][i] = bit;
        else if (i < 8) modules[8][i + 1] = bit;
        else if (i < 9) modules[8 - i + 7][8] = bit;
        else modules[14 - i][8] = bit;
        // Around bottom-left and top-right finders.
        if (i < 8) modules[size - 1 - i][8] = bit;
        else modules[8][size - 15 + i] = bit;
    }
    if (version >= 7) {
        var versionBits = qrVersionBits(version);
        for (var i = 0; i < 18; i++) {
            var bit = ((versionBits >> i) & 1) === 1;
            modules[Math.floor(i / 3)][size - 11 + (i % 3)] = bit;
            modules[size - 11 + (i % 3)][Math.floor(i / 3)] = bit;
        }
    }
}

function qrFormatBits(mask) {
    // EC level L = 01, mask pattern 0-7.
    var data = (1 << 3) | mask; // 01 + mask
    var rem = data;
    for (var i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >> 9) * 0x537);
    var bits = ((data << 10) | rem) ^ 0x5412;
    return bits;
}

function qrVersionBits(version) {
    var rem = version;
    for (var i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >> 11) * 0x1F25);
    return (version << 12) | rem;
}

function qrPenalty(modules, size) {
    var penalty = 0;
    // Rule 1: consecutive same-color runs.
    for (var y = 0; y < size; y++) {
        var run = 1;
        for (var x = 1; x < size; x++) {
            if (modules[y][x] === modules[y][x - 1]) { run++; }
            else { if (run >= 5) penalty += run - 2; run = 1; }
        }
        if (run >= 5) penalty += run - 2;
    }
    for (var x = 0; x < size; x++) {
        var run = 1;
        for (var y = 1; y < size; y++) {
            if (modules[y][x] === modules[y - 1][x]) { run++; }
            else { if (run >= 5) penalty += run - 2; run = 1; }
        }
        if (run >= 5) penalty += run - 2;
    }
    // Rule 2: 2x2 blocks.
    for (var y = 0; y < size - 1; y++) {
        for (var x = 0; x < size - 1; x++) {
            var c = modules[y][x];
            if (c === modules[y][x + 1] && c === modules[y + 1][x] && c === modules[y + 1][x + 1]) penalty += 3;
        }
    }
    return penalty;
}
