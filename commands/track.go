package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/golem-mc/golem/plugin"
	"github.com/golem-mc/perms/core"
)

func (m *Manager) cmdCreateTrack(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermCreateTrack) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s createtrack <track>", label))
		return 0
	}
	name := strings.ToLower(args[0])
	if !isValidGroupOrTrackName(name) {
		core.SendError(sender, "Invalid track name: "+args[0])
		return 0
	}
	if m.lp.Tracks.GetIfLoaded(name) != nil {
		core.SendError(sender, "Track "+name+" already exists.")
		return 0
	}
	if _, err := m.lp.Storage.CreateAndLoadTrack(name); err != nil {
		core.SendError(sender, "Failed to create track: "+err.Error())
		return 0
	}
	m.lp.PushUpdateAsync()
	m.lp.Actions.Submit(sender, core.TargetTrack, nil, name, "create")
	core.SendInfo(sender, fmt.Sprintf("Track %s was successfully created.", name))
	return 1
}

func (m *Manager) cmdDeleteTrack(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermDeleteTrack) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s deletetrack <track>", label))
		return 0
	}
	name := strings.ToLower(args[0])
	t := m.lp.Tracks.GetIfLoaded(name)
	if t == nil {
		core.SendError(sender, "Track "+name+" does not exist.")
		return 0
	}
	if err := m.lp.Storage.DeleteTrack(t); err != nil {
		core.SendError(sender, "Failed to delete track: "+err.Error())
		return 0
	}
	m.lp.PushUpdateAsync()
	m.lp.Actions.Submit(sender, core.TargetTrack, nil, name, "delete")
	core.SendInfo(sender, fmt.Sprintf("Track %s was successfully deleted.", name))
	return 1
}

func (m *Manager) cmdListTracks(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermListTracks) {
		return 0
	}
	tracks := m.lp.Tracks.All()
	if len(tracks) == 0 {
		core.SendInfo(sender, "No tracks exist.")
		return 1
	}
	names := make([]string, len(tracks))
	for i, t := range tracks {
		names[i] = t.Name()
	}
	core.SendPrefixed(sender,
		plugin.Plain("Tracks: ").Color("green"),
		plugin.Plain(strings.Join(names, ", ")).Color("aqua"),
	)
	return 1
}

func (m *Manager) cmdTrack(sender plugin.CommandSender, label string, args []string) int {
	if len(args) < 2 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s track <track> <info|append|insert|remove|clear|rename|clone>", label))
		return 0
	}
	t := m.lp.Tracks.GetIfLoaded(args[0])
	if t == nil {
		core.SendError(sender, "Track "+args[0]+" does not exist.")
		return 0
	}
	sub := strings.ToLower(args[1])
	rest := args[2:]

	switch sub {
	case "info":
		if !m.requirePerm(sender, core.PermTrackInfo) {
			return 0
		}
		groups := t.Groups()
		path := "Empty"
		if len(groups) > 0 {
			path = strings.Join(groups, " ---> ")
		}
		core.SendPrefixed(sender, plugin.Plain("Showing Track: ").Color("green"), plugin.Plain(t.Name()).Color("aqua"))
		sender.SendMessage(plugin.Plain("  Path: ").Color("dark_aqua").Append(plugin.Plain(path).Color("white")))
		return 1

	case "editor":
		if !m.requirePerm(sender, core.PermTrackEditor) {
			return 0
		}
		return m.openEditorSession(sender, label, nil, nil, []*core.Track{t})

	case "append":
		if !m.requirePerm(sender, core.PermTrackAppend) {
			return 0
		}
		if len(rest) < 1 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s track %s append <group>", label, t.Name()))
			return 0
		}
		groupName := strings.ToLower(rest[0])
		if m.lp.Groups.GetIfLoaded(groupName) == nil {
			core.SendError(sender, "Group "+groupName+" does not exist.")
			return 0
		}
		if res := t.AppendGroup(groupName); !res.WasSuccessful() {
			core.SendError(sender, fmt.Sprintf("Track %s already contains group %s.", t.Name(), groupName))
			return 0
		}
		if err := m.lp.Storage.SaveTrack(t); err != nil {
			core.SendError(sender, "Failed to save track: "+err.Error())
			return 0
		}
		m.lp.PushUpdateAsync()
		m.lp.Actions.Submit(sender, core.TargetTrack, nil, t.Name(), "append "+groupName)
		core.SendInfo(sender, fmt.Sprintf("Group %s was appended to track %s.", groupName, t.Name()))
		return 1

	case "insert":
		if !m.requirePerm(sender, core.PermTrackInsert) {
			return 0
		}
		if len(rest) < 2 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s track %s insert <group> <position>", label, t.Name()))
			return 0
		}
		groupName := strings.ToLower(rest[0])
		if m.lp.Groups.GetIfLoaded(groupName) == nil {
			core.SendError(sender, "Group "+groupName+" does not exist.")
			return 0
		}
		pos, err := strconv.Atoi(rest[1])
		if err != nil {
			core.SendError(sender, "Invalid position: "+rest[1])
			return 0
		}
		// LuckPerms /lp track <t> insert <g> <pos> uses 1-based position!
		res, inRange := t.InsertGroup(groupName, pos-1)
		if !inRange {
			core.SendError(sender, fmt.Sprintf("Position %d is out of range (1..%d).", pos, t.Size()+1))
			return 0
		}
		if !res.WasSuccessful() {
			core.SendError(sender, fmt.Sprintf("Track %s already contains group %s.", t.Name(), groupName))
			return 0
		}
		if err := m.lp.Storage.SaveTrack(t); err != nil {
			core.SendError(sender, "Failed to save track: "+err.Error())
			return 0
		}
		m.lp.PushUpdateAsync()
		m.lp.Actions.Submit(sender, core.TargetTrack, nil, t.Name(), fmt.Sprintf("insert %s %d", groupName, pos))
		core.SendInfo(sender, fmt.Sprintf("Group %s was inserted into track %s at position %d.", groupName, t.Name(), pos))
		return 1

	case "remove":
		if !m.requirePerm(sender, core.PermTrackRemove) {
			return 0
		}
		if len(rest) < 1 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s track %s remove <group>", label, t.Name()))
			return 0
		}
		groupName := strings.ToLower(rest[0])
		if res := t.RemoveGroup(groupName); !res.WasSuccessful() {
			core.SendError(sender, fmt.Sprintf("Track %s does not contain group %s.", t.Name(), groupName))
			return 0
		}
		if err := m.lp.Storage.SaveTrack(t); err != nil {
			core.SendError(sender, "Failed to save track: "+err.Error())
			return 0
		}
		m.lp.PushUpdateAsync()
		m.lp.Actions.Submit(sender, core.TargetTrack, nil, t.Name(), "remove "+groupName)
		core.SendInfo(sender, fmt.Sprintf("Group %s was removed from track %s.", groupName, t.Name()))
		return 1

	case "clear":
		if !m.requirePerm(sender, core.PermTrackClear) {
			return 0
		}
		t.ClearGroups()
		if err := m.lp.Storage.SaveTrack(t); err != nil {
			core.SendError(sender, "Failed to save track: "+err.Error())
			return 0
		}
		m.lp.PushUpdateAsync()
		m.lp.Actions.Submit(sender, core.TargetTrack, nil, t.Name(), "clear")
		core.SendInfo(sender, fmt.Sprintf("Cleared all groups from track %s.", t.Name()))
		return 1

	case "rename":
		if !m.requirePerm(sender, core.PermTrackRename) {
			return 0
		}
		if len(rest) < 1 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s track %s rename <new-name>", label, t.Name()))
			return 0
		}
		newName := strings.ToLower(rest[0])
		if !isValidGroupOrTrackName(newName) {
			core.SendError(sender, "Invalid track name: "+rest[0])
			return 0
		}
		if m.lp.Tracks.GetIfLoaded(newName) != nil {
			core.SendError(sender, "Track "+newName+" already exists.")
			return 0
		}
		dst, err := m.lp.Storage.CreateAndLoadTrack(newName)
		if err != nil {
			core.SendError(sender, "Failed to create track: "+err.Error())
			return 0
		}
		dst.SetGroups(t.Groups())
		if err := m.lp.Storage.SaveTrack(dst); err != nil {
			core.SendError(sender, "Failed to save track: "+err.Error())
			return 0
		}
		oldName := t.Name()
		if err := m.lp.Storage.DeleteTrack(t); err != nil {
			core.SendError(sender, "Failed to delete old track: "+err.Error())
			return 0
		}
		m.lp.PushUpdateAsync()
		m.lp.Actions.Submit(sender, core.TargetTrack, nil, oldName, "rename "+newName)
		core.SendInfo(sender, fmt.Sprintf("Track %s was successfully renamed to %s.", oldName, newName))
		return 1

	case "clone":
		if !m.requirePerm(sender, core.PermTrackClone) {
			return 0
		}
		if len(rest) < 1 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s track %s clone <new-name>", label, t.Name()))
			return 0
		}
		newName := strings.ToLower(rest[0])
		if !isValidGroupOrTrackName(newName) {
			core.SendError(sender, "Invalid track name: "+rest[0])
			return 0
		}
		dst, err := m.lp.Storage.CreateAndLoadTrack(newName)
		if err != nil {
			core.SendError(sender, "Failed to create track: "+err.Error())
			return 0
		}
		dst.SetGroups(t.Groups())
		if err := m.lp.Storage.SaveTrack(dst); err != nil {
			core.SendError(sender, "Failed to save track: "+err.Error())
			return 0
		}
		m.lp.PushUpdateAsync()
		m.lp.Actions.Submit(sender, core.TargetTrack, nil, t.Name(), "clone "+newName)
		core.SendInfo(sender, fmt.Sprintf("Track %s was successfully cloned onto %s.", t.Name(), newName))
		return 1

	default:
		core.SendError(sender, "Unknown track subcommand: "+sub)
		return 0
	}
}
