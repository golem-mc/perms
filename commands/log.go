package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golem-mc/golem/plugin"
	"github.com/golem-mc/perms/core"
)

func (m *Manager) cmdLog(sender plugin.CommandSender, label string, args []string) int {
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s log <recent|userhistory|grouphistory|trackhistory|search|notify>", label))
		return 0
	}
	sub := strings.ToLower(args[0])
	rest := args[1:]

	switch sub {
	case "notify":
		if !m.requirePerm(sender, core.PermLogNotify) {
			return 0
		}
		p, ok := sender.(plugin.Player)
		if !ok {
			core.SendError(sender, "Log notifications toggle is for players only.")
			return 0
		}
		if len(rest) < 1 {
			state := "enabled"
			if m.lp.Actions.IsNotifyMuted(p.UUID()) {
				state = "disabled"
			}
			core.SendInfo(sender, "Log notifications are currently "+state+".")
			return 1
		}
		switch strings.ToLower(rest[0]) {
		case "on", "true":
			m.lp.Actions.SetNotifyMuted(p.UUID(), false)
			core.SendInfo(sender, "Log notifications enabled.")
			return 1
		case "off", "false":
			m.lp.Actions.SetNotifyMuted(p.UUID(), true)
			core.SendInfo(sender, "Log notifications disabled.")
			return 1
		default:
			core.SendError(sender, fmt.Sprintf("Usage: /%s log notify <on|off>", label))
			return 0
		}

	case "recent":
		if !m.requirePerm(sender, core.PermLogRecent) {
			return 0
		}
		entries, err := m.lp.Storage.GetLogEntries()
		if err != nil {
			core.SendError(sender, "Failed to read logs: "+err.Error())
			return 0
		}
		page := 1
		title := "Recent Actions"
		if len(rest) >= 1 {
			if n, err := strconv.Atoi(rest[0]); err == nil {
				page = n
			} else {
				actor := rest[0]
				title = "Recent Actions by " + actor
				entries = core.FilterActions(entries, func(a core.Action) bool {
					return strings.EqualFold(a.SourceName, actor) || strings.EqualFold(a.SourceUUID.String(), actor)
				})
				page = parsePage(rest, 1)
			}
		}
		return m.showLogPage(sender, title, entries, page)

	case "userhistory":
		if !m.requirePerm(sender, core.PermLogUserHistory) {
			return 0
		}
		if len(rest) < 1 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s log userhistory <user> [page]", label))
			return 0
		}
		target := rest[0]
		page := parsePage(rest, 1)
		id, _, hasID := m.lp.ResolveUserUUID(target)
		entries, err := m.lp.Storage.GetLogEntries()
		if err != nil {
			core.SendError(sender, "Failed to read logs: "+err.Error())
			return 0
		}
		filtered := core.FilterActions(entries, func(a core.Action) bool {
			if a.TargetType != core.TargetUser {
				return false
			}
			if hasID && a.TargetUUID != nil && *a.TargetUUID == id {
				return true
			}
			return strings.EqualFold(a.TargetName, target)
		})
		return m.showLogPage(sender, "User History: "+target, filtered, page)

	case "grouphistory":
		if !m.requirePerm(sender, core.PermLogGroupHistory) {
			return 0
		}
		if len(rest) < 1 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s log grouphistory <group> [page]", label))
			return 0
		}
		group := strings.ToLower(rest[0])
		page := parsePage(rest, 1)
		entries, err := m.lp.Storage.GetLogEntries()
		if err != nil {
			core.SendError(sender, "Failed to read logs: "+err.Error())
			return 0
		}
		filtered := core.FilterActions(entries, func(a core.Action) bool {
			return a.TargetType == core.TargetGroup && strings.EqualFold(a.TargetName, group)
		})
		return m.showLogPage(sender, "Group History: "+group, filtered, page)

	case "trackhistory":
		if !m.requirePerm(sender, core.PermLogTrackHistory) {
			return 0
		}
		if len(rest) < 1 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s log trackhistory <track> [page]", label))
			return 0
		}
		track := strings.ToLower(rest[0])
		page := parsePage(rest, 1)
		entries, err := m.lp.Storage.GetLogEntries()
		if err != nil {
			core.SendError(sender, "Failed to read logs: "+err.Error())
			return 0
		}
		filtered := core.FilterActions(entries, func(a core.Action) bool {
			return a.TargetType == core.TargetTrack && strings.EqualFold(a.TargetName, track)
		})
		return m.showLogPage(sender, "Track History: "+track, filtered, page)

	case "search":
		if !m.requirePerm(sender, core.PermLogSearch) {
			return 0
		}
		if len(rest) < 1 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s log search <query> [page]", label))
			return 0
		}
		query := rest[0]
		page := parsePage(rest, 1)
		entries, err := m.lp.Storage.GetLogEntries()
		if err != nil {
			core.SendError(sender, "Failed to read logs: "+err.Error())
			return 0
		}
		filtered := core.FilterActions(entries, func(a core.Action) bool {
			return core.ActionMatchesQuery(a, query)
		})
		return m.showLogPage(sender, "Log Search: "+query, filtered, page)

	default:
		core.SendError(sender, "Unknown log subcommand: "+sub)
		return 0
	}
}

func (m *Manager) showLogPage(sender plugin.CommandSender, title string, entries []core.Action, page int) int {
	slice, curPage, totalPages := paginate(entries, page, 10)
	core.SendPrefixed(sender,
		plugin.Plain(title+" ").Color("green"),
		plugin.Plain(fmt.Sprintf("(page %d of %d - %d entries)", curPage, totalPages, len(entries))).Color("gray"),
	)
	if len(slice) == 0 {
		sender.SendMessage(plugin.Plain("  No log entries found.").Color("gray"))
		return 1
	}
	for _, a := range slice {
		ago := core.FormatDuration(time.Since(a.Timestamp))
		sender.SendMessage(plugin.Plain("> ").Color("dark_aqua").Append(
			plugin.Plain(ago+" ago ").Color("gray"),
			plugin.Plain(a.FormatLine()).Color("white"),
		))
	}
	return 1
}
