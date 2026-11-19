package core

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golem-mc/golem/plugin"
	"github.com/google/uuid"
)

// ActionTargetType matches net.luckperms.api.actionlog.Action.Target.Type.
type ActionTargetType string

const (
	TargetUser  ActionTargetType = "USER"
	TargetGroup ActionTargetType = "GROUP"
	TargetTrack ActionTargetType = "TRACK"
)

// ConsoleUUID is the synthetic UUID used for console actions, matching LuckPerms Sender.CONSOLE_UUID.
var ConsoleUUID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

// Action represents a logged LuckPerms administrative action.
type Action struct {
	Timestamp   time.Time        `json:"timestamp"`
	SourceUUID  plugin.UUID      `json:"source_uuid"`
	SourceName  string           `json:"source_name"`
	TargetType  ActionTargetType `json:"target_type"`
	TargetUUID  *plugin.UUID     `json:"target_uuid,omitempty"`
	TargetName  string           `json:"target_name"`
	Description string           `json:"description"`
}

// FormatLine formats an action for display in logs and notifications.
func (a Action) FormatLine() string {
	return fmt.Sprintf("(%s) [%s] (%s) %s", a.SourceName, string(a.TargetType)[:1], a.TargetName, a.Description)
}

// ActionLogger persists actions and broadcasts live log notifications.
type ActionLogger struct {
	lp *LuckPerms

	mu          sync.RWMutex
	notifyMuted map[plugin.UUID]bool // players who toggled `/lp log notify off`
}

func newActionLogger(lp *LuckPerms) *ActionLogger {
	return &ActionLogger{
		lp:          lp,
		notifyMuted: make(map[plugin.UUID]bool),
	}
}

// SetNotifyMuted toggles log notifications for a player.
func (l *ActionLogger) SetNotifyMuted(id plugin.UUID, muted bool) {
	l.mu.Lock()
	if muted {
		l.notifyMuted[id] = true
	} else {
		delete(l.notifyMuted, id)
	}
	l.mu.Unlock()
}

// IsNotifyMuted reports whether id muted notifications.
func (l *ActionLogger) IsNotifyMuted(id plugin.UUID) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.notifyMuted[id]
}

// Submit logs an action to storage and broadcasts a notification if enabled.
func (l *ActionLogger) Submit(sender plugin.CommandSender, targetType ActionTargetType, targetUUID *plugin.UUID, targetName, desc string) {
	srcUUID := ConsoleUUID
	srcName := "Console"
	if sender != nil {
		srcName = sender.Name()
		if p, ok := sender.(plugin.Player); ok {
			srcUUID = p.UUID()
		}
	}
	a := Action{
		Timestamp:   time.Now().UTC(),
		SourceUUID:  srcUUID,
		SourceName:  srcName,
		TargetType:  targetType,
		TargetUUID:  targetUUID,
		TargetName:  targetName,
		Description: desc,
	}
	if err := l.lp.Storage.LogAction(a); err != nil {
		l.lp.Log.Warn("failed to log action", "err", err)
	}
	l.broadcast(a, sender)
	if l.lp.Messaging != nil && l.lp.Config().PushLogEntries {
		go func() { _ = l.lp.Messaging.PushLog(a) }()
	}
}

// BroadcastRemote broadcasts a log entry received over the messaging service to online staff.
func (l *ActionLogger) BroadcastRemote(a Action) {
	l.broadcast(a, nil)
}

func (l *ActionLogger) broadcast(a Action, exclude plugin.CommandSender) {
	cfg := l.lp.Config()
	if !cfg.LogNotify || l.lp.Server == nil {
		return
	}
	for _, rx := range cfg.LogNotifyFilterRegex {
		if rx.MatchString(a.Description) {
			return
		}
	}
	msg := LPPrefix().Append(
		plugin.Plain("LOG > ").Color("dark_aqua").Bold(),
		plugin.Plain("("+a.SourceName+") ").Color("aqua"),
		plugin.Plain("["+string(a.TargetType)[:1]+"] ").Color("dark_green"),
		plugin.Plain("("+a.TargetName+") ").Color("green"),
		plugin.Plain(a.Description).Color("gray"),
	)
	for _, p := range l.lp.Server.Players() {
		if exclude != nil {
			if ep, ok := exclude.(plugin.Player); ok && ep.UUID() == p.UUID() {
				continue
			}
		}
		if l.IsNotifyMuted(p.UUID()) {
			continue
		}
		if p.HasPermission("luckperms.log.notify") {
			p.SendMessage(msg)
		}
	}
}

// FilterActions filters actions according to pred.
func FilterActions(actions []Action, pred func(Action) bool) []Action {
	var out []Action
	for _, a := range actions {
		if pred(a) {
			out = append(out, a)
		}
	}
	return out
}

// ActionMatchesQuery reports whether a matches a case-insensitive substring query.
func ActionMatchesQuery(a Action, q string) bool {
	lq := strings.ToLower(q)
	return strings.Contains(strings.ToLower(a.SourceName), lq) ||
		strings.Contains(strings.ToLower(a.TargetName), lq) ||
		strings.Contains(strings.ToLower(a.Description), lq)
}
