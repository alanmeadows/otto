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

    // Full refresh: clear and re-render canonical history.
    const wasAtBottom = !state.userScrolledUp;
    container.innerHTML = '';
    messages.forEach(msg => appendChatMessage(msg.role, msg.content, false));
    state.renderedMessageCount = messages.length;

    // Re-add pending messages that haven't appeared in history yet.
    if (state.pendingPrompt) {
        const alreadyInHistory = messages.some(m => m.role === 'user' && m.content === state.pendingPrompt);
        if (alreadyInHistory) {
            state.pendingPrompt = null;
        } else {
            const s = state.sessions.find(s => s.name === state.activeSession);
            const isQueued = s && s.is_processing;
            appendPendingMessage(state.pendingPrompt, isQueued);
        }
    }

    if (wasAtBottom) {
        state.userScrolledUp = false;
        const el = document.getElementById('chat-messages');
        requestAnimationFrame(() => { el.scrollTop = el.scrollHeight; });
    }
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
    }

    if (payload.session_name === state.activeSession) {
        // Always refresh from history on turn end to prevent duplicates
        // and ordering issues. Tool indicators from the completed turn
        // are replaced by the canonical history.
        send('get_history', { session_name: payload.session_name });
        showActivity(false);
    }
    updateSessionState(payload.session_name, 'idle');
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

    // Check if this message matches a pending prompt.
    if (state.pendingPrompt && payload.content === state.pendingPrompt) {
        state.pendingPrompt = null;
    }
    const pending = document.getElementById('pending-message');
    if (pending) pending.remove();

    appendChatMessage('user', payload.content, false);
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
    const s = state.sessions.find(s => s.name === state.activeSession);

    // If the LLM is currently processing, show interrupt/queue choice.
    if (s && s.is_processing) {
        showInterruptQueueChoice(prompt);
        return;
    }

    // Clear error state so the session can recover on retry.
    if (s && s.state === 'error') {
        updateSessionState(state.activeSession, 'processing');
        showActivity(true);
        updateActivity('💭 Thinking...');
    }
    doSendMessage(prompt);
}

function doSendMessage(prompt) {
    send('send_message', { session_name: state.activeSession, prompt });
    state.pendingPrompt = prompt;
    appendPendingMessage(prompt, false);

    const input = document.getElementById('chat-input');
    input.value = '';
    input.style.height = 'auto';
    document.getElementById('send-btn').disabled = true;
}

function showInterruptQueueChoice(prompt) {
    var existing = document.getElementById('send-choice');
    if (existing) existing.remove();

    var div = document.createElement('div');
    div.id = 'send-choice';
    div.className = 'send-choice';
    div.innerHTML = '<div class="send-choice-text">LLM is working. How should this message be sent?</div>' +
        '<div class="send-choice-buttons">' +
        '<button class="btn btn-sm" id="choice-queue">⏳ Queue</button>' +
        '<button class="btn btn-sm btn-danger" id="choice-interrupt">⚡ Interrupt</button>' +
        '<button class="btn btn-sm" id="choice-cancel">Cancel</button>' +
        '</div>';
    document.getElementById('chat-input-area').before(div);

    document.getElementById('choice-queue').onclick = function() {
        div.remove();
        state.pendingPrompt = prompt;
        appendPendingMessage(prompt, true);
        send('send_message', { session_name: state.activeSession, prompt });
        document.getElementById('chat-input').value = '';
        document.getElementById('chat-input').style.height = 'auto';
        document.getElementById('send-btn').disabled = true;
    };
    document.getElementById('choice-interrupt').onclick = function() {
        div.remove();
        send('abort_session', { session_name: state.activeSession });
        setTimeout(function() { doSendMessage(prompt); }, 500);
    };
    document.getElementById('choice-cancel').onclick = function() {
        div.remove();
    };
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
// QR Code generator (qrcode-generator by Kazuhiko Arase, MIT license)
// ---------------------------------------------------------------------------

var qrcode=function(){var t=function(t,r){var e=t,n=g[r],o=null,i=0,a=null,u=[],f={},c=function(t,r){o=function(t){for(var r=new Array(t),e=0;e<t;e+=1){r[e]=new Array(t);for(var n=0;n<t;n+=1)r[e][n]=null}return r}(i=4*e+17),l(0,0),l(i-7,0),l(0,i-7),s(),h(),d(t,r),e>=7&&v(t),null==a&&(a=p(e,n,u)),w(a,r)},l=function(t,r){for(var e=-1;e<=7;e+=1)if(!(t+e<=-1||i<=t+e))for(var n=-1;n<=7;n+=1)r+n<=-1||i<=r+n||(o[t+e][r+n]=0<=e&&e<=6&&(0==n||6==n)||0<=n&&n<=6&&(0==e||6==e)||2<=e&&e<=4&&2<=n&&n<=4)},h=function(){for(var t=8;t<i-8;t+=1)null==o[t][6]&&(o[t][6]=t%2==0);for(var r=8;r<i-8;r+=1)null==o[6][r]&&(o[6][r]=r%2==0)},s=function(){for(var t=B.getPatternPosition(e),r=0;r<t.length;r+=1)for(var n=0;n<t.length;n+=1){var i=t[r],a=t[n];if(null==o[i][a])for(var u=-2;u<=2;u+=1)for(var f=-2;f<=2;f+=1)o[i+u][a+f]=-2==u||2==u||-2==f||2==f||0==u&&0==f}},v=function(t){for(var r=B.getBCHTypeNumber(e),n=0;n<18;n+=1){var a=!t&&1==(r>>n&1);o[Math.floor(n/3)][n%3+i-8-3]=a}for(n=0;n<18;n+=1){a=!t&&1==(r>>n&1);o[n%3+i-8-3][Math.floor(n/3)]=a}},d=function(t,r){for(var e=n<<3|r,a=B.getBCHTypeInfo(e),u=0;u<15;u+=1){var f=!t&&1==(a>>u&1);u<6?o[u][8]=f:u<8?o[u+1][8]=f:o[i-15+u][8]=f}for(u=0;u<15;u+=1){f=!t&&1==(a>>u&1);u<8?o[8][i-u-1]=f:u<9?o[8][15-u-1+1]=f:o[8][15-u-1]=f}o[i-8][8]=!t},w=function(t,r){for(var e=-1,n=i-1,a=7,u=0,f=B.getMaskFunction(r),c=i-1;c>0;c-=2)for(6==c&&(c-=1);;){for(var g=0;g<2;g+=1)if(null==o[n][c-g]){var l=!1;u<t.length&&(l=1==(t[u]>>>a&1)),f(n,c-g)&&(l=!l),o[n][c-g]=l,-1==(a-=1)&&(u+=1,a=7)}if((n+=e)<0||i<=n){n-=e,e=-e;break}}},p=function(t,r,e){for(var n=A.getRSBlocks(t,r),o=b(),i=0;i<e.length;i+=1){var a=e[i];o.put(a.getMode(),4),o.put(a.getLength(),B.getLengthInBits(a.getMode(),t)),a.write(o)}var u=0;for(i=0;i<n.length;i+=1)u+=n[i].dataCount;if(o.getLengthInBits()>8*u)throw"code length overflow. ("+o.getLengthInBits()+">"+8*u+")";for(o.getLengthInBits()+4<=8*u&&o.put(0,4);o.getLengthInBits()%8!=0;)o.putBit(!1);for(;!(o.getLengthInBits()>=8*u||(o.put(236,8),o.getLengthInBits()>=8*u));)o.put(17,8);return function(t,r){for(var e=0,n=0,o=0,i=new Array(r.length),a=new Array(r.length),u=0;u<r.length;u+=1){var f=r[u].dataCount,c=r[u].totalCount-f;n=Math.max(n,f),o=Math.max(o,c),i[u]=new Array(f);for(var g=0;g<i[u].length;g+=1)i[u][g]=255&t.getBuffer()[g+e];e+=f;var l=B.getErrorCorrectPolynomial(c),h=k(i[u],l.getLength()-1).mod(l);for(a[u]=new Array(l.getLength()-1),g=0;g<a[u].length;g+=1){var s=g+h.getLength()-a[u].length;a[u][g]=s>=0?h.getAt(s):0}}var v=0;for(g=0;g<r.length;g+=1)v+=r[g].totalCount;var d=new Array(v),w=0;for(g=0;g<n;g+=1)for(u=0;u<r.length;u+=1)g<i[u].length&&(d[w]=i[u][g],w+=1);for(g=0;g<o;g+=1)for(u=0;u<r.length;u+=1)g<a[u].length&&(d[w]=a[u][g],w+=1);return d}(o,n)};f.addData=function(t,r){var e=null;switch(r=r||"Byte"){case"Numeric":e=M(t);break;case"Alphanumeric":e=x(t);break;case"Byte":e=m(t);break;case"Kanji":e=L(t);break;default:throw"mode:"+r}u.push(e),a=null},f.isDark=function(t,r){if(t<0||i<=t||r<0||i<=r)throw t+","+r;return o[t][r]},f.getModuleCount=function(){return i},f.make=function(){if(e<1){for(var t=1;t<40;t++){for(var r=A.getRSBlocks(t,n),o=b(),i=0;i<u.length;i++){var a=u[i];o.put(a.getMode(),4),o.put(a.getLength(),B.getLengthInBits(a.getMode(),t)),a.write(o)}var g=0;for(i=0;i<r.length;i++)g+=r[i].dataCount;if(o.getLengthInBits()<=8*g)break}e=t}c(!1,function(){for(var t=0,r=0,e=0;e<8;e+=1){c(!0,e);var n=B.getLostPoint(f);(0==e||t>n)&&(t=n,r=e)}return r}())},f.createTableTag=function(t,r){t=t||2;var e="";e+='<table style="',e+=" border-width: 0px; border-style: none;",e+=" border-collapse: collapse;",e+=" padding: 0px; margin: "+(r=void 0===r?4*t:r)+"px;",e+='">',e+="<tbody>";for(var n=0;n<f.getModuleCount();n+=1){e+="<tr>";for(var o=0;o<f.getModuleCount();o+=1)e+='<td style="',e+=" border-width: 0px; border-style: none;",e+=" border-collapse: collapse;",e+=" padding: 0px; margin: 0px;",e+=" width: "+t+"px;",e+=" height: "+t+"px;",e+=" background-color: ",e+=f.isDark(n,o)?"#000000":"#ffffff",e+=";",e+='"/>';e+="</tr>"}return e+="</tbody>",e+="</table>"},f.createSvgTag=function(t,r,e,n){var o={};"object"==typeof arguments[0]&&(t=(o=arguments[0]).cellSize,r=o.margin,e=o.alt,n=o.title),t=t||2,r=void 0===r?4*t:r,(e="string"==typeof e?{text:e}:e||{}).text=e.text||null,e.id=e.text?e.id||"qrcode-description":null,(n="string"==typeof n?{text:n}:n||{}).text=n.text||null,n.id=n.text?n.id||"qrcode-title":null;var i,a,u,c,g=f.getModuleCount()*t+2*r,l="";for(c="l"+t+",0 0,"+t+" -"+t+",0 0,-"+t+"z ",l+='<svg version="1.1" xmlns="http://www.w3.org/2000/svg"',l+=o.scalable?"":' width="'+g+'px" height="'+g+'px"',l+=' viewBox="0 0 '+g+" "+g+'" ',l+=' preserveAspectRatio="xMinYMin meet"',l+=n.text||e.text?' role="img" aria-labelledby="'+y([n.id,e.id].join(" ").trim())+'"':"",l+=">",l+=n.text?'<title id="'+y(n.id)+'">'+y(n.text)+"</title>":"",l+=e.text?'<description id="'+y(e.id)+'">'+y(e.text)+"</description>":"",l+='<rect width="100%" height="100%" fill="white" cx="0" cy="0"/>',l+='<path d="',a=0;a<f.getModuleCount();a+=1)for(u=a*t+r,i=0;i<f.getModuleCount();i+=1)f.isDark(a,i)&&(l+="M"+(i*t+r)+","+u+c);return l+='" stroke="transparent" fill="black"/>',l+="</svg>"},f.createDataURL=function(t,r){t=t||2,r=void 0===r?4*t:r;var e=f.getModuleCount()*t+2*r,n=r,o=e-r;return I(e,e,function(r,e){if(n<=r&&r<o&&n<=e&&e<o){var i=Math.floor((r-n)/t),a=Math.floor((e-n)/t);return f.isDark(a,i)?0:1}return 1})},f.createImgTag=function(t,r,e){t=t||2,r=void 0===r?4*t:r;var n=f.getModuleCount()*t+2*r,o="";return o+="<img",o+=' src="',o+=f.createDataURL(t,r),o+='"',o+=' width="',o+=n,o+='"',o+=' height="',o+=n,o+='"',e&&(o+=' alt="',o+=y(e),o+='"'),o+="/>"};var y=function(t){for(var r="",e=0;e<t.length;e+=1){var n=t.charAt(e);switch(n){case"<":r+="&lt;";break;case">":r+="&gt;";break;case"&":r+="&amp;";break;case'"':r+="&quot;";break;default:r+=n}}return r};return f.createASCII=function(t,r){if((t=t||1)<2)return function(t){t=void 0===t?2:t;var r,e,n,o,i,a=1*f.getModuleCount()+2*t,u=t,c=a-t,g={"██":"█","█ ":"▀"," █":"▄","  ":" "},l={"██":"▀","█ ":"▀"," █":" ","  ":" "},h="";for(r=0;r<a;r+=2){for(n=Math.floor((r-u)/1),o=Math.floor((r+1-u)/1),e=0;e<a;e+=1)i="█",u<=e&&e<c&&u<=r&&r<c&&f.isDark(n,Math.floor((e-u)/1))&&(i=" "),u<=e&&e<c&&u<=r+1&&r+1<c&&f.isDark(o,Math.floor((e-u)/1))?i+=" ":i+="█",h+=t<1&&r+1>=c?l[i]:g[i];h+="\n"}return a%2&&t>0?h.substring(0,h.length-a-1)+Array(a+1).join("▀"):h.substring(0,h.length-1)}(r);t-=1,r=void 0===r?2*t:r;var e,n,o,i,a=f.getModuleCount()*t+2*r,u=r,c=a-r,g=Array(t+1).join("██"),l=Array(t+1).join("  "),h="",s="";for(e=0;e<a;e+=1){for(o=Math.floor((e-u)/t),s="",n=0;n<a;n+=1)i=1,u<=n&&n<c&&u<=e&&e<c&&f.isDark(o,Math.floor((n-u)/t))&&(i=0),s+=i?g:l;for(o=0;o<t;o+=1)h+=s+"\n"}return h.substring(0,h.length-1)},f.renderTo2dContext=function(t,r){r=r||2;for(var e=f.getModuleCount(),n=0;n<e;n++)for(var o=0;o<e;o++)t.fillStyle=f.isDark(n,o)?"black":"white",t.fillRect(o*r,n*r,r,r)},f};t.stringToBytes=(t.stringToBytesFuncs={default:function(t){for(var r=[],e=0;e<t.length;e+=1){var n=t.charCodeAt(e);r.push(255&n)}return r}}).default,t.createStringToBytes=function(t,r){var e=function(){for(var e=S(t),n=function(){var t=e.read();if(-1==t)throw"eof";return t},o=0,i={};;){var a=e.read();if(-1==a)break;var u=n(),f=n()<<8|n();i[String.fromCharCode(a<<8|u)]=f,o+=1}if(o!=r)throw o+" != "+r;return i}(),n="?".charCodeAt(0);return function(t){for(var r=[],o=0;o<t.length;o+=1){var i=t.charCodeAt(o);if(i<128)r.push(i);else{var a=e[t.charAt(o)];"number"==typeof a?(255&a)==a?r.push(a):(r.push(a>>>8),r.push(255&a)):r.push(n)}}return r}};var r,e,n,o,i,a=1,u=2,f=4,c=8,g={L:1,M:0,Q:3,H:2},l=0,h=1,s=2,v=3,d=4,w=5,p=6,y=7,B=(r=[[],[6,18],[6,22],[6,26],[6,30],[6,34],[6,22,38],[6,24,42],[6,26,46],[6,28,50],[6,30,54],[6,32,58],[6,34,62],[6,26,46,66],[6,26,48,70],[6,26,50,74],[6,30,54,78],[6,30,56,82],[6,30,58,86],[6,34,62,90],[6,28,50,72,94],[6,26,50,74,98],[6,30,54,78,102],[6,28,54,80,106],[6,32,58,84,110],[6,30,58,86,114],[6,34,62,90,118],[6,26,50,74,98,122],[6,30,54,78,102,126],[6,26,52,78,104,130],[6,30,56,82,108,134],[6,34,60,86,112,138],[6,30,58,86,114,142],[6,34,62,90,118,146],[6,30,54,78,102,126,150],[6,24,50,76,102,128,154],[6,28,54,80,106,132,158],[6,32,58,84,110,136,162],[6,26,54,82,110,138,166],[6,30,58,86,114,142,170]],e=1335,n=7973,i=function(t){for(var r=0;0!=t;)r+=1,t>>>=1;return r},(o={}).getBCHTypeInfo=function(t){for(var r=t<<10;i(r)-i(e)>=0;)r^=e<<i(r)-i(e);return 21522^(t<<10|r)},o.getBCHTypeNumber=function(t){for(var r=t<<12;i(r)-i(n)>=0;)r^=n<<i(r)-i(n);return t<<12|r},o.getPatternPosition=function(t){return r[t-1]},o.getMaskFunction=function(t){switch(t){case l:return function(t,r){return(t+r)%2==0};case h:return function(t,r){return t%2==0};case s:return function(t,r){return r%3==0};case v:return function(t,r){return(t+r)%3==0};case d:return function(t,r){return(Math.floor(t/2)+Math.floor(r/3))%2==0};case w:return function(t,r){return t*r%2+t*r%3==0};case p:return function(t,r){return(t*r%2+t*r%3)%2==0};case y:return function(t,r){return(t*r%3+(t+r)%2)%2==0};default:throw"bad maskPattern:"+t}},o.getErrorCorrectPolynomial=function(t){for(var r=k([1],0),e=0;e<t;e+=1)r=r.multiply(k([1,C.gexp(e)],0));return r},o.getLengthInBits=function(t,r){if(1<=r&&r<10)switch(t){case a:return 10;case u:return 9;case f:case c:return 8;default:throw"mode:"+t}else if(r<27)switch(t){case a:return 12;case u:return 11;case f:return 16;case c:return 10;default:throw"mode:"+t}else{if(!(r<41))throw"type:"+r;switch(t){case a:return 14;case u:return 13;case f:return 16;case c:return 12;default:throw"mode:"+t}}},o.getLostPoint=function(t){for(var r=t.getModuleCount(),e=0,n=0;n<r;n+=1)for(var o=0;o<r;o+=1){for(var i=0,a=t.isDark(n,o),u=-1;u<=1;u+=1)if(!(n+u<0||r<=n+u))for(var f=-1;f<=1;f+=1)o+f<0||r<=o+f||0==u&&0==f||a==t.isDark(n+u,o+f)&&(i+=1);i>5&&(e+=3+i-5)}for(n=0;n<r-1;n+=1)for(o=0;o<r-1;o+=1){var c=0;t.isDark(n,o)&&(c+=1),t.isDark(n+1,o)&&(c+=1),t.isDark(n,o+1)&&(c+=1),t.isDark(n+1,o+1)&&(c+=1),0!=c&&4!=c||(e+=3)}for(n=0;n<r;n+=1)for(o=0;o<r-6;o+=1)t.isDark(n,o)&&!t.isDark(n,o+1)&&t.isDark(n,o+2)&&t.isDark(n,o+3)&&t.isDark(n,o+4)&&!t.isDark(n,o+5)&&t.isDark(n,o+6)&&(e+=40);for(o=0;o<r;o+=1)for(n=0;n<r-6;n+=1)t.isDark(n,o)&&!t.isDark(n+1,o)&&t.isDark(n+2,o)&&t.isDark(n+3,o)&&t.isDark(n+4,o)&&!t.isDark(n+5,o)&&t.isDark(n+6,o)&&(e+=40);var g=0;for(o=0;o<r;o+=1)for(n=0;n<r;n+=1)t.isDark(n,o)&&(g+=1);return e+=Math.abs(100*g/r/r-50)/5*10},o),C=function(){for(var t=new Array(256),r=new Array(256),e=0;e<8;e+=1)t[e]=1<<e;for(e=8;e<256;e+=1)t[e]=t[e-4]^t[e-5]^t[e-6]^t[e-8];for(e=0;e<255;e+=1)r[t[e]]=e;var n={glog:function(t){if(t<1)throw"glog("+t+")";return r[t]},gexp:function(r){for(;r<0;)r+=255;for(;r>=256;)r-=255;return t[r]}};return n}();function k(t,r){if(void 0===t.length)throw t.length+"/"+r;var e=function(){for(var e=0;e<t.length&&0==t[e];)e+=1;for(var n=new Array(t.length-e+r),o=0;o<t.length-e;o+=1)n[o]=t[o+e];return n}(),n={getAt:function(t){return e[t]},getLength:function(){return e.length},multiply:function(t){for(var r=new Array(n.getLength()+t.getLength()-1),e=0;e<n.getLength();e+=1)for(var o=0;o<t.getLength();o+=1)r[e+o]^=C.gexp(C.glog(n.getAt(e))+C.glog(t.getAt(o)));return k(r,0)},mod:function(t){if(n.getLength()-t.getLength()<0)return n;for(var r=C.glog(n.getAt(0))-C.glog(t.getAt(0)),e=new Array(n.getLength()),o=0;o<n.getLength();o+=1)e[o]=n.getAt(o);for(o=0;o<t.getLength();o+=1)e[o]^=C.gexp(C.glog(t.getAt(o))+r);return k(e,0).mod(t)}};return n}var A=function(){var t=[[1,26,19],[1,26,16],[1,26,13],[1,26,9],[1,44,34],[1,44,28],[1,44,22],[1,44,16],[1,70,55],[1,70,44],[2,35,17],[2,35,13],[1,100,80],[2,50,32],[2,50,24],[4,25,9],[1,134,108],[2,67,43],[2,33,15,2,34,16],[2,33,11,2,34,12],[2,86,68],[4,43,27],[4,43,19],[4,43,15],[2,98,78],[4,49,31],[2,32,14,4,33,15],[4,39,13,1,40,14],[2,121,97],[2,60,38,2,61,39],[4,40,18,2,41,19],[4,40,14,2,41,15],[2,146,116],[3,58,36,2,59,37],[4,36,16,4,37,17],[4,36,12,4,37,13],[2,86,68,2,87,69],[4,69,43,1,70,44],[6,43,19,2,44,20],[6,43,15,2,44,16],[4,101,81],[1,80,50,4,81,51],[4,50,22,4,51,23],[3,36,12,8,37,13],[2,116,92,2,117,93],[6,58,36,2,59,37],[4,46,20,6,47,21],[7,42,14,4,43,15],[4,133,107],[8,59,37,1,60,38],[8,44,20,4,45,21],[12,33,11,4,34,12],[3,145,115,1,146,116],[4,64,40,5,65,41],[11,36,16,5,37,17],[11,36,12,5,37,13],[5,109,87,1,110,88],[5,65,41,5,66,42],[5,54,24,7,55,25],[11,36,12,7,37,13],[5,122,98,1,123,99],[7,73,45,3,74,46],[15,43,19,2,44,20],[3,45,15,13,46,16],[1,135,107,5,136,108],[10,74,46,1,75,47],[1,50,22,15,51,23],[2,42,14,17,43,15],[5,150,120,1,151,121],[9,69,43,4,70,44],[17,50,22,1,51,23],[2,42,14,19,43,15],[3,141,113,4,142,114],[3,70,44,11,71,45],[17,47,21,4,48,22],[9,39,13,16,40,14],[3,135,107,5,136,108],[3,67,41,13,68,42],[15,54,24,5,55,25],[15,43,15,10,44,16],[4,144,116,4,145,117],[17,68,42],[17,50,22,6,51,23],[19,46,16,6,47,17],[2,139,111,7,140,112],[17,74,46],[7,54,24,16,55,25],[34,37,13],[4,151,121,5,152,122],[4,75,47,14,76,48],[11,54,24,14,55,25],[16,45,15,14,46,16],[6,147,117,4,148,118],[6,73,45,14,74,46],[11,54,24,16,55,25],[30,46,16,2,47,17],[8,132,106,4,133,107],[8,75,47,13,76,48],[7,54,24,22,55,25],[22,45,15,13,46,16],[10,142,114,2,143,115],[19,74,46,4,75,47],[28,50,22,6,51,23],[33,46,16,4,47,17],[8,152,122,4,153,123],[22,73,45,3,74,46],[8,53,23,26,54,24],[12,45,15,28,46,16],[3,147,117,10,148,118],[3,73,45,23,74,46],[4,54,24,31,55,25],[11,45,15,31,46,16],[7,146,116,7,147,117],[21,73,45,7,74,46],[1,53,23,37,54,24],[19,45,15,26,46,16],[5,145,115,10,146,116],[19,75,47,10,76,48],[15,54,24,25,55,25],[23,45,15,25,46,16],[13,145,115,3,146,116],[2,74,46,29,75,47],[42,54,24,1,55,25],[23,45,15,28,46,16],[17,145,115],[10,74,46,23,75,47],[10,54,24,35,55,25],[19,45,15,35,46,16],[17,145,115,1,146,116],[14,74,46,21,75,47],[29,54,24,19,55,25],[11,45,15,46,46,16],[13,145,115,6,146,116],[14,74,46,23,75,47],[44,54,24,7,55,25],[59,46,16,1,47,17],[12,151,121,7,152,122],[12,75,47,26,76,48],[39,54,24,14,55,25],[22,45,15,41,46,16],[6,151,121,14,152,122],[6,75,47,34,76,48],[46,54,24,10,55,25],[2,45,15,64,46,16],[17,152,122,4,153,123],[29,74,46,14,75,47],[49,54,24,10,55,25],[24,45,15,46,46,16],[4,152,122,18,153,123],[13,74,46,32,75,47],[48,54,24,14,55,25],[42,45,15,32,46,16],[20,147,117,4,148,118],[40,75,47,7,76,48],[43,54,24,22,55,25],[10,45,15,67,46,16],[19,148,118,6,149,119],[18,75,47,31,76,48],[34,54,24,34,55,25],[20,45,15,61,46,16]],r=function(t,r){var e={};return e.totalCount=t,e.dataCount=r,e},e={};return e.getRSBlocks=function(e,n){var o=function(r,e){switch(e){case g.L:return t[4*(r-1)+0];case g.M:return t[4*(r-1)+1];case g.Q:return t[4*(r-1)+2];case g.H:return t[4*(r-1)+3];default:return}}(e,n);if(void 0===o)throw"bad rs block @ typeNumber:"+e+"/errorCorrectionLevel:"+n;for(var i=o.length/3,a=[],u=0;u<i;u+=1)for(var f=o[3*u+0],c=o[3*u+1],l=o[3*u+2],h=0;h<f;h+=1)a.push(r(c,l));return a},e}(),b=function(){var t=[],r=0,e={getBuffer:function(){return t},getAt:function(r){var e=Math.floor(r/8);return 1==(t[e]>>>7-r%8&1)},put:function(t,r){for(var n=0;n<r;n+=1)e.putBit(1==(t>>>r-n-1&1))},getLengthInBits:function(){return r},putBit:function(e){var n=Math.floor(r/8);t.length<=n&&t.push(0),e&&(t[n]|=128>>>r%8),r+=1}};return e},M=function(t){var r=a,e=t,n={getMode:function(){return r},getLength:function(t){return e.length},write:function(t){for(var r=e,n=0;n+2<r.length;)t.put(o(r.substring(n,n+3)),10),n+=3;n<r.length&&(r.length-n==1?t.put(o(r.substring(n,n+1)),4):r.length-n==2&&t.put(o(r.substring(n,n+2)),7))}},o=function(t){for(var r=0,e=0;e<t.length;e+=1)r=10*r+i(t.charAt(e));return r},i=function(t){if("0"<=t&&t<="9")return t.charCodeAt(0)-"0".charCodeAt(0);throw"illegal char :"+t};return n},x=function(t){var r=u,e=t,n={getMode:function(){return r},getLength:function(t){return e.length},write:function(t){for(var r=e,n=0;n+1<r.length;)t.put(45*o(r.charAt(n))+o(r.charAt(n+1)),11),n+=2;n<r.length&&t.put(o(r.charAt(n)),6)}},o=function(t){if("0"<=t&&t<="9")return t.charCodeAt(0)-"0".charCodeAt(0);if("A"<=t&&t<="Z")return t.charCodeAt(0)-"A".charCodeAt(0)+10;switch(t){case" ":return 36;case"$":return 37;case"%":return 38;case"*":return 39;case"+":return 40;case"-":return 41;case".":return 42;case"/":return 43;case":":return 44;default:throw"illegal char :"+t}};return n},m=function(r){var e=f,n=t.stringToBytes(r),o={getMode:function(){return e},getLength:function(t){return n.length},write:function(t){for(var r=0;r<n.length;r+=1)t.put(n[r],8)}};return o},L=function(r){var e=c,n=t.stringToBytesFuncs.SJIS;if(!n)throw"sjis not supported.";!function(){var t=n("友");if(2!=t.length||38726!=(t[0]<<8|t[1]))throw"sjis not supported."}();var o=n(r),i={getMode:function(){return e},getLength:function(t){return~~(o.length/2)},write:function(t){for(var r=o,e=0;e+1<r.length;){var n=(255&r[e])<<8|255&r[e+1];if(33088<=n&&n<=40956)n-=33088;else{if(!(57408<=n&&n<=60351))throw"illegal char at "+(e+1)+"/"+n;n-=49472}n=192*(n>>>8&255)+(255&n),t.put(n,13),e+=2}if(e<r.length)throw"illegal char at "+(e+1)}};return i},D=function(){var t=[],r={writeByte:function(r){t.push(255&r)},writeShort:function(t){r.writeByte(t),r.writeByte(t>>>8)},writeBytes:function(t,e,n){e=e||0,n=n||t.length;for(var o=0;o<n;o+=1)r.writeByte(t[o+e])},writeString:function(t){for(var e=0;e<t.length;e+=1)r.writeByte(t.charCodeAt(e))},toByteArray:function(){return t},toString:function(){var r="";r+="[";for(var e=0;e<t.length;e+=1)e>0&&(r+=","),r+=t[e];return r+="]"}};return r},S=function(t){var r=t,e=0,n=0,o=0,i={read:function(){for(;o<8;){if(e>=r.length){if(0==o)return-1;throw"unexpected end of file./"+o}var t=r.charAt(e);if(e+=1,"="==t)return o=0,-1;t.match(/^\s$/)||(n=n<<6|a(t.charCodeAt(0)),o+=6)}var i=n>>>o-8&255;return o-=8,i}},a=function(t){if(65<=t&&t<=90)return t-65;if(97<=t&&t<=122)return t-97+26;if(48<=t&&t<=57)return t-48+52;if(43==t)return 62;if(47==t)return 63;throw"c:"+t};return i},I=function(t,r,e){for(var n=function(t,r){var e=t,n=r,o=new Array(t*r),i={setPixel:function(t,r,n){o[r*e+t]=n},write:function(t){t.writeString("GIF87a"),t.writeShort(e),t.writeShort(n),t.writeByte(128),t.writeByte(0),t.writeByte(0),t.writeByte(0),t.writeByte(0),t.writeByte(0),t.writeByte(255),t.writeByte(255),t.writeByte(255),t.writeString(","),t.writeShort(0),t.writeShort(0),t.writeShort(e),t.writeShort(n),t.writeByte(0);var r=a(2);t.writeByte(2);for(var o=0;r.length-o>255;)t.writeByte(255),t.writeBytes(r,o,255),o+=255;t.writeByte(r.length-o),t.writeBytes(r,o,r.length-o),t.writeByte(0),t.writeString(";")}},a=function(t){for(var r=1<<t,e=1+(1<<t),n=t+1,i=u(),a=0;a<r;a+=1)i.add(String.fromCharCode(a));i.add(String.fromCharCode(r)),i.add(String.fromCharCode(e));var f,c,g,l=D(),h=(f=l,c=0,g=0,{write:function(t,r){if(t>>>r!=0)throw"length over";for(;c+r>=8;)f.writeByte(255&(t<<c|g)),r-=8-c,t>>>=8-c,g=0,c=0;g|=t<<c,c+=r},flush:function(){c>0&&f.writeByte(g)}});h.write(r,n);var s=0,v=String.fromCharCode(o[s]);for(s+=1;s<o.length;){var d=String.fromCharCode(o[s]);s+=1,i.contains(v+d)?v+=d:(h.write(i.indexOf(v),n),i.size()<4095&&(i.size()==1<<n&&(n+=1),i.add(v+d)),v=d)}return h.write(i.indexOf(v),n),h.write(e,n),h.flush(),l.toByteArray()},u=function(){var t={},r=0,e={add:function(n){if(e.contains(n))throw"dup key:"+n;t[n]=r,r+=1},size:function(){return r},indexOf:function(r){return t[r]},contains:function(r){return void 0!==t[r]}};return e};return i}(t,r),o=0;o<r;o+=1)for(var i=0;i<t;i+=1)n.setPixel(i,o,e(i,o));var a=D();n.write(a);for(var u=function(){var t=0,r=0,e=0,n="",o={},i=function(t){n+=String.fromCharCode(a(63&t))},a=function(t){if(t<0);else{if(t<26)return 65+t;if(t<52)return t-26+97;if(t<62)return t-52+48;if(62==t)return 43;if(63==t)return 47}throw"n:"+t};return o.writeByte=function(n){for(t=t<<8|255&n,r+=8,e+=1;r>=6;)i(t>>>r-6),r-=6},o.flush=function(){if(r>0&&(i(t<<6-r),t=0,r=0),e%3!=0)for(var o=3-e%3,a=0;a<o;a+=1)n+="="},o.toString=function(){return n},o}(),f=a.toByteArray(),c=0;c<f.length;c+=1)u.writeByte(f[c]);return u.flush(),"data:image/gif;base64,"+u};return t}();qrcode.stringToBytesFuncs["UTF-8"]=function(t){return function(t){for(var r=[],e=0;e<t.length;e++){var n=t.charCodeAt(e);n<128?r.push(n):n<2048?r.push(192|n>>6,128|63&n):n<55296||n>=57344?r.push(224|n>>12,128|n>>6&63,128|63&n):(e++,n=65536+((1023&n)<<10|1023&t.charCodeAt(e)),r.push(240|n>>18,128|n>>12&63,128|n>>6&63,128|63&n))}return r}(t)},function(t){"function"==typeof define&&define.amd?define([],t):"object"==typeof exports&&(module.exports=t())}(function(){return qrcode});
function generateQRCode(text, size) {
    var qr = qrcode(0, 'L');
    qr.addData(text);
    qr.make();
    var count = qr.getModuleCount();
    var canvas = document.createElement('canvas');
    canvas.width = size;
    canvas.height = size;
    var ctx = canvas.getContext('2d');
    var cellSize = size / (count + 8);
    ctx.fillStyle = '#ffffff';
    ctx.fillRect(0, 0, size, size);
    ctx.fillStyle = '#000000';
    for (var y = 0; y < count; y++) {
        for (var x = 0; x < count; x++) {
            if (qr.isDark(y, x)) {
                ctx.fillRect((x + 4) * cellSize, (y + 4) * cellSize, cellSize + 0.5, cellSize + 0.5);
            }
        }
    }
    return canvas;
}
