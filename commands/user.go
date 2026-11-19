package commands

import (
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/golem-mc/golem/plugin"
	"github.com/golem-mc/perms/core"
)

func (m *Manager) resolveTargetPlayers(sender plugin.CommandSender, selector string) []plugin.Player {
	if !m.lp.Config().ResolveCommandSelectors || !strings.HasPrefix(selector, "@") || m.lp.Server == nil {
		return nil
	}
	players := m.lp.Server.Players()
	switch strings.ToLower(selector) {
	case "@s":
		if p, ok := sender.(plugin.Player); ok {
			return []plugin.Player{p}
		}
	case "@a":
		return players
	case "@p":
		if p, ok := sender.(plugin.Player); ok {
			return []plugin.Player{p}
		}
		if len(players) > 0 {
			return []plugin.Player{players[0]}
		}
	case "@r":
		if len(players) > 0 {
			return []plugin.Player{players[rand.IntN(len(players))]}
		}
	}
	return nil
}

func (m *Manager) cmdUser(sender plugin.CommandSender, label string, args []string) int {
	if len(args) < 2 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s user <user> <info|permission|parent|meta|editor|promote|demote|showtracks|clear|clone>", label))
		return 0
	}
	targetArg := args[0]
	if matched := m.resolveTargetPlayers(sender, targetArg); len(matched) > 0 {
		okCount := 0
		for _, p := range matched {
			rebuilt := append([]string{p.Name()}, args[1:]...)
			if m.cmdUserSingle(sender, label, rebuilt) != 0 {
				okCount++
			}
		}
		return okCount
	}
	return m.cmdUserSingle(sender, label, args)
}

func (m *Manager) cmdUserSingle(sender plugin.CommandSender, label string, args []string) int {
	targetArg := args[0]
	id, name, ok := m.lp.ResolveUserUUID(targetArg)
	if !ok {
		core.SendError(sender, fmt.Sprintf("A user for %s could not be found.", targetArg))
		return 0
	}
	sub := strings.ToLower(args[1])
	rest := args[2:]

	var code int
	err := m.lp.WithUser(id, name, false, func(u *core.User) error {
		uid := u.UUID()
		h := &holderTarget{
			holder:     &u.PermissionHolder,
			user:       u,
			display:    u.FriendlyName(),
			targetType: core.TargetUser,
			targetUUID: &uid,
			save: func() error {
				if err := m.lp.Storage.SaveUser(u); err != nil {
					return err
				}
				m.lp.PushUserUpdateAsync(u)
				return nil
			},
		}

		switch sub {
		case "info":
			code = m.cmdUserInfo(sender, u)
		case "permission":
			code = m.cmdHolderPermission(sender, label, h, rest)
		case "parent":
			code = m.cmdHolderParent(sender, label, h, rest)
		case "meta":
			code = m.cmdHolderMeta(sender, label, h, rest)
		case "editor":
			if !m.requirePerm(sender, core.PermUserEditor) {
				code = 0
			} else {
				code = m.openEditorSession(sender, label, nil, []*core.User{u}, nil)
			}
		case "promote":
			code = m.cmdUserPromoteOrDemote(sender, label, u, h, true, rest)
		case "demote":
			code = m.cmdUserPromoteOrDemote(sender, label, u, h, false, rest)
		case "showtracks":
			code = m.cmdHolderShowTracks(sender, h)
		case "clear":
			code = m.cmdHolderClear(sender, h, rest)
		case "clone":
			code = m.cmdUserClone(sender, label, u, h, rest)
		default:
			core.SendError(sender, "Unknown user subcommand: "+sub)
			code = 0
		}
		return nil
	})
	if err != nil {
		core.SendError(sender, "Failed to load user: "+err.Error())
		return 0
	}
	return code
}

func (m *Manager) cmdUserInfo(sender plugin.CommandSender, u *core.User) int {
	if !m.requirePerm(sender, core.PermUserInfo) {
		return 0
	}
	onlinePlayer, online := m.lp.Server.Player(u.UUID())
	qo := m.lp.Contexts.StaticQueryOptions()
	if online {
		qo = m.lp.Contexts.QueryOptionsForPlayer(onlinePlayer)
	}
	md := u.CachedData().MetaData(qo)

	var permCount, parentCount, metaCount int
	var parentNames []string
	for _, n := range u.NormalNodes() {
		switch n.Type {
		case core.NodeTypeInheritance:
			parentCount++
			if n.Value {
				parentNames = append(parentNames, n.GroupName)
			}
		case core.NodeTypePrefix, core.NodeTypeSuffix, core.NodeTypeMeta:
			metaCount++
		default:
			permCount++
		}
	}

	status := "Offline"
	if online {
		status = "Online"
	}

	core.SendPrefixed(sender, plugin.Plain("User Info: ").Color("green"), plugin.Plain(u.FriendlyName()).Color("aqua"))
	sender.SendMessage(plugin.Plain("  UUID: ").Color("dark_aqua").Append(plugin.Plain(u.UUID().String()).Color("white")))
	sender.SendMessage(plugin.Plain("  Status: ").Color("dark_aqua").Append(plugin.Plain(status).Color("aqua")))
	sender.SendMessage(plugin.Plain("  Primary Group: ").Color("dark_aqua").Append(plugin.Plain(u.PrimaryGroup(qo)).Color("aqua")))
	sender.SendMessage(plugin.Plain("  Parents: ").Color("dark_aqua").Append(plugin.Plain(strings.Join(parentNames, ", ")).Color("white")))
	sender.SendMessage(plugin.Plain("  Counts: ").Color("dark_aqua").Append(plugin.Plain(fmt.Sprintf("%d permissions, %d parents, %d meta", permCount, parentCount, metaCount)).Color("white")))
	if p := md.Prefix(); p != "" {
		sender.SendMessage(plugin.Plain("  Prefix: ").Color("dark_aqua").Append(core.ParseLegacyColor(p)))
	}
	if s := md.Suffix(); s != "" {
		sender.SendMessage(plugin.Plain("  Suffix: ").Color("dark_aqua").Append(core.ParseLegacyColor(s)))
	}
	if online {
		sender.SendMessage(plugin.Plain("  Contexts: ").Color("dark_aqua").Append(plugin.Plain(qo.Contexts.String()).Color("aqua")))
	}
	return 1
}

func (m *Manager) cmdUserPromoteOrDemote(sender plugin.CommandSender, label string, u *core.User, h *holderTarget, promote bool, args []string) int {
	verb := "demote"
	perm := core.PermUserDemote
	if promote {
		verb = "promote"
		perm = core.PermUserPromote
	}
	if !m.requirePerm(sender, perm) || !h.checkModify(m, sender) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s user %s %s <track> [context...]", label, h.display, verb))
		return 0
	}
	track := m.lp.Tracks.GetIfLoaded(args[0])
	if track == nil {
		core.SendError(sender, "Track "+args[0]+" does not exist.")
		return 0
	}

	flag := true
	var ctxArgs []string
	for _, a := range args[1:] {
		if promote && (strings.EqualFold(a, "--dont-add-to-first") || strings.EqualFold(a, "dont-add-to-first")) {
			flag = false
		} else if !promote && (strings.EqualFold(a, "--dont-remove-from-first") || strings.EqualFold(a, "dont-remove-from-first")) {
			flag = false
		} else {
			ctxArgs = append(ctxArgs, a)
		}
	}
	contexts := parseContextsOrDefault(m.lp, ctxArgs)

	permCheck := func(g string) bool {
		return m.lp.CheckModifyGroupPermission(sender, g, verb)
	}

	var res core.PromotionResult
	if promote {
		res = track.Promote(u, contexts, permCheck, flag)
	} else {
		res = track.Demote(u, contexts, permCheck, flag)
	}

	switch res.Status {
	case core.PromoEmptyTrack:
		core.SendError(sender, fmt.Sprintf("Track %s must contain at least 2 groups.", track.Name()))
		return 0
	case core.PromoMalformedTrack:
		core.SendError(sender, fmt.Sprintf("Track %s references non-existent group %s.", track.Name(), res.Missing))
		return 0
	case core.PromoAmbiguousCall:
		core.SendError(sender, fmt.Sprintf("%s is in more than one group on track %s.", h.display, track.Name()))
		return 0
	case core.PromoEndOfTrack:
		core.SendError(sender, fmt.Sprintf("%s is already at the end of track %s (%s).", h.display, track.Name(), res.GroupFrom))
		return 0
	case core.PromoNotOnTrack:
		core.SendError(sender, fmt.Sprintf("%s is not in any groups on track %s.", h.display, track.Name()))
		return 0
	case core.PromoPermissionDenied:
		core.SendError(sender, "You do not have permission to perform this track action.")
		return 0
	case core.PromoAddedToFirst:
		if res.GroupTo == "" {
			core.SendError(sender, fmt.Sprintf("%s is not in any groups on track %s.", h.display, track.Name()))
			return 0
		}
		if err := h.save(); err != nil {
			core.SendError(sender, "Failed to save data: "+err.Error())
			return 0
		}
		h.log(m, sender, fmt.Sprintf("promote %s (added to %s)%s", track.Name(), res.GroupTo, describeContextSuffix(contexts)))
		core.SendInfo(sender, fmt.Sprintf("%s wasn't already in any groups on track %s, so they were added to the first group: %s%s.", h.display, track.Name(), res.GroupTo, describeContextSuffix(contexts)))
		return 1
	case core.PromoRemovedFromFirst:
		if res.GroupFrom == "" {
			core.SendError(sender, fmt.Sprintf("%s is at the start of track %s and cannot be demoted further.", h.display, track.Name()))
			return 0
		}
		if err := h.save(); err != nil {
			core.SendError(sender, "Failed to save data: "+err.Error())
			return 0
		}
		h.log(m, sender, fmt.Sprintf("demote %s (removed from %s)%s", track.Name(), res.GroupFrom, describeContextSuffix(contexts)))
		core.SendInfo(sender, fmt.Sprintf("Removed %s from %s as it was the first group on track %s%s.", h.display, res.GroupFrom, track.Name(), describeContextSuffix(contexts)))
		return 1
	case core.PromoSuccess:
		if err := h.save(); err != nil {
			core.SendError(sender, "Failed to save data: "+err.Error())
			return 0
		}
		h.log(m, sender, fmt.Sprintf("%s %s (%s -> %s)%s", verb, track.Name(), res.GroupFrom, res.GroupTo, describeContextSuffix(contexts)))
		if promote {
			core.SendInfo(sender, fmt.Sprintf("Promoted %s along track %s from %s to %s%s.", h.display, track.Name(), res.GroupFrom, res.GroupTo, describeContextSuffix(contexts)))
		} else {
			core.SendInfo(sender, fmt.Sprintf("Demoted %s along track %s from %s to %s%s.", h.display, track.Name(), res.GroupFrom, res.GroupTo, describeContextSuffix(contexts)))
		}
		return 1
	}
	return 0
}

func (m *Manager) cmdUserClone(sender plugin.CommandSender, label string, src *core.User, h *holderTarget, args []string) int {
	if !m.requirePerm(sender, core.PermUserClone) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s user %s clone <target-user>", label, h.display))
		return 0
	}
	dstID, dstName, ok := m.lp.ResolveUserUUID(args[0])
	if !ok {
		core.SendError(sender, fmt.Sprintf("A user for %s could not be found.", args[0]))
		return 0
	}
	err := m.lp.WithUser(dstID, dstName, true, func(dst *core.User) error {
		dst.Data(core.DataNormal).SetContent(src.NormalNodes())
		dst.SetStoredPrimaryGroup(src.StoredPrimaryGroup())
		dst.GiveDefaultIfNeeded()
		dst.InvalidateCache()
		return nil
	})
	if err != nil {
		core.SendError(sender, "Failed to clone user: "+err.Error())
		return 0
	}
	h.log(m, sender, "clone "+args[0])
	core.SendInfo(sender, fmt.Sprintf("Cloned %s onto %s.", src.FriendlyName(), args[0]))
	return 1
}
