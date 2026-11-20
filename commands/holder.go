package commands

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/golem-mc/golem/plugin"
	"github.com/golem-mc/perms/core"
)

// holderTarget abstracts User and Group for shared permission/parent/meta/clear/showtracks commands.
type holderTarget struct {
	holder     *core.PermissionHolder
	user       *core.User  // non-nil when holder is a user
	group      *core.Group // non-nil when holder is a group
	display    string
	targetType core.ActionTargetType
	targetUUID *plugin.UUID
	save       func() error
}

func (h *holderTarget) isUser() bool { return h.user != nil }

func (h *holderTarget) perm(userPerm, groupPerm core.CommandPerm) core.CommandPerm {
	if h.isUser() {
		return userPerm
	}
	return groupPerm
}

func (h *holderTarget) checkModify(m *Manager, sender plugin.CommandSender) bool {
	if h.isUser() {
		if !m.lp.CheckModifyUserPermission(sender, h.user) {
			core.SendError(sender, "You do not have permission to modify this user.")
			return false
		}
		return true
	}
	if !m.lp.CheckModifyGroupPermission(sender, h.group.Name(), "") {
		core.SendError(sender, "You do not have permission to modify group "+h.group.Name()+".")
		return false
	}
	return true
}

func (h *holderTarget) queryOptions(lp *core.LuckPerms) core.QueryOptions {
	if h.isUser() && lp.Server != nil {
		if p, ok := lp.Server.Player(h.user.UUID()); ok {
			return lp.Contexts.QueryOptionsForPlayer(p)
		}
	}
	return lp.Contexts.StaticQueryOptions()
}

func (h *holderTarget) log(m *Manager, sender plugin.CommandSender, desc string) {
	m.lp.Actions.Submit(sender, h.targetType, h.targetUUID, h.display, desc)
}

// --- Permission subcommands ---

func (m *Manager) cmdHolderPermission(sender plugin.CommandSender, label string, h *holderTarget, args []string) int {
	if len(args) == 0 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s permission <info|set|unset|settemp|unsettemp|check|clear>", label, h.holder.Type(), h.display))
		return 0
	}
	sub := strings.ToLower(args[0])
	rest := args[1:]

	switch sub {
	case "info":
		if !m.requirePerm(sender, h.perm(core.PermUserPermInfo, core.PermGroupPermInfo)) {
			return 0
		}
		page := parsePage(rest, 0)
		var nodes []core.Node
		for _, n := range h.holder.NormalNodes() {
			if n.Type == core.NodeTypePermission || n.Type == core.NodeTypeRegexPermission {
				nodes = append(nodes, n)
			}
		}
		slices.SortStableFunc(nodes, core.CompareNodes)
		slice, curPage, totalPages := paginate(nodes, page, 8)
		core.SendPrefixed(sender,
			plugin.Plain(h.display).Color("aqua"),
			plugin.Plain("'s Permissions: ").Color("green"),
			plugin.Plain(fmt.Sprintf("(page %d of %d - %d entries)", curPage, totalPages, len(nodes))).Color("gray"),
		)
		if len(slice) == 0 {
			sender.SendMessage(plugin.Plain("  None").Color("gray"))
			return 1
		}
		for _, n := range slice {
			line := plugin.Plain("> ").Color("dark_aqua").Append(
				plugin.Plain(n.Key).Color("white"),
				plugin.Plain(" (").Color("gray"),
				core.FormatBool(n.Value),
				plugin.Plain(")").Color("gray"),
				core.FormatContexts(n.Contexts),
			)
			if n.HasExpiry() {
				rem := time.Until(n.Expiry)
				line = line.Append(plugin.Plain(" [expires in " + core.FormatDuration(rem) + "]").Color("yellow"))
			}
			sender.SendMessage(line)
		}
		return 1

	case "set":
		if !m.requirePerm(sender, h.perm(core.PermUserPermSet, core.PermGroupPermSet)) || !h.checkModify(m, sender) {
			return 0
		}
		if len(rest) < 1 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s permission set <node> [true|false] [context...]", label, h.holder.Type(), h.display))
			return 0
		}
		nodeKey := rest[0]
		if !m.lp.CheckModifyNodePermission(sender, nodeKey) {
			core.SendError(sender, "You do not have permission to modify node "+nodeKey+".")
			return 0
		}
		val := true
		ctxIdx := 1
		if len(rest) >= 2 && (strings.EqualFold(rest[1], "true") || strings.EqualFold(rest[1], "false")) {
			val = strings.EqualFold(rest[1], "true")
			ctxIdx = 2
		}
		contexts := parseContextsOrDefault(m.lp, rest[ctxIdx:])
		node := core.ParseNode(nodeKey, val).WithContexts(contexts)
		res := h.holder.SetNode(core.DataNormal, node)
		if !res.WasSuccessful() {
			core.SendError(sender, fmt.Sprintf("%s already has %s set to %v%s.", h.display, node.Key, val, describeContextSuffix(contexts)))
			return 0
		}
		if err := h.save(); err != nil {
			core.SendError(sender, "Failed to save data: "+err.Error())
			return 0
		}
		h.log(m, sender, fmt.Sprintf("permission set %s %v%s", node.Key, val, describeContextSuffix(contexts)))
		core.SendPrefixed(sender,
			plugin.Plain("Set ").Color("green"),
			plugin.Plain(node.Key).Color("aqua"),
			plugin.Plain(" to ").Color("green"),
			core.FormatBool(val),
			plugin.Plain(" for ").Color("green"),
			plugin.Plain(h.display).Color("aqua"),
			core.FormatContexts(contexts),
			plugin.Plain(".").Color("green"),
		)
		return 1

	case "unset":
		if !m.requirePerm(sender, h.perm(core.PermUserPermUnset, core.PermGroupPermUnset)) || !h.checkModify(m, sender) {
			return 0
		}
		if len(rest) < 1 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s permission unset <node> [context...]", label, h.holder.Type(), h.display))
			return 0
		}
		nodeKey := rest[0]
		if !m.lp.CheckModifyNodePermission(sender, nodeKey) {
			core.SendError(sender, "You do not have permission to modify node "+nodeKey+".")
			return 0
		}
		contexts := parseContextsOrDefault(m.lp, rest[1:])
		node := core.ParseNode(nodeKey, true).WithContexts(contexts)
		res := h.holder.UnsetNode(core.DataNormal, node)
		if !res.WasSuccessful() {
			core.SendError(sender, fmt.Sprintf("%s does not have %s set%s.", h.display, node.Key, describeContextSuffix(contexts)))
			return 0
		}
		if err := h.save(); err != nil {
			core.SendError(sender, "Failed to save data: "+err.Error())
			return 0
		}
		h.log(m, sender, fmt.Sprintf("permission unset %s%s", node.Key, describeContextSuffix(contexts)))
		core.SendPrefixed(sender,
			plugin.Plain("Unset ").Color("green"),
			plugin.Plain(node.Key).Color("aqua"),
			plugin.Plain(" for ").Color("green"),
			plugin.Plain(h.display).Color("aqua"),
			core.FormatContexts(contexts),
			plugin.Plain(".").Color("green"),
		)
		return 1

	case "settemp":
		if !m.requirePerm(sender, h.perm(core.PermUserPermSetTemp, core.PermGroupPermSetTemp)) || !h.checkModify(m, sender) {
			return 0
		}
		if len(rest) < 2 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s permission settemp <node> [true|false] <duration> [accumulate|replace|deny] [context...]", label, h.holder.Type(), h.display))
			return 0
		}
		nodeKey := rest[0]
		if !m.lp.CheckModifyNodePermission(sender, nodeKey) {
			core.SendError(sender, "You do not have permission to modify node "+nodeKey+".")
			return 0
		}
		val := true
		idx := 1
		if len(rest) > idx && (strings.EqualFold(rest[idx], "true") || strings.EqualFold(rest[idx], "false")) {
			val = strings.EqualFold(rest[idx], "true")
			idx++
		}
		if idx >= len(rest) {
			core.SendError(sender, "Missing duration argument.")
			return 0
		}
		dur, err := core.ParseDuration(rest[idx])
		if err != nil {
			core.SendError(sender, "Invalid duration: "+rest[idx])
			return 0
		}
		idx++
		strategy := m.lp.Config().TemporaryAddBehaviour
		if idx < len(rest) {
			if mod, ok := parseTempModifier(rest[idx]); ok {
				strategy = mod
				idx++
			}
		}
		contexts := parseContextsOrDefault(m.lp, rest[idx:])
		node := core.ParseNode(nodeKey, val).WithExpiry(time.Now().Add(dur)).WithContexts(contexts)
		stored, res := h.holder.SetNodeTemp(core.DataNormal, node, strategy)
		if !res.WasSuccessful() {
			core.SendError(sender, fmt.Sprintf("%s already has temporary permission %s%s.", h.display, node.Key, describeContextSuffix(contexts)))
			return 0
		}
		if err := h.save(); err != nil {
			core.SendError(sender, "Failed to save data: "+err.Error())
			return 0
		}
		remStr := core.FormatDuration(time.Until(stored.Expiry))
		h.log(m, sender, fmt.Sprintf("permission settemp %s %v %s%s", node.Key, val, remStr, describeContextSuffix(contexts)))
		core.SendPrefixed(sender,
			plugin.Plain("Set ").Color("green"),
			plugin.Plain(node.Key).Color("aqua"),
			plugin.Plain(" to ").Color("green"),
			core.FormatBool(val),
			plugin.Plain(" for ").Color("green"),
			plugin.Plain(h.display).Color("aqua"),
			plugin.Plain(" for a duration of ").Color("green"),
			plugin.Plain(remStr).Color("aqua"),
			core.FormatContexts(contexts),
			plugin.Plain(".").Color("green"),
		)
		return 1

	case "unsettemp":
		if !m.requirePerm(sender, h.perm(core.PermUserPermUnsetTemp, core.PermGroupPermUnsetTemp)) || !h.checkModify(m, sender) {
			return 0
		}
		if len(rest) < 1 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s permission unsettemp <node> [duration] [context...]", label, h.holder.Type(), h.display))
			return 0
		}
		nodeKey := rest[0]
		idx := 1
		var subtract time.Duration
		if idx < len(rest) {
			if d, err := core.ParseDuration(rest[idx]); err == nil {
				subtract = d
				idx++
			}
		}
		contexts := parseContextsOrDefault(m.lp, rest[idx:])
		node := core.ParseNode(nodeKey, true).WithContexts(contexts)
		updated, removed, res := h.holder.UnsetNodeTemp(core.DataNormal, node, subtract)
		if !res.WasSuccessful() {
			core.SendError(sender, fmt.Sprintf("%s does not have temporary permission %s%s.", h.display, node.Key, describeContextSuffix(contexts)))
			return 0
		}
		if err := h.save(); err != nil {
			core.SendError(sender, "Failed to save data: "+err.Error())
			return 0
		}
		if removed {
			h.log(m, sender, fmt.Sprintf("permission unsettemp %s%s", node.Key, describeContextSuffix(contexts)))
			core.SendInfo(sender, fmt.Sprintf("Unset temporary permission %s for %s%s.", node.Key, h.display, describeContextSuffix(contexts)))
		} else {
			remStr := core.FormatDuration(time.Until(updated.Expiry))
			h.log(m, sender, fmt.Sprintf("permission unsettemp %s %s%s", node.Key, core.FormatDuration(subtract), describeContextSuffix(contexts)))
			core.SendInfo(sender, fmt.Sprintf("Subtracted %s from temporary permission %s for %s (%s remaining)%s.", core.FormatDuration(subtract), node.Key, h.display, remStr, describeContextSuffix(contexts)))
		}
		return 1

	case "check":
		if !m.requirePerm(sender, h.perm(core.PermUserPermCheck, core.PermGroupPermCheck)) {
			return 0
		}
		if len(rest) < 1 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s permission check <node> [context...]", label, h.holder.Type(), h.display))
			return 0
		}
		nodeKey := rest[0]
		qo := h.queryOptions(m.lp)
		if len(rest) > 1 {
			qo.Contexts = core.ParseContextSet(rest[1:])
		}
		res := h.holder.CachedData().PermissionData(qo).CheckPermission(nodeKey, core.OriginInternal)
		msg := core.Prefixed(
			plugin.Plain(h.display).Color("aqua"),
			plugin.Plain(" has permission ").Color("green"),
			plugin.Plain(nodeKey).Color("aqua"),
			plugin.Plain(" set to ").Color("green"),
			core.FormatTristate(res.Result),
			plugin.Plain(" in context ").Color("green"),
			plugin.Plain(qo.Contexts.String()).Color("aqua"),
			plugin.Plain(".").Color("green"),
		)
		sender.SendMessage(msg)
		if res.Processor != "" {
			extra := "  Processor: " + res.Processor
			if res.Node != nil {
				extra += " | Cause: " + res.Node.Key
				if res.Node.Origin.Name != "" {
					extra += fmt.Sprintf(" (inherited from %s %s)", res.Node.Origin.Type, res.Node.Origin.Name)
				}
			}
			sender.SendMessage(plugin.Plain(extra).Color("gray"))
		}
		return 1

	case "clear":
		if !m.requirePerm(sender, h.perm(core.PermUserPermClear, core.PermGroupPermClear)) || !h.checkModify(m, sender) {
			return 0
		}
		var contexts core.ContextSet
		filterCtx := len(rest) > 0
		if filterCtx {
			contexts = core.ParseContextSet(rest)
		}
		removed := h.holder.ClearNodes(core.DataNormal, func(n core.Node) bool {
			if n.Type != core.NodeTypePermission && n.Type != core.NodeTypeRegexPermission {
				return false
			}
			return !filterCtx || n.Contexts.Equal(contexts)
		})
		if err := h.save(); err != nil {
			core.SendError(sender, "Failed to save data: "+err.Error())
			return 0
		}
		h.log(m, sender, fmt.Sprintf("permission clear%s", describeContextSuffix(contexts)))
		core.SendInfo(sender, fmt.Sprintf("Cleared %d permission node(s) for %s%s.", removed, h.display, describeContextSuffix(contexts)))
		return 1

	default:
		core.SendError(sender, "Unknown permission subcommand: "+sub)
		return 0
	}
}

// --- Parent subcommands ---

func (m *Manager) cmdHolderParent(sender plugin.CommandSender, label string, h *holderTarget, args []string) int {
	if len(args) == 0 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s parent <info|set|add|remove|settrack|addtemp|removetemp|clear|cleartrack>", label, h.holder.Type(), h.display))
		return 0
	}
	sub := strings.ToLower(args[0])
	rest := args[1:]

	switch sub {
	case "info":
		return m.cmdHolderParentInfo(sender, label, h, rest)
	case "add":
		return m.cmdHolderParentAdd(sender, label, h, rest)
	case "set":
		return m.cmdHolderParentSet(sender, label, h, rest)
	case "remove":
		return m.cmdHolderParentRemove(sender, label, h, rest)
	case "addtemp":
		return m.cmdHolderParentAddtemp(sender, label, h, rest)
	case "removetemp":
		return m.cmdHolderParentRemovetemp(sender, label, h, rest)
	case "settrack":
		return m.cmdHolderParentSettrack(sender, label, h, rest)
	case "clear":
		return m.cmdHolderParentClear(sender, label, h, rest)
	case "cleartrack":
		return m.cmdHolderParentCleartrack(sender, label, h, rest)
	case "switchprimarygroup":
		return m.cmdHolderParentSwitchprimarygroup(sender, label, h, rest)
	default:
		core.SendError(sender, "Unknown parent subcommand: "+sub)
		return 0
	}
}

func (m *Manager) cmdHolderParentInfo(sender plugin.CommandSender, label string, h *holderTarget, rest []string) int {
	if !m.requirePerm(sender, h.perm(core.PermUserParentInfo, core.PermGroupParentInfo)) {
		return 0
	}
	page := parsePage(rest, 0)
	var nodes []core.Node
	for _, n := range h.holder.NormalNodes() {
		if n.Type == core.NodeTypeInheritance {
			nodes = append(nodes, n)
		}
	}
	slices.SortStableFunc(nodes, core.CompareNodes)
	slice, curPage, totalPages := paginate(nodes, page, 8)
	core.SendPrefixed(sender,
		plugin.Plain(h.display).Color("aqua"),
		plugin.Plain("'s Parent Groups: ").Color("green"),
		plugin.Plain(fmt.Sprintf("(page %d of %d - %d entries)", curPage, totalPages, len(nodes))).Color("gray"),
	)
	if len(slice) == 0 {
		sender.SendMessage(plugin.Plain("  None").Color("gray"))
		return 1
	}
	for _, n := range slice {
		line := plugin.Plain("> ").Color("dark_aqua").Append(
			plugin.Plain(n.GroupName).Color("aqua"),
			core.FormatContexts(n.Contexts),
		)
		if n.HasExpiry() {
			line = line.Append(plugin.Plain(" [expires in " + core.FormatDuration(time.Until(n.Expiry)) + "]").Color("yellow"))
		}
		sender.SendMessage(line)
	}
	return 1
}

func (m *Manager) cmdHolderParentAdd(sender plugin.CommandSender, label string, h *holderTarget, rest []string) int {
	if !m.requirePerm(sender, h.perm(core.PermUserParentAdd, core.PermGroupParentAdd)) || !h.checkModify(m, sender) {
		return 0
	}
	if len(rest) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s parent add <group> [context...]", label, h.holder.Type(), h.display))
		return 0
	}
	groupName := strings.ToLower(rest[0])
	if m.lp.Groups.GetIfLoaded(groupName) == nil {
		core.SendError(sender, "Group "+groupName+" does not exist.")
		return 0
	}
	if !m.lp.CheckModifyGroupPermission(sender, groupName, "parent.add") {
		core.SendError(sender, "You do not have permission to add parent group "+groupName+".")
		return 0
	}
	if !h.isUser() && h.group.Name() == groupName {
		core.SendError(sender, "A group cannot inherit from itself.")
		return 0
	}
	contexts := parseContextsOrDefault(m.lp, rest[1:])
	node := core.NewInheritanceNode(groupName).WithContexts(contexts)
	if res := h.holder.SetNode(core.DataNormal, node); !res.WasSuccessful() {
		core.SendError(sender, fmt.Sprintf("%s already inherits from %s%s.", h.display, groupName, describeContextSuffix(contexts)))
		return 0
	}
	if err := h.save(); err != nil {
		core.SendError(sender, "Failed to save data: "+err.Error())
		return 0
	}
	h.log(m, sender, fmt.Sprintf("parent add %s%s", groupName, describeContextSuffix(contexts)))
	core.SendInfo(sender, fmt.Sprintf("%s now inherits permissions from %s%s.", h.display, groupName, describeContextSuffix(contexts)))
	return 1
}

func (m *Manager) cmdHolderParentSet(sender plugin.CommandSender, label string, h *holderTarget, rest []string) int {
	if !m.requirePerm(sender, h.perm(core.PermUserParentSet, core.PermGroupParentSet)) || !h.checkModify(m, sender) {
		return 0
	}
	if len(rest) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s parent set <group> [context...]", label, h.holder.Type(), h.display))
		return 0
	}
	groupName := strings.ToLower(rest[0])
	if m.lp.Groups.GetIfLoaded(groupName) == nil {
		core.SendError(sender, "Group "+groupName+" does not exist.")
		return 0
	}
	if !m.lp.CheckModifyGroupPermission(sender, groupName, "parent.set") {
		core.SendError(sender, "You do not have permission to set parent group "+groupName+".")
		return 0
	}
	if !h.isUser() && h.group.Name() == groupName {
		core.SendError(sender, "A group cannot inherit from itself.")
		return 0
	}
	contexts := parseContextsOrDefault(m.lp, rest[1:])
	h.holder.ClearNodes(core.DataNormal, func(n core.Node) bool {
		return n.Type == core.NodeTypeInheritance && n.Contexts.Equal(contexts)
	})
	h.holder.SetNode(core.DataNormal, core.NewInheritanceNode(groupName).WithContexts(contexts))
	if h.isUser() && contexts.IsEmpty() {
		h.user.SetStoredPrimaryGroup(groupName)
	}
	if err := h.save(); err != nil {
		core.SendError(sender, "Failed to save data: "+err.Error())
		return 0
	}
	h.log(m, sender, fmt.Sprintf("parent set %s%s", groupName, describeContextSuffix(contexts)))
	core.SendInfo(sender, fmt.Sprintf("%s had their existing parent groups cleared, and now only inherits %s%s.", h.display, groupName, describeContextSuffix(contexts)))
	return 1
}

func (m *Manager) cmdHolderParentRemove(sender plugin.CommandSender, label string, h *holderTarget, rest []string) int {
	if !m.requirePerm(sender, h.perm(core.PermUserParentRemove, core.PermGroupParentRemove)) || !h.checkModify(m, sender) {
		return 0
	}
	if len(rest) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s parent remove <group> [context...]", label, h.holder.Type(), h.display))
		return 0
	}
	groupName := strings.ToLower(rest[0])
	contexts := parseContextsOrDefault(m.lp, rest[1:])
	if h.isUser() && contexts.IsEmpty() && m.lp.Config().PreventPrimaryGroupRemoval && strings.EqualFold(h.user.StoredPrimaryGroup(), groupName) {
		core.SendError(sender, fmt.Sprintf("Cannot remove %s's primary group %s.", h.display, groupName))
		return 0
	}
	node := core.NewInheritanceNode(groupName).WithContexts(contexts)
	if res := h.holder.UnsetNode(core.DataNormal, node); !res.WasSuccessful() {
		core.SendError(sender, fmt.Sprintf("%s does not inherit from %s%s.", h.display, groupName, describeContextSuffix(contexts)))
		return 0
	}
	if h.isUser() {
		h.user.GiveDefaultIfNeeded()
		if contexts.IsEmpty() && strings.EqualFold(h.user.StoredPrimaryGroup(), groupName) {
			h.user.SetStoredPrimaryGroup(core.DefaultGroupName)
		}
	}
	if err := h.save(); err != nil {
		core.SendError(sender, "Failed to save data: "+err.Error())
		return 0
	}
	h.log(m, sender, fmt.Sprintf("parent remove %s%s", groupName, describeContextSuffix(contexts)))
	core.SendInfo(sender, fmt.Sprintf("%s no longer inherits permissions from %s%s.", h.display, groupName, describeContextSuffix(contexts)))
	return 1
}

func (m *Manager) cmdHolderParentAddtemp(sender plugin.CommandSender, label string, h *holderTarget, rest []string) int {
	if !m.requirePerm(sender, h.perm(core.PermUserParentAddTemp, core.PermGroupParentAddTemp)) || !h.checkModify(m, sender) {
		return 0
	}
	if len(rest) < 2 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s parent addtemp <group> <duration> [accumulate|replace|deny] [context...]", label, h.holder.Type(), h.display))
		return 0
	}
	groupName := strings.ToLower(rest[0])
	if m.lp.Groups.GetIfLoaded(groupName) == nil {
		core.SendError(sender, "Group "+groupName+" does not exist.")
		return 0
	}
	dur, err := core.ParseDuration(rest[1])
	if err != nil {
		core.SendError(sender, "Invalid duration: "+rest[1])
		return 0
	}
	idx := 2
	strategy := m.lp.Config().TemporaryAddBehaviour
	if idx < len(rest) {
		if mod, ok := parseTempModifier(rest[idx]); ok {
			strategy = mod
			idx++
		}
	}
	contexts := parseContextsOrDefault(m.lp, rest[idx:])
	node := core.NewInheritanceNode(groupName).WithExpiry(time.Now().Add(dur)).WithContexts(contexts)
	stored, res := h.holder.SetNodeTemp(core.DataNormal, node, strategy)
	if !res.WasSuccessful() {
		core.SendError(sender, fmt.Sprintf("%s already temporarily inherits from %s%s.", h.display, groupName, describeContextSuffix(contexts)))
		return 0
	}
	if err := h.save(); err != nil {
		core.SendError(sender, "Failed to save data: "+err.Error())
		return 0
	}
	remStr := core.FormatDuration(time.Until(stored.Expiry))
	h.log(m, sender, fmt.Sprintf("parent addtemp %s %s%s", groupName, remStr, describeContextSuffix(contexts)))
	core.SendInfo(sender, fmt.Sprintf("%s now inherits permissions from %s for a duration of %s%s.", h.display, groupName, remStr, describeContextSuffix(contexts)))
	return 1
}

func (m *Manager) cmdHolderParentRemovetemp(sender plugin.CommandSender, label string, h *holderTarget, rest []string) int {
	if !m.requirePerm(sender, h.perm(core.PermUserParentRemoveTemp, core.PermGroupParentRemoveTemp)) || !h.checkModify(m, sender) {
		return 0
	}
	if len(rest) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s parent removetemp <group> [duration] [context...]", label, h.holder.Type(), h.display))
		return 0
	}
	groupName := strings.ToLower(rest[0])
	idx := 1
	var subtract time.Duration
	if idx < len(rest) {
		if d, err := core.ParseDuration(rest[idx]); err == nil {
			subtract = d
			idx++
		}
	}
	contexts := parseContextsOrDefault(m.lp, rest[idx:])
	node := core.NewInheritanceNode(groupName).WithContexts(contexts)
	updated, removed, res := h.holder.UnsetNodeTemp(core.DataNormal, node, subtract)
	if !res.WasSuccessful() {
		core.SendError(sender, fmt.Sprintf("%s does not temporarily inherit from %s%s.", h.display, groupName, describeContextSuffix(contexts)))
		return 0
	}
	if err := h.save(); err != nil {
		core.SendError(sender, "Failed to save data: "+err.Error())
		return 0
	}
	if removed {
		h.log(m, sender, fmt.Sprintf("parent removetemp %s%s", groupName, describeContextSuffix(contexts)))
		core.SendInfo(sender, fmt.Sprintf("Removed temporary parent %s from %s%s.", groupName, h.display, describeContextSuffix(contexts)))
	} else {
		h.log(m, sender, fmt.Sprintf("parent removetemp %s %s%s", groupName, core.FormatDuration(subtract), describeContextSuffix(contexts)))
		core.SendInfo(sender, fmt.Sprintf("Subtracted %s from temporary parent %s for %s (%s remaining)%s.", core.FormatDuration(subtract), groupName, h.display, core.FormatDuration(time.Until(updated.Expiry)), describeContextSuffix(contexts)))
	}
	return 1
}

func (m *Manager) cmdHolderParentSettrack(sender plugin.CommandSender, label string, h *holderTarget, rest []string) int {
	if !m.requirePerm(sender, h.perm(core.PermUserParentSetTrack, core.PermGroupParentSetTrack)) || !h.checkModify(m, sender) {
		return 0
	}
	if len(rest) < 2 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s parent settrack <track> <group|index> [context...]", label, h.holder.Type(), h.display))
		return 0
	}
	track := m.lp.Tracks.GetIfLoaded(rest[0])
	if track == nil {
		core.SendError(sender, "Track "+rest[0]+" does not exist.")
		return 0
	}
	groupName := strings.ToLower(rest[1])
	if idx, err := strconv.Atoi(groupName); err == nil {
		gs := track.Groups()
		if idx < 1 || idx > len(gs) {
			core.SendError(sender, fmt.Sprintf("Track %s has %d groups.", track.Name(), len(gs)))
			return 0
		}
		groupName = gs[idx-1]
	}
	if !track.ContainsGroup(groupName) {
		core.SendError(sender, fmt.Sprintf("Track %s does not contain group %s.", track.Name(), groupName))
		return 0
	}
	if m.lp.Groups.GetIfLoaded(groupName) == nil {
		core.SendError(sender, "Group "+groupName+" does not exist.")
		return 0
	}
	contexts := parseContextsOrDefault(m.lp, rest[2:])
	h.holder.ClearNodes(core.DataNormal, func(n core.Node) bool {
		return n.Type == core.NodeTypeInheritance && n.Contexts.Equal(contexts) && track.ContainsGroup(n.GroupName)
	})
	h.holder.SetNode(core.DataNormal, core.NewInheritanceNode(groupName).WithContexts(contexts))
	if err := h.save(); err != nil {
		core.SendError(sender, "Failed to save data: "+err.Error())
		return 0
	}
	h.log(m, sender, fmt.Sprintf("parent settrack %s %s%s", track.Name(), groupName, describeContextSuffix(contexts)))
	core.SendInfo(sender, fmt.Sprintf("Set %s to group %s on track %s%s.", h.display, groupName, track.Name(), describeContextSuffix(contexts)))
	return 1
}

func (m *Manager) cmdHolderParentClear(sender plugin.CommandSender, label string, h *holderTarget, rest []string) int {
	if !m.requirePerm(sender, h.perm(core.PermUserParentClear, core.PermGroupParentClear)) || !h.checkModify(m, sender) {
		return 0
	}
	var contexts core.ContextSet
	filterCtx := len(rest) > 0
	if filterCtx {
		contexts = core.ParseContextSet(rest)
	}
	removed := h.holder.ClearNodes(core.DataNormal, func(n core.Node) bool {
		return n.Type == core.NodeTypeInheritance && (!filterCtx || n.Contexts.Equal(contexts))
	})
	if h.isUser() {
		h.user.GiveDefaultIfNeeded()
		if !filterCtx || contexts.IsEmpty() {
			h.user.SetStoredPrimaryGroup(core.DefaultGroupName)
		}
	}
	if err := h.save(); err != nil {
		core.SendError(sender, "Failed to save data: "+err.Error())
		return 0
	}
	h.log(m, sender, fmt.Sprintf("parent clear%s", describeContextSuffix(contexts)))
	core.SendInfo(sender, fmt.Sprintf("Cleared %d parent group(s) for %s%s.", removed, h.display, describeContextSuffix(contexts)))
	return 1
}

func (m *Manager) cmdHolderParentCleartrack(sender plugin.CommandSender, label string, h *holderTarget, rest []string) int {
	if !m.requirePerm(sender, h.perm(core.PermUserParentClearTrack, core.PermGroupParentClearTrack)) || !h.checkModify(m, sender) {
		return 0
	}
	if len(rest) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s parent cleartrack <track> [context...]", label, h.holder.Type(), h.display))
		return 0
	}
	track := m.lp.Tracks.GetIfLoaded(rest[0])
	if track == nil {
		core.SendError(sender, "Track "+rest[0]+" does not exist.")
		return 0
	}
	var contexts core.ContextSet
	filterCtx := len(rest) > 1
	if filterCtx {
		contexts = core.ParseContextSet(rest[1:])
	}
	removed := h.holder.ClearNodes(core.DataNormal, func(n core.Node) bool {
		return n.Type == core.NodeTypeInheritance && track.ContainsGroup(n.GroupName) && (!filterCtx || n.Contexts.Equal(contexts))
	})
	if h.isUser() {
		h.user.GiveDefaultIfNeeded()
	}
	if err := h.save(); err != nil {
		core.SendError(sender, "Failed to save data: "+err.Error())
		return 0
	}
	h.log(m, sender, fmt.Sprintf("parent cleartrack %s%s", track.Name(), describeContextSuffix(contexts)))
	core.SendInfo(sender, fmt.Sprintf("Cleared %d parent group(s) on track %s for %s%s.", removed, track.Name(), h.display, describeContextSuffix(contexts)))
	return 1
}

func (m *Manager) cmdHolderParentSwitchprimarygroup(sender plugin.CommandSender, label string, h *holderTarget, rest []string) int {
	if !h.isUser() {
		core.SendError(sender, "Only users have a primary group.")
		return 0
	}
	if !m.requirePerm(sender, core.PermUserParentSwitchPrimary) || !h.checkModify(m, sender) {
		return 0
	}
	if len(rest) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s user %s parent switchprimarygroup <group>", label, h.display))
		return 0
	}
	groupName := strings.ToLower(rest[0])
	if m.lp.Groups.GetIfLoaded(groupName) == nil {
		core.SendError(sender, "Group "+groupName+" does not exist.")
		return 0
	}
	if strings.EqualFold(h.user.StoredPrimaryGroup(), groupName) {
		core.SendError(sender, fmt.Sprintf("%s already has %s set as their primary group.", h.display, groupName))
		return 0
	}
	// Ensure the user inherits the group.
	h.user.SetNode(core.DataNormal, core.NewInheritanceNode(groupName))
	h.user.SetStoredPrimaryGroup(groupName)
	if err := h.save(); err != nil {
		core.SendError(sender, "Failed to save data: "+err.Error())
		return 0
	}
	h.log(m, sender, "parent switchprimarygroup "+groupName)
	core.SendInfo(sender, fmt.Sprintf("%s's primary group was changed to %s.", h.display, groupName))
	return 1
}

// --- Meta subcommands ---

func (m *Manager) cmdHolderMeta(sender plugin.CommandSender, label string, h *holderTarget, args []string) int {
	if len(args) == 0 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s meta <info|set|unset|settemp|unsettemp|addprefix|addsuffix|setprefix|setsuffix|removeprefix|removesuffix|clear>", label, h.holder.Type(), h.display))
		return 0
	}
	sub := strings.ToLower(args[0])
	rest := args[1:]

	switch sub {
	case "info":
		if !m.requirePerm(sender, h.perm(core.PermUserMetaInfo, core.PermGroupMetaInfo)) {
			return 0
		}
		qo := h.queryOptions(m.lp)
		md := h.holder.CachedData().MetaData(qo)
		core.SendPrefixed(sender, plugin.Plain(h.display).Color("aqua"), plugin.Plain("'s Meta:").Color("green"))

		prefixes := md.Prefixes()
		sender.SendMessage(plugin.Plain("  Prefixes:").Color("dark_aqua"))
		if len(prefixes) == 0 {
			sender.SendMessage(plugin.Plain("    None").Color("gray"))
		} else {
			for _, prio := range core.SortIntMapKeysDesc(prefixes) {
				sender.SendMessage(plugin.Plain(fmt.Sprintf("    - %d: ", prio)).Color("gray").Append(core.ParseLegacyColor(prefixes[prio])))
			}
		}

		suffixes := md.Suffixes()
		sender.SendMessage(plugin.Plain("  Suffixes:").Color("dark_aqua"))
		if len(suffixes) == 0 {
			sender.SendMessage(plugin.Plain("    None").Color("gray"))
		} else {
			for _, prio := range core.SortIntMapKeysDesc(suffixes) {
				sender.SendMessage(plugin.Plain(fmt.Sprintf("    - %d: ", prio)).Color("gray").Append(core.ParseLegacyColor(suffixes[prio])))
			}
		}

		meta := md.Meta()
		sender.SendMessage(plugin.Plain("  Meta:").Color("dark_aqua"))
		if len(meta) == 0 {
			sender.SendMessage(plugin.Plain("    None").Color("gray"))
		} else {
			keys := make([]string, 0, len(meta))
			for k := range meta {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				sender.SendMessage(plugin.Plain(fmt.Sprintf("    - %s = %s", k, strings.Join(meta[k], ", "))).Color("aqua"))
			}
		}
		return 1

	case "set":
		if !m.requirePerm(sender, h.perm(core.PermUserMetaSet, core.PermGroupMetaSet)) || !h.checkModify(m, sender) {
			return 0
		}
		if len(rest) < 2 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s meta set <key> <value> [context...]", label, h.holder.Type(), h.display))
			return 0
		}
		key, val := strings.ToLower(rest[0]), rest[1]
		contexts := parseContextsOrDefault(m.lp, rest[2:])
		h.holder.ClearNodes(core.DataNormal, func(n core.Node) bool {
			return n.Type == core.NodeTypeMeta && n.MetaKey == key && !n.HasExpiry() && n.Contexts.Equal(contexts)
		})
		h.holder.SetNode(core.DataNormal, core.NewMetaNode(key, val).WithContexts(contexts))
		if err := h.save(); err != nil {
			core.SendError(sender, "Failed to save data: "+err.Error())
			return 0
		}
		h.log(m, sender, fmt.Sprintf("meta set %s %s%s", key, val, describeContextSuffix(contexts)))
		core.SendInfo(sender, fmt.Sprintf("Set meta value %s to %q for %s%s.", key, val, h.display, describeContextSuffix(contexts)))
		return 1

	case "unset":
		if !m.requirePerm(sender, h.perm(core.PermUserMetaUnset, core.PermGroupMetaUnset)) || !h.checkModify(m, sender) {
			return 0
		}
		if len(rest) < 1 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s meta unset <key> [context...]", label, h.holder.Type(), h.display))
			return 0
		}
		key := strings.ToLower(rest[0])
		contexts := parseContextsOrDefault(m.lp, rest[1:])
		removed := h.holder.ClearNodes(core.DataNormal, func(n core.Node) bool {
			return n.Type == core.NodeTypeMeta && n.MetaKey == key && !n.HasExpiry() && n.Contexts.Equal(contexts)
		})
		if removed == 0 {
			core.SendError(sender, fmt.Sprintf("%s does not have meta key %s set%s.", h.display, key, describeContextSuffix(contexts)))
			return 0
		}
		if err := h.save(); err != nil {
			core.SendError(sender, "Failed to save data: "+err.Error())
			return 0
		}
		h.log(m, sender, fmt.Sprintf("meta unset %s%s", key, describeContextSuffix(contexts)))
		core.SendInfo(sender, fmt.Sprintf("Unset meta value %s for %s%s.", key, h.display, describeContextSuffix(contexts)))
		return 1

	case "settemp":
		if !m.requirePerm(sender, h.perm(core.PermUserMetaSetTemp, core.PermGroupMetaSetTemp)) || !h.checkModify(m, sender) {
			return 0
		}
		if len(rest) < 3 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s meta settemp <key> <value> <duration> [temp-modifier] [context...]", label, h.holder.Type(), h.display))
			return 0
		}
		key, val := strings.ToLower(rest[0]), rest[1]
		dur, err := core.ParseDuration(rest[2])
		if err != nil {
			core.SendError(sender, "Invalid duration: "+rest[2])
			return 0
		}
		idx := 3
		strategy := m.lp.Config().TemporaryAddBehaviour
		if idx < len(rest) {
			if mod, ok := parseTempModifier(rest[idx]); ok {
				strategy = mod
				idx++
			}
		}
		contexts := parseContextsOrDefault(m.lp, rest[idx:])
		node := core.NewMetaNode(key, val).WithExpiry(time.Now().Add(dur)).WithContexts(contexts)
		stored, res := h.holder.SetNodeTemp(core.DataNormal, node, strategy)
		if !res.WasSuccessful() {
			core.SendError(sender, fmt.Sprintf("%s already has temporary meta %s=%s%s.", h.display, key, val, describeContextSuffix(contexts)))
			return 0
		}
		if err := h.save(); err != nil {
			core.SendError(sender, "Failed to save data: "+err.Error())
			return 0
		}
		remStr := core.FormatDuration(time.Until(stored.Expiry))
		h.log(m, sender, fmt.Sprintf("meta settemp %s %s %s%s", key, val, remStr, describeContextSuffix(contexts)))
		core.SendInfo(sender, fmt.Sprintf("Set temporary meta %s to %q for %s for %s%s.", key, val, h.display, remStr, describeContextSuffix(contexts)))
		return 1

	case "unsettemp":
		if !m.requirePerm(sender, h.perm(core.PermUserMetaUnsetTemp, core.PermGroupMetaUnsetTemp)) || !h.checkModify(m, sender) {
			return 0
		}
		if len(rest) < 1 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s meta unsettemp <key> [context...]", label, h.holder.Type(), h.display))
			return 0
		}
		key := strings.ToLower(rest[0])
		contexts := parseContextsOrDefault(m.lp, rest[1:])
		removed := h.holder.ClearNodes(core.DataNormal, func(n core.Node) bool {
			return n.Type == core.NodeTypeMeta && n.MetaKey == key && n.HasExpiry() && n.Contexts.Equal(contexts)
		})
		if removed == 0 {
			core.SendError(sender, fmt.Sprintf("%s does not have temporary meta key %s%s.", h.display, key, describeContextSuffix(contexts)))
			return 0
		}
		if err := h.save(); err != nil {
			core.SendError(sender, "Failed to save data: "+err.Error())
			return 0
		}
		h.log(m, sender, fmt.Sprintf("meta unsettemp %s%s", key, describeContextSuffix(contexts)))
		core.SendInfo(sender, fmt.Sprintf("Unset temporary meta %s for %s%s.", key, h.display, describeContextSuffix(contexts)))
		return 1

	case "addprefix", "addsuffix":
		isPrefix := sub == "addprefix"
		if isPrefix {
			if !m.requirePerm(sender, h.perm(core.PermUserMetaAddPrefix, core.PermGroupMetaAddPrefix)) || !h.checkModify(m, sender) {
				return 0
			}
		} else {
			if !m.requirePerm(sender, h.perm(core.PermUserMetaAddSuffix, core.PermGroupMetaAddSuffix)) || !h.checkModify(m, sender) {
				return 0
			}
		}
		return m.cmdChatMetaAddOrSet(sender, label, h, isPrefix, false, false, rest)

	case "setprefix", "setsuffix":
		isPrefix := sub == "setprefix"
		if isPrefix {
			if !m.requirePerm(sender, h.perm(core.PermUserMetaSetPrefix, core.PermGroupMetaSetPrefix)) || !h.checkModify(m, sender) {
				return 0
			}
		} else {
			if !m.requirePerm(sender, h.perm(core.PermUserMetaSetSuffix, core.PermGroupMetaSetSuffix)) || !h.checkModify(m, sender) {
				return 0
			}
		}
		return m.cmdChatMetaAddOrSet(sender, label, h, isPrefix, true, false, rest)

	case "addtempprefix", "addtempsuffix":
		isPrefix := sub == "addtempprefix"
		if isPrefix {
			if !m.requirePerm(sender, h.perm(core.PermUserMetaAddTempPrefix, core.PermGroupMetaAddTempPrefix)) || !h.checkModify(m, sender) {
				return 0
			}
		} else {
			if !m.requirePerm(sender, h.perm(core.PermUserMetaAddTempSuffix, core.PermGroupMetaAddTempSuffix)) || !h.checkModify(m, sender) {
				return 0
			}
		}
		return m.cmdChatMetaAddOrSet(sender, label, h, isPrefix, false, true, rest)

	case "settempprefix", "settempsuffix":
		isPrefix := sub == "settempprefix"
		if isPrefix {
			if !m.requirePerm(sender, h.perm(core.PermUserMetaSetTempPrefix, core.PermGroupMetaSetTempPrefix)) || !h.checkModify(m, sender) {
				return 0
			}
		} else {
			if !m.requirePerm(sender, h.perm(core.PermUserMetaSetTempSuffix, core.PermGroupMetaSetTempSuffix)) || !h.checkModify(m, sender) {
				return 0
			}
		}
		return m.cmdChatMetaAddOrSet(sender, label, h, isPrefix, true, true, rest)

	case "removeprefix", "removesuffix", "removetempprefix", "removetempsuffix":
		isPrefix := strings.HasSuffix(sub, "prefix")
		isTemp := strings.Contains(sub, "temp")
		var perm core.CommandPerm
		switch sub {
		case "removeprefix":
			perm = h.perm(core.PermUserMetaRemovePrefix, core.PermGroupMetaRemovePrefix)
		case "removesuffix":
			perm = h.perm(core.PermUserMetaRemoveSuffix, core.PermGroupMetaRemoveSuffix)
		case "removetempprefix":
			perm = h.perm(core.PermUserMetaRemoveTempPrefix, core.PermGroupMetaRemoveTempPrefix)
		default:
			perm = h.perm(core.PermUserMetaRemoveTempSuffix, core.PermGroupMetaRemoveTempSuffix)
		}
		if !m.requirePerm(sender, perm) || !h.checkModify(m, sender) {
			return 0
		}
		return m.cmdChatMetaRemove(sender, label, h, isPrefix, isTemp, rest)

	case "clear":
		if !m.requirePerm(sender, h.perm(core.PermUserMetaClear, core.PermGroupMetaClear)) || !h.checkModify(m, sender) {
			return 0
		}
		var targetType core.NodeType
		idx := 0
		if len(rest) > 0 {
			switch strings.ToLower(rest[0]) {
			case "meta":
				targetType = core.NodeTypeMeta
				idx = 1
			case "prefix", "prefixes":
				targetType = core.NodeTypePrefix
				idx = 1
			case "suffix", "suffixes":
				targetType = core.NodeTypeSuffix
				idx = 1
			case "chatmeta":
				targetType = "chatmeta"
				idx = 1
			}
		}
		var contexts core.ContextSet
		filterCtx := len(rest) > idx
		if filterCtx {
			contexts = core.ParseContextSet(rest[idx:])
		}
		removed := h.holder.ClearNodes(core.DataNormal, func(n core.Node) bool {
			if filterCtx && !n.Contexts.Equal(contexts) {
				return false
			}
			switch targetType {
			case "":
				return n.Type == core.NodeTypeMeta || n.Type == core.NodeTypePrefix || n.Type == core.NodeTypeSuffix
			case "chatmeta":
				return n.Type == core.NodeTypePrefix || n.Type == core.NodeTypeSuffix
			default:
				return n.Type == targetType
			}
		})
		if err := h.save(); err != nil {
			core.SendError(sender, "Failed to save data: "+err.Error())
			return 0
		}
		h.log(m, sender, fmt.Sprintf("meta clear%s", describeContextSuffix(contexts)))
		core.SendInfo(sender, fmt.Sprintf("Cleared %d meta node(s) for %s%s.", removed, h.display, describeContextSuffix(contexts)))
		return 1

	default:
		core.SendError(sender, "Unknown meta subcommand: "+sub)
		return 0
	}
}

func (m *Manager) cmdChatMetaAddOrSet(sender plugin.CommandSender, label string, h *holderTarget, isPrefix, replace, temp bool, rest []string) int {
	kind := "suffix"
	nodeType := core.NodeTypeSuffix
	if isPrefix {
		kind = "prefix"
		nodeType = core.NodeTypePrefix
	}
	if len(rest) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s meta %s <priority> <%s> [context...]", label, h.holder.Type(), h.display, kind, kind))
		return 0
	}

	var prio int
	var val string
	idx := 0
	if replace {
		// setprefix [priority] <prefix> [duration] ...
		if len(rest) >= 2 {
			if p, err := strconv.Atoi(rest[0]); err == nil {
				prio = p
				val = rest[1]
				idx = 2
			}
		}
		if idx == 0 {
			// Auto-choose highest inherited priority + 1 (or 100 if none).
			qo := h.queryOptions(m.lp)
			md := h.holder.CachedData().MetaData(qo)
			var existing map[int]string
			if isPrefix {
				existing = md.Prefixes()
			} else {
				existing = md.Suffixes()
			}
			maxP := 0
			for p := range existing {
				if p > maxP {
					maxP = p
				}
			}
			if maxP == 0 {
				prio = 100
			} else {
				prio = maxP + 1
			}
			val = rest[0]
			idx = 1
		}
	} else {
		if len(rest) < 2 {
			core.SendError(sender, fmt.Sprintf("Missing priority or %s value.", kind))
			return 0
		}
		p, err := strconv.Atoi(rest[0])
		if err != nil {
			core.SendError(sender, "Invalid priority: "+rest[0])
			return 0
		}
		prio = p
		val = rest[1]
		idx = 2
	}

	var dur time.Duration
	strategy := m.lp.Config().TemporaryAddBehaviour
	if temp {
		if idx >= len(rest) {
			core.SendError(sender, "Missing duration argument.")
			return 0
		}
		d, err := core.ParseDuration(rest[idx])
		if err != nil {
			core.SendError(sender, "Invalid duration: "+rest[idx])
			return 0
		}
		dur = d
		idx++
		if idx < len(rest) {
			if mod, ok := parseTempModifier(rest[idx]); ok {
				strategy = mod
				idx++
			}
		}
	}

	contexts := parseContextsOrDefault(m.lp, rest[idx:])
	if replace {
		h.holder.ClearNodes(core.DataNormal, func(n core.Node) bool {
			return n.Type == nodeType && n.HasExpiry() == temp && n.Contexts.Equal(contexts)
		})
	}

	var node core.Node
	if isPrefix {
		node = core.NewPrefixNode(prio, val).WithContexts(contexts)
	} else {
		node = core.NewSuffixNode(prio, val).WithContexts(contexts)
	}
	if temp {
		node = node.WithExpiry(time.Now().Add(dur))
		stored, res := h.holder.SetNodeTemp(core.DataNormal, node, strategy)
		if !res.WasSuccessful() {
			core.SendError(sender, fmt.Sprintf("%s already has temporary %s %q at priority %d.", h.display, kind, val, prio))
			return 0
		}
		if err := h.save(); err != nil {
			core.SendError(sender, "Failed to save data: "+err.Error())
			return 0
		}
		remStr := core.FormatDuration(time.Until(stored.Expiry))
		h.log(m, sender, fmt.Sprintf("meta temp%s %d %s %s%s", kind, prio, val, remStr, describeContextSuffix(contexts)))
		core.SendInfo(sender, fmt.Sprintf("Set temporary %s %q (priority %d) for %s for %s%s.", kind, val, prio, h.display, remStr, describeContextSuffix(contexts)))
		return 1
	}

	if res := h.holder.SetNode(core.DataNormal, node); !res.WasSuccessful() {
		core.SendError(sender, fmt.Sprintf("%s already has %s %q at priority %d.", h.display, kind, val, prio))
		return 0
	}
	if err := h.save(); err != nil {
		core.SendError(sender, "Failed to save data: "+err.Error())
		return 0
	}
	h.log(m, sender, fmt.Sprintf("meta %s %d %s%s", kind, prio, val, describeContextSuffix(contexts)))
	core.SendInfo(sender, fmt.Sprintf("Added %s %q at priority %d for %s%s.", kind, val, prio, h.display, describeContextSuffix(contexts)))
	return 1
}

func (m *Manager) cmdChatMetaRemove(sender plugin.CommandSender, label string, h *holderTarget, isPrefix, isTemp bool, rest []string) int {
	kind := "suffix"
	nodeType := core.NodeTypeSuffix
	if isPrefix {
		kind = "prefix"
		nodeType = core.NodeTypePrefix
	}
	if len(rest) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s %s %s meta remove%s <priority> [%s] [context...]", label, h.holder.Type(), h.display, kind, kind))
		return 0
	}
	prio, err := strconv.Atoi(rest[0])
	if err != nil {
		core.SendError(sender, "Invalid priority: "+rest[0])
		return 0
	}
	idx := 1
	matchVal := ""
	hasVal := false
	if idx < len(rest) && !strings.Contains(rest[idx], "=") {
		matchVal = rest[idx]
		hasVal = true
		idx++
	}
	contexts := parseContextsOrDefault(m.lp, rest[idx:])
	removed := h.holder.ClearNodes(core.DataNormal, func(n core.Node) bool {
		if n.Type != nodeType || n.Priority != prio || n.HasExpiry() != isTemp || !n.Contexts.Equal(contexts) {
			return false
		}
		return !hasVal || n.MetaValue == matchVal
	})
	if removed == 0 {
		core.SendError(sender, fmt.Sprintf("%s does not have a matching %s at priority %d%s.", h.display, kind, prio, describeContextSuffix(contexts)))
		return 0
	}
	if err := h.save(); err != nil {
		core.SendError(sender, "Failed to save data: "+err.Error())
		return 0
	}
	h.log(m, sender, fmt.Sprintf("meta remove%s %d%s", kind, prio, describeContextSuffix(contexts)))
	core.SendInfo(sender, fmt.Sprintf("Removed %d %s node(s) at priority %d from %s%s.", removed, kind, prio, h.display, describeContextSuffix(contexts)))
	return 1
}

// --- Shared Clear & ShowTracks ---

func (m *Manager) cmdHolderClear(sender plugin.CommandSender, h *holderTarget, args []string) int {
	if !m.requirePerm(sender, h.perm(core.PermUserClear, core.PermGroupClear)) || !h.checkModify(m, sender) {
		return 0
	}
	var contexts core.ContextSet
	filterCtx := len(args) > 0
	if filterCtx {
		contexts = core.ParseContextSet(args)
	}
	removed := h.holder.ClearNodes(core.DataNormal, func(n core.Node) bool {
		return !filterCtx || n.Contexts.Equal(contexts)
	})
	if h.isUser() {
		h.user.GiveDefaultIfNeeded()
		if !filterCtx || contexts.IsEmpty() {
			h.user.SetStoredPrimaryGroup(core.DefaultGroupName)
		}
	}
	if err := h.save(); err != nil {
		core.SendError(sender, "Failed to save data: "+err.Error())
		return 0
	}
	h.log(m, sender, fmt.Sprintf("clear%s", describeContextSuffix(contexts)))
	core.SendInfo(sender, fmt.Sprintf("Cleared %d node(s) for %s%s.", removed, h.display, describeContextSuffix(contexts)))
	return 1
}

func (m *Manager) cmdHolderShowTracks(sender plugin.CommandSender, h *holderTarget) int {
	if !m.requirePerm(sender, h.perm(core.PermUserShowTracks, core.PermGroupShowTracks)) {
		return 0
	}
	tracks := m.lp.Tracks.All()
	var lines []plugin.Text
	for _, tr := range tracks {
		if h.isUser() {
			for _, n := range h.user.NormalNodes() {
				if n.Type == core.NodeTypeInheritance && n.Value && tr.ContainsGroup(n.GroupName) {
					lines = append(lines, formatTrackProgress(tr, n.GroupName, n.Contexts))
				}
			}
		} else if tr.ContainsGroup(h.group.Name()) {
			lines = append(lines, formatTrackProgress(tr, h.group.Name(), core.EmptyContextSet))
		}
	}
	if len(lines) == 0 {
		core.SendInfo(sender, fmt.Sprintf("%s is not on any tracks.", h.display))
		return 1
	}
	core.SendPrefixed(sender, plugin.Plain(h.display).Color("aqua"), plugin.Plain("'s Tracks:").Color("green"))
	for _, l := range lines {
		sender.SendMessage(l)
	}
	return 1
}

func formatTrackProgress(tr *core.Track, highlightGroup string, contexts core.ContextSet) plugin.Text {
	out := plugin.Plain("> ").Color("dark_aqua").Append(
		plugin.Plain(tr.Name() + ": ").Color("aqua"),
	)
	for i, g := range tr.Groups() {
		if i > 0 {
			out = out.Append(plugin.Plain(" ---> ").Color("gray"))
		}
		if strings.EqualFold(g, highlightGroup) {
			out = out.Append(plugin.Plain(g).Color("green").Bold())
		} else {
			out = out.Append(plugin.Plain(g).Color("gray"))
		}
	}
	return out.Append(core.FormatContexts(contexts))
}
