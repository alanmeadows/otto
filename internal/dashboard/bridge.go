package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/alanmeadows/otto/internal/copilot"
	"github.com/coder/websocket"
)

// Bridge manages WebSocket connections and broadcasts session events to clients.
type Bridge struct {
	manager       *copilot.Manager
	clients       map[string]*wsClient
	mu            sync.RWMutex
	nextID        int
	onStartTunnel     func()
	onStopTunnel      func()
	onListWorktrees   func() []WorktreeSummary
	onSetTunnelConfig   func(SetTunnelConfigPayload)
	onAddAllowedUser    func(string)
	onRemoveAllowedUser func(string)
	onGetAllowedUsers   func() AllowedUsersListPayload
}

type wsClient struct {
	conn          *websocket.Conn
	ctx           context.Context
	mu            sync.Mutex // serializes writes
	sessionFilter string     // if set, only receive events for this session (shared view)
	readOnly      bool       // if true, can't send prompts
}

// NewBridge creates a Bridge wired to the given copilot Manager.
// Call this after the Manager is created; it registers the event handler.
func NewBridge(mgr *copilot.Manager) *Bridge {
	b := &Bridge{
		manager: mgr,
		clients: make(map[string]*wsClient),
	}
	mgr.SetEventHandler(b.onSessionEvent)
	return b
}

// HandleWS is the HTTP handler for the /ws endpoint.
func (b *Bridge) HandleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // accept any origin for DevTunnel access
	})
	if err != nil {
		slog.Warn("websocket accept failed", "error", err)
		return
	}

	ctx := r.Context()
	b.mu.Lock()
	b.nextID++
	id := fmt.Sprintf("client-%d", b.nextID)
	client := &wsClient{conn: c, ctx: ctx}
	b.clients[id] = client
	b.mu.Unlock()

	slog.Info("websocket client connected", "id", id, "remote", r.RemoteAddr)

	// Send initial state.
	b.sendSessionsList(client)
	b.sendPersistedSessions(client)
	b.sendAllowedUsers(client)

	// Read loop — handle client commands.
	b.readLoop(ctx, id, client)
}

// HandleSharedWS is the HTTP handler for /ws/shared/{token} — read-only, single session.
func (b *Bridge) HandleSharedWS(w http.ResponseWriter, r *http.Request, sessionName string, mode string) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		slog.Warn("shared websocket accept failed", "error", err)
		return
	}

	ctx := r.Context()
	b.mu.Lock()
	b.nextID++
	id := fmt.Sprintf("shared-%d", b.nextID)
	client := &wsClient{conn: c, ctx: ctx, sessionFilter: sessionName, readOnly: mode != "readwrite"}
	b.clients[id] = client
	b.mu.Unlock()

	slog.Info("shared websocket client connected", "id", id, "session", sessionName, "mode", mode, "remote", r.RemoteAddr)

	// Send the session history immediately.
	history, err := b.manager.GetHistory(sessionName)
	if err == nil {
		msgs := make([]MessageSummary, 0, len(history))
		for _, m := range history {
			if m.Content == "" {
				continue
			}
			msgs = append(msgs, MessageSummary{Role: m.Role, Content: m.Content, Timestamp: m.Timestamp})
		}
		b.sendTo(client, MsgSessionHistory, SessionHistoryPayload{
			SessionName: sessionName,
			Messages:    msgs,
		})
	}

	b.readLoop(ctx, id, client)
}

func (b *Bridge) readLoop(ctx context.Context, id string, client *wsClient) {
	defer func() {
		b.mu.Lock()
		delete(b.clients, id)
		b.mu.Unlock()
		client.conn.Close(websocket.StatusNormalClosure, "")
		slog.Info("websocket client disconnected", "id", id)
	}()

	for {
		_, data, err := client.conn.Read(ctx)
		if err != nil {
			return // client disconnected
		}

		var msg BridgeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Warn("invalid ws message", "error", err, "client", id)
			continue
		}

		b.handleClientMessage(ctx, client, msg)
	}
}

func (b *Bridge) handleClientMessage(ctx context.Context, client *wsClient, msg BridgeMessage) {
	switch msg.Type {
	case MsgGetSessions:
		b.sendSessionsList(client)

	case MsgGetHistory:
		var p GetHistoryPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		history, err := b.manager.GetHistory(p.SessionName)
		if err != nil {
			return
		}
		msgs := make([]MessageSummary, len(history))
		for i, m := range history {
			msgs[i] = MessageSummary{Role: m.Role, Content: m.Content, Timestamp: m.Timestamp}
		}
		b.sendTo(client, MsgSessionHistory, SessionHistoryPayload{
			SessionName: p.SessionName,
			Messages:    msgs,
		})

	case MsgSendMessage:
		if client.readOnly {
			return // read-only shared clients can't send prompts
		}
		var p SendMessagePayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		// For shared clients, use their session filter as the target.
		sessionName := p.SessionName
		if client.sessionFilter != "" {
			sessionName = client.sessionFilter
		}
		go func() {
			if err := b.manager.SendPrompt(ctx, sessionName, p.Prompt); err != nil {
				slog.Warn("send prompt failed", "session", sessionName, "error", err)
			}
		}()

	case MsgCreateSession:
		var p CreateSessionPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		go func() {
			if err := b.manager.CreateSession(ctx, p.Name, p.Model, p.WorkingDir); err != nil {
				slog.Warn("create session failed", "name", p.Name, "error", err)
				b.sendTo(client, MsgSessionError, ErrorPayload{
					SessionName: p.Name,
					Message:     err.Error(),
				})
				return
			}
			b.broadcastSessionsList()
		}()

	case MsgResumeSession:
		var p ResumeSessionPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		go func() {
			if err := b.manager.ResumeSession(ctx, p.SessionID, p.DisplayName); err != nil {
				slog.Warn("resume session failed", "id", p.SessionID, "error", err)
				return
			}
			b.broadcastSessionsList()
		}()

	case MsgCloseSession:
		var p CloseSessionPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		if err := b.manager.CloseSession(p.SessionName); err != nil {
			slog.Warn("close session failed", "name", p.SessionName, "error", err)
			return
		}
		b.broadcastSessionsList()

	case MsgGetPersistedSessions:
		b.sendPersistedSessions(client)

	case MsgListWorktrees:
		if b.onListWorktrees != nil {
			wts := b.onListWorktrees()
			b.sendTo(client, MsgWorktreesList, WorktreesListPayload{Worktrees: wts})
		}

	case MsgStartTunnel:
		if b.onStartTunnel != nil {
			b.onStartTunnel()
		}

	case MsgStopTunnel:
		if b.onStopTunnel != nil {
			b.onStopTunnel()
		}

	case MsgSetTunnelConfig:
		var p SetTunnelConfigPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		if b.onSetTunnelConfig != nil {
			b.onSetTunnelConfig(p)
		}

	case MsgAddAllowedUser:
		var p AllowedUserPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		if b.onAddAllowedUser != nil {
			b.onAddAllowedUser(p.Email)
			b.broadcastAllowedUsers()
		}

	case MsgRemoveAllowedUser:
		var p AllowedUserPayload
		if err := json.Unmarshal(msg.Payload, &p); err != nil {
			return
		}
		if b.onRemoveAllowedUser != nil {
			b.onRemoveAllowedUser(p.Email)
			b.broadcastAllowedUsers()
		}

	case MsgGetAllowedUsers:
		b.sendAllowedUsers(client)
	}
}

// --- Event handler ---

func (b *Bridge) onSessionEvent(evt copilot.SessionEvent) {
	sn := evt.SessionName
	switch evt.Type {
	case copilot.EventContentDelta:
		b.broadcastFiltered(MsgContentDelta, ContentDeltaPayload{
			SessionName: sn,
			Content:     deref(evt.Data.Content),
		}, sn)
	case copilot.EventToolStart:
		b.broadcastFiltered(MsgToolStarted, ToolStartedPayload{
			SessionName: sn,
			ToolName:    deref(evt.Data.ToolName),
			CallID:      deref(evt.Data.ToolCallID),
			ToolInput:   deref(evt.Data.ToolInput),
		}, sn)
	case copilot.EventToolComplete:
		b.broadcastFiltered(MsgToolCompleted, ToolCompletedPayload{
			SessionName: sn,
			CallID:      deref(evt.Data.ToolCallID),
			Result:      deref(evt.Data.ToolResult),
			Success:     derefBool(evt.Data.ToolSuccess),
		}, sn)
	case copilot.EventIntentChanged:
		b.broadcastFiltered(MsgIntentChanged, IntentChangedPayload{
			SessionName: sn,
			Intent:      deref(evt.Data.Intent),
		}, sn)
	case copilot.EventTurnStart:
		b.broadcastFiltered(MsgTurnStart, TurnPayload{SessionName: sn}, sn)
		b.broadcastSessionsList()
	case copilot.EventTurnEnd:
		b.broadcastFiltered(MsgTurnEnd, TurnPayload{SessionName: sn}, sn)
		b.broadcastSessionsList()
	case copilot.EventSessionError:
		b.broadcastFiltered(MsgSessionError, ErrorPayload{
			SessionName: sn,
			Message:     deref(evt.Data.ErrorMessage),
		}, sn)
		b.broadcastSessionsList()
	case copilot.EventReasoningDelta:
		b.broadcastFiltered(MsgReasoningDelta, ReasoningDeltaPayload{
			SessionName: sn,
			ReasoningID: deref(evt.Data.ReasoningID),
			Content:     deref(evt.Data.Content),
		}, sn)
	case copilot.EventUserMessage:
		b.broadcastFiltered(MsgUserMessage, ContentDeltaPayload{
			SessionName: sn,
			Content:     deref(evt.Data.Content),
		}, sn)
	case copilot.EventSessionIdle:
		b.broadcastSessionsList()
	}
}

// --- Send helpers ---

func (b *Bridge) sendSessionsList(client *wsClient) {
	sessions := b.manager.ListSessions()
	summaries := make([]SessionSummary, len(sessions))
	for i, s := range sessions {
		summaries[i] = SessionSummary{
			Name:         s.Name,
			Model:        s.Model,
			SessionID:    s.SessionID,
			WorkingDir:   s.WorkingDir,
			CreatedAt:    s.CreatedAt,
			MessageCount: s.MessageCount,
			IsProcessing: s.State == copilot.StateProcessing,
			Intent:       s.Intent,
			State:        string(s.State),
		}
	}
	b.sendTo(client, MsgSessionsList, SessionsListPayload{
		Sessions:      summaries,
		ActiveSession: b.manager.ActiveSessionName(),
	})
}

func (b *Bridge) sendPersistedSessions(client *wsClient) {
	persisted := b.manager.ListPersistedSessions()
	summaries := make([]PersistedSessionSummary, len(persisted))
	for i, p := range persisted {
		summaries[i] = PersistedSessionSummary{
			SessionID:    p.SessionID,
			Summary:      p.Summary,
			LastModified: p.LastModified.Format("2006-01-02T15:04:05Z"),
			CreatedAt:    p.CreatedAt,
			UpdatedAt:    p.UpdatedAt,
		}
	}
	b.sendTo(client, MsgPersistedSessionsList, PersistedSessionsListPayload{
		Sessions: summaries,
	})
}

func (b *Bridge) broadcastPersistedSessions() {
	persisted := b.manager.ListPersistedSessions()
	summaries := make([]PersistedSessionSummary, len(persisted))
	for i, p := range persisted {
		summaries[i] = PersistedSessionSummary{
			SessionID:    p.SessionID,
			Summary:      p.Summary,
			LastModified: p.LastModified.Format("2006-01-02T15:04:05Z"),
			CreatedAt:    p.CreatedAt,
			UpdatedAt:    p.UpdatedAt,
		}
	}
	b.broadcast(MsgPersistedSessionsList, PersistedSessionsListPayload{
		Sessions: summaries,
	})
}

func (b *Bridge) sendAllowedUsers(client *wsClient) {
	if b.onGetAllowedUsers != nil {
		b.sendTo(client, MsgAllowedUsersList, b.onGetAllowedUsers())
	}
}

func (b *Bridge) broadcastAllowedUsers() {
	if b.onGetAllowedUsers != nil {
		b.broadcast(MsgAllowedUsersList, b.onGetAllowedUsers())
	}
}

func (b *Bridge) broadcastSessionsList() {
	sessions := b.manager.ListSessions()
	summaries := make([]SessionSummary, len(sessions))
	for i, s := range sessions {
		summaries[i] = SessionSummary{
			Name:         s.Name,
			Model:        s.Model,
			SessionID:    s.SessionID,
			WorkingDir:   s.WorkingDir,
			CreatedAt:    s.CreatedAt,
			MessageCount: s.MessageCount,
			IsProcessing: s.State == copilot.StateProcessing,
			Intent:       s.Intent,
			State:        string(s.State),
		}
	}
	b.broadcast(MsgSessionsList, SessionsListPayload{
		Sessions:      summaries,
		ActiveSession: b.manager.ActiveSessionName(),
	})
}

func (b *Bridge) broadcast(msgType string, payload any) {
	b.broadcastFiltered(msgType, payload, "")
}

// broadcastFiltered sends a message to all clients. If sessionName is non-empty,
// shared clients that are filtering on a different session are skipped.
func (b *Bridge) broadcastFiltered(msgType string, payload any, sessionName string) {
	data, err := json.Marshal(BridgeMessage{
		Type:    msgType,
		Payload: mustMarshal(payload),
	})
	if err != nil {
		return
	}

	b.mu.RLock()
	clients := make([]*wsClient, 0, len(b.clients))
	for _, c := range b.clients {
		// Skip shared clients watching a different session.
		if c.sessionFilter != "" && sessionName != "" && c.sessionFilter != sessionName {
			continue
		}
		clients = append(clients, c)
	}
	b.mu.RUnlock()

	for _, c := range clients {
		c.mu.Lock()
		_ = c.conn.Write(c.ctx, websocket.MessageText, data)
		c.mu.Unlock()
	}
}

func (b *Bridge) sendTo(client *wsClient, msgType string, payload any) {
	data, err := json.Marshal(BridgeMessage{
		Type:    msgType,
		Payload: mustMarshal(payload),
	})
	if err != nil {
		return
	}
	client.mu.Lock()
	_ = client.conn.Write(client.ctx, websocket.MessageText, data)
	client.mu.Unlock()
}

// BroadcastTunnelStatus sends tunnel status to all clients.
func (b *Bridge) BroadcastTunnelStatus(running bool, url string) {
	b.broadcast(MsgTunnelStatus, TunnelStatusPayload{Running: running, URL: url})
}

// BroadcastWorktrees sends the worktrees list to all clients.
func (b *Bridge) BroadcastWorktrees(worktrees []WorktreeSummary) {
	b.broadcast(MsgWorktreesList, WorktreesListPayload{Worktrees: worktrees})
}

// SendWorktreesTo sends the worktrees list to a single client.
func (b *Bridge) SendWorktreesTo(client *wsClient, worktrees []WorktreeSummary) {
	b.sendTo(client, MsgWorktreesList, WorktreesListPayload{Worktrees: worktrees})
}

func mustMarshal(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func deref(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

func derefBool(b *bool) bool {
	if b != nil {
		return *b
	}
	return false
}
