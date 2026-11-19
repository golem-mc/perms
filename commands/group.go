package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/golem-mc/golem/plugin"
	"github.com/golem-mc/perms/core"
)

func (m *Manager) cmdCreateGroup(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermCreateGroup) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s creategroup <group> [weight] [displayname]", label))
		return 0
	}
	name := strings.ToLower(args[0])
	if !isValidGroupOrTrackName(name) {
		core.SendError(sender, "Invalid group name: "+args[0])
		return 0
	}
	if m.lp.Groups.GetIfLoaded(name) != nil {
		core.SendError(sender, "Group "+name+" already exists.")
		return 0
	}
	g, err := m.lp.Storage.CreateAndLoadGroup(name)
	if err != nil {
		core.SendError(sender, "Failed to create group: "+err.Error())
		return 0
	}
	if len(args) >= 2 {
		if w, err := strconv.Atoi(args[1]); err == nil {
			g.SetNode(core.DataNormal, core.NewWeightNode(w))
		} else {
			core.SendError(sender, "Invalid weight: "+args[1])
			return 0
		}
	}
	if len(args) >= 3 {
		g.SetNode(core.DataNormal, core.NewDisplayNameNode(args[2]))
	}
	if err := m.lp.Storage.SaveGroup(g); err != nil {
		core.SendError(sender, "Failed to save group: "+err.Error())
		return 0
	}
	m.lp.FireGroupCreate(g)
	m.lp.PushUpdateAsync()
	m.lp.Actions.Submit(sender, core.TargetGroup, nil, name, "create")
	core.SendInfo(sender, fmt.Sprintf("Group %s was successfully created.", name))
	return 1
}

func (m *Manager) cmdDeleteGroup(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermDeleteGroup) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s deletegroup <group>", label))
		return 0
	}
	name := strings.ToLower(args[0])
	if name == core.DefaultGroupName {
		core.SendError(sender, "You cannot delete the default group.")
		return 0
	}
	g := m.lp.Groups.GetIfLoaded(name)
	if g == nil {
		core.SendError(sender, "Group "+name+" does not exist.")
		return 0
	}
	if err := m.lp.Storage.DeleteGroup(g); err != nil {
		core.SendError(sender, "Failed to delete group: "+err.Error())
		return 0
	}
	m.lp.FireGroupDelete(name)
	m.lp.PushUpdateAsync()
	m.lp.Actions.Submit(sender, core.TargetGroup, nil, name, "delete")
	core.SendInfo(sender, fmt.Sprintf("Group %s was successfully deleted.", name))
	return 1
}

func (m *Manager) cmdListGroups(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermListGroups) {
		return 0
	}
	groups := m.lp.Groups.All()
	core.SendPrefixed(sender, plugin.Plain("Groups: ").Color("green"), plugin.Plain("(name, weight, display name, tracks)").Color("gray"))
	qo := m.lp.Contexts.StaticQueryOptions()
	for _, g := range groups {
		w, hasW := g.Weight()
		wStr := "none"
		if hasW {
			wStr = strconv.Itoa(w)
		}
		var tracks []string
		for _, tr := range m.lp.Tracks.All() {
			if tr.ContainsGroup(g.Name()) {
				tracks = append(tracks, tr.Name())
			}
		}
		line := plugin.Plain("- ").Color("dark_aqua").Append(
			plugin.Plain(g.Name()).Color("aqua").OnClick(plugin.RunCommand, fmt.Sprintf("/%s group %s info", label, g.Name())),
			plugin.Plain(" (weight: "+wStr+")").Color("gray"),
		)
		if dn := g.DisplayName(qo); dn != "" {
			line = line.Append(plugin.Plain(" [" + dn + "]").Color("white"))
		}
		if len(tracks) > 0 {
			line = line.Append(plugin.Plain(" (tracks: " + strings.Join(tracks, ", ") + ")").Color("dark_gray"))
		}
		sender.SendMessage(line)
	}
	return 1
}

func (m *Manager) cmdGroup(sender plugin.CommandSender, label string, args []string) int {
	if len(args) < 2 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s group <group> <info|permission|parent|meta|listmembers|setweight|setdisplayname|showtracks|clear|rename|clone>", label))
		return 0
	}
	groupName := strings.ToLower(args[0])
	g := m.lp.Groups.GetIfLoaded(groupName)
	if g == nil {
		core.SendError(sender, "Group "+groupName+" does not exist.")
		return 0
	}
	sub := strings.ToLower(args[1])
	rest := args[2:]

	h := &holderTarget{
		holder:     &g.PermissionHolder,
		group:      g,
		display:    g.Name(),
		targetType: core.TargetGroup,
		save: func() error {
			if err := m.lp.Storage.SaveGroup(g); err != nil {
				return err
			}
			m.lp.PushUpdateAsync()
			return nil
		},
	}

	switch sub {
	case "info":
		return m.cmdGroupInfo(sender, g)
	case "permission":
		return m.cmdHolderPermission(sender, label, h, rest)
	case "parent":
		return m.cmdHolderParent(sender, label, h, rest)
	case "meta":
		return m.cmdHolderMeta(sender, label, h, rest)
	case "editor":
		if !m.requirePerm(sender, core.PermGroupEditor) {
			return 0
		}
		return m.openEditorSession(sender, label, []*core.Group{g}, nil, nil)
	case "listmembers":
		return m.cmdGroupListMembers(sender, g, rest)
	case "setweight":
		return m.cmdGroupSetWeight(sender, label, g, h, rest)
	case "setdisplayname":
		return m.cmdGroupSetDisplayName(sender, label, g, h, rest)
	case "showtracks":
		return m.cmdHolderShowTracks(sender, h)
	case "clear":
		return m.cmdHolderClear(sender, h, rest)
	case "rename":
		return m.cmdGroupRename(sender, label, g, h, rest)
	case "clone":
		return m.cmdGroupClone(sender, label, g, h, rest)
	default:
		core.SendError(sender, "Unknown group subcommand: "+sub)
		return 0
	}
}

func (m *Manager) cmdGroupInfo(sender plugin.CommandSender, g *core.Group) int {
	if !m.requirePerm(sender, core.PermGroupInfo) {
		return 0
	}
	qo := m.lp.Contexts.StaticQueryOptions()
	md := g.CachedData().MetaData(qo)
	w, hasW := g.Weight()
	wStr := "None"
	if hasW {
		wStr = strconv.Itoa(w)
	}
	var permCount, parentCount, metaCount int
	var parentNames []string
	for _, n := range g.NormalNodes() {
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
	core.SendPrefixed(sender, plugin.Plain("Group Info: ").Color("green"), plugin.Plain(g.Name()).Color("aqua"))
	if dn := g.DisplayName(qo); dn != "" {
		sender.SendMessage(plugin.Plain("  Display Name: ").Color("dark_aqua").Append(core.ParseLegacyColor(dn)))
	}
	sender.SendMessage(plugin.Plain("  Weight: ").Color("dark_aqua").Append(plugin.Plain(wStr).Color("white")))
	if len(parentNames) > 0 {
		sender.SendMessage(plugin.Plain("  Parents: ").Color("dark_aqua").Append(plugin.Plain(strings.Join(parentNames, ", ")).Color("white")))
	}
	sender.SendMessage(plugin.Plain("  Counts: ").Color("dark_aqua").Append(plugin.Plain(fmt.Sprintf("%d permissions, %d parents, %d meta", permCount, parentCount, metaCount)).Color("white")))
	if p := md.Prefix(); p != "" {
		sender.SendMessage(plugin.Plain("  Prefix: ").Color("dark_aqua").Append(core.ParseLegacyColor(p)))
	}
	if s := md.Suffix(); s != "" {
		sender.SendMessage(plugin.Plain("  Suffix: ").Color("dark_aqua").Append(core.ParseLegacyColor(s)))
	}
	return 1
}

func (m *Manager) cmdGroupListMembers(sender plugin.CommandSender, g *core.Group, args []string) int {
	if !m.requirePerm(sender, core.PermGroupListMembers) {
		return 0
	}
	page := parsePage(args, 0)
	match := func(n core.Node) bool {
		return n.Type == core.NodeTypeInheritance && n.Value && n.GroupName == g.Name()
	}
	userRes, _ := m.lp.Storage.SearchUserNodes(match)
	groupRes, _ := m.lp.Storage.SearchGroupNodes(match)
	all := append(groupRes, userRes...)
	slice, curPage, totalPages := paginate(all, page, 8)
	core.SendPrefixed(sender,
		plugin.Plain("Members of ").Color("green"),
		plugin.Plain(g.Name()).Color("aqua"),
		plugin.Plain(fmt.Sprintf(" (page %d of %d - %d entries)", curPage, totalPages, len(all))).Color("gray"),
	)
	if len(slice) == 0 {
		sender.SendMessage(plugin.Plain("  None").Color("gray"))
		return 1
	}
	for _, r := range slice {
		sender.SendMessage(plugin.Plain("> ").Color("dark_aqua").Append(
			plugin.Plain(fmt.Sprintf("[%s] %s", r.HolderType, r.HolderName)).Color("white"),
			core.FormatContexts(r.Node.Contexts),
		))
	}
	return 1
}

func (m *Manager) cmdGroupSetWeight(sender plugin.CommandSender, label string, g *core.Group, h *holderTarget, args []string) int {
	if !m.requirePerm(sender, core.PermGroupSetWeight) || !h.checkModify(m, sender) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s group %s setweight <weight>", label, g.Name()))
		return 0
	}
	w, err := strconv.Atoi(args[0])
	if err != nil {
		core.SendError(sender, "Invalid weight: "+args[0])
		return 0
	}
	g.ClearNodes(core.DataNormal, func(n core.Node) bool { return n.Type == core.NodeTypeWeight })
	g.SetNode(core.DataNormal, core.NewWeightNode(w))
	if err := h.save(); err != nil {
		core.SendError(sender, "Failed to save group: "+err.Error())
		return 0
	}
	h.log(m, sender, fmt.Sprintf("setweight %d", w))
	core.SendInfo(sender, fmt.Sprintf("Set weight to %d for group %s.", w, g.Name()))
	return 1
}

func (m *Manager) cmdGroupSetDisplayName(sender plugin.CommandSender, label string, g *core.Group, h *holderTarget, args []string) int {
	if !m.requirePerm(sender, core.PermGroupSetDisplayName) || !h.checkModify(m, sender) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s group %s setdisplayname <name> [context...]", label, g.Name()))
		return 0
	}
	dn := args[0]
	contexts := parseContextsOrDefault(m.lp, args[1:])
	g.ClearNodes(core.DataNormal, func(n core.Node) bool {
		return n.Type == core.NodeTypeDisplayName && n.Contexts.Equal(contexts)
	})
	if dn != "" && !strings.EqualFold(dn, g.Name()) {
		g.SetNode(core.DataNormal, core.NewDisplayNameNode(dn).WithContexts(contexts))
	}
	if err := h.save(); err != nil {
		core.SendError(sender, "Failed to save group: "+err.Error())
		return 0
	}
	h.log(m, sender, fmt.Sprintf("setdisplayname %s%s", dn, describeContextSuffix(contexts)))
	core.SendInfo(sender, fmt.Sprintf("Set display name to %q for group %s%s.", dn, g.Name(), describeContextSuffix(contexts)))
	return 1
}

func (m *Manager) cmdGroupRename(sender plugin.CommandSender, label string, g *core.Group, h *holderTarget, args []string) int {
	if !m.requirePerm(sender, core.PermGroupRename) || !h.checkModify(m, sender) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s group %s rename <new-name>", label, g.Name()))
		return 0
	}
	if g.Name() == core.DefaultGroupName {
		core.SendError(sender, "You cannot rename the default group.")
		return 0
	}
	newName := strings.ToLower(args[0])
	if !isValidGroupOrTrackName(newName) {
		core.SendError(sender, "Invalid group name: "+args[0])
		return 0
	}
	if m.lp.Groups.GetIfLoaded(newName) != nil {
		core.SendError(sender, "Group "+newName+" already exists.")
		return 0
	}
	newGroup, err := m.lp.Storage.CreateAndLoadGroup(newName)
	if err != nil {
		core.SendError(sender, "Failed to create new group: "+err.Error())
		return 0
	}
	newGroup.Data(core.DataNormal).SetContent(g.NormalNodes())
	if err := m.lp.Storage.SaveGroup(newGroup); err != nil {
		core.SendError(sender, "Failed to save new group: "+err.Error())
		return 0
	}
	oldName := g.Name()
	if err := m.lp.Storage.DeleteGroup(g); err != nil {
		core.SendError(sender, "Failed to delete old group: "+err.Error())
		return 0
	}
	m.lp.InvalidateAllCaches()
	m.lp.PushUpdateAsync()
	h.log(m, sender, "rename "+newName)
	core.SendInfo(sender, fmt.Sprintf("Group %s was successfully renamed to %s.", oldName, newName))
	return 1
}

func (m *Manager) cmdGroupClone(sender plugin.CommandSender, label string, g *core.Group, h *holderTarget, args []string) int {
	if !m.requirePerm(sender, core.PermGroupClone) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s group %s clone <new-name>", label, g.Name()))
		return 0
	}
	newName := strings.ToLower(args[0])
	if !isValidGroupOrTrackName(newName) {
		core.SendError(sender, "Invalid group name: "+args[0])
		return 0
	}
	dst, err := m.lp.Storage.CreateAndLoadGroup(newName)
	if err != nil {
		core.SendError(sender, "Failed to create group: "+err.Error())
		return 0
	}
	dst.Data(core.DataNormal).SetContent(g.NormalNodes())
	dst.InvalidateCache()
	if err := m.lp.Storage.SaveGroup(dst); err != nil {
		core.SendError(sender, "Failed to save group: "+err.Error())
		return 0
	}
	m.lp.PushUpdateAsync()
	h.log(m, sender, "clone "+newName)
	core.SendInfo(sender, fmt.Sprintf("Group %s was successfully cloned onto %s.", g.Name(), newName))
	return 1
}
