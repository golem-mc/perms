package commands

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golem-mc/golem/plugin"
	"github.com/golem-mc/perms/core"
)

func (m *Manager) cmdInfo(sender plugin.CommandSender, label string) int {
	if !m.requirePerm(sender, core.PermInfo) {
		return 0
	}
	cfg := m.lp.Config()
	mcVer, proto := m.lp.Server.Version()
	uniqueUsers, _ := m.lp.Storage.GetUniqueUsers()
	uptime := core.FormatDuration(time.Since(m.lp.StartedAt))
	msgName := "None"
	if m.lp.Messaging != nil {
		msgName = m.lp.Messaging.Name()
	}

	core.SendPrefixed(sender, plugin.Plain("Running ").Color("green"), plugin.Plain("LuckPerms v"+core.Version).Color("aqua"))
	sender.SendMessage(plugin.Plain("  Platform: ").Color("dark_aqua").Append(plugin.Plain(fmt.Sprintf("golem (Minecraft %s, protocol %d)", mcVer, proto)).Color("white")))
	sender.SendMessage(plugin.Plain("  Storage: ").Color("dark_aqua").Append(plugin.Plain(m.lp.Storage.Name()).Color("aqua")))
	sender.SendMessage(plugin.Plain("  Messaging: ").Color("dark_aqua").Append(plugin.Plain(msgName).Color("aqua")))
	sender.SendMessage(plugin.Plain("  Server Name: ").Color("dark_aqua").Append(plugin.Plain(cfg.Server).Color("aqua")))
	sender.SendMessage(plugin.Plain("  Static Contexts: ").Color("dark_aqua").Append(plugin.Plain(m.lp.Contexts.StaticContexts().String()).Color("aqua")))
	sender.SendMessage(plugin.Plain("  Counts: ").Color("dark_aqua").Append(plugin.Plain(fmt.Sprintf("%d online players, %d loaded users, %d stored users, %d groups, %d tracks",
		len(m.lp.Server.Players()), len(m.lp.Users.Loaded()), len(uniqueUsers), len(m.lp.Groups.All()), len(m.lp.Tracks.All()))).Color("white")))
	sender.SendMessage(plugin.Plain("  Uptime: ").Color("dark_aqua").Append(plugin.Plain(uptime).Color("white")))
	return 1
}

func (m *Manager) cmdSync(sender plugin.CommandSender, label string) int {
	if !m.requirePerm(sender, core.PermSync) {
		return 0
	}
	if err := m.lp.Sync(); err != nil {
		core.SendError(sender, "Sync failed: "+err.Error())
		return 0
	}
	core.SendInfo(sender, "An update task has been completed. All groups, tracks, and online users were refreshed.")
	return 1
}

func (m *Manager) cmdNetworkSync(sender plugin.CommandSender, label string) int {
	if !m.requirePerm(sender, core.PermSync) {
		return 0
	}
	if err := m.lp.Sync(); err != nil {
		core.SendError(sender, "Sync failed: "+err.Error())
		return 0
	}
	core.SendInfo(sender, "Local sync task completed.")
	if m.lp.Messaging == nil {
		core.SendError(sender, "Unable to push changes to other servers: no messaging service is configured.")
		return 0
	}
	if err := m.lp.Messaging.PushUpdate(); err != nil {
		core.SendError(sender, "Failed to push network update: "+err.Error())
		return 0
	}
	core.SendInfo(sender, fmt.Sprintf("Other servers were notified of the update via %s.", m.lp.Messaging.Name()))
	return 1
}

func (m *Manager) cmdReloadConfig(sender plugin.CommandSender, label string) int {
	if !m.requirePerm(sender, core.PermReloadConfig) {
		return 0
	}
	if err := m.lp.ReloadConfig(); err != nil {
		core.SendError(sender, "Failed to reload config: "+err.Error())
		return 0
	}
	core.SendInfo(sender, "The configuration file was reloaded.")
	return 1
}

func (m *Manager) cmdTranslations(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermTranslations) {
		return 0
	}
	if len(args) >= 1 && strings.EqualFold(args[0], "install") {
		core.SendInfo(sender, "Downloading and installing community translations...")
		langs, err := m.lp.Translations.AvailableLanguages()
		if err != nil {
			core.SendError(sender, "Unable to fetch translations list: "+err.Error())
			return 0
		}
		n, err := m.lp.Translations.DownloadAndInstall(langs, nil)
		if err != nil {
			core.SendError(sender, "Failed to install translations: "+err.Error())
			return 0
		}
		core.SendInfo(sender, fmt.Sprintf("Installed %d translation bundle(s). Installed locales: %s", n, strings.Join(m.lp.Translations.InstalledLocales(), ", ")))
		return 1
	}

	core.SendInfo(sender, "Installed translations: "+strings.Join(m.lp.Translations.InstalledLocales(), ", "))
	langs, err := m.lp.Translations.AvailableLanguages()
	if err != nil {
		core.SendError(sender, "Unable to fetch available community translations: "+err.Error())
		return 1
	}
	core.SendInfo(sender, fmt.Sprintf("Available community translations (%d):", len(langs)))
	limit := min(len(langs), 20)
	for i := range limit {
		l := langs[i]
		sender.SendMessage(plugin.Plain(fmt.Sprintf("  - %s (%s) - %d%%", l.LocaleTag, l.Name, l.Progress)).Color("aqua"))
	}
	core.SendInfo(sender, fmt.Sprintf("Run '/%s translations install' to download and install all translations.", label))
	return 1
}

func (m *Manager) cmdQuickCheck(sender plugin.CommandSender, label string, args []string) int {
	if len(args) < 2 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s check <user> <permission> [context...]", label))
		return 0
	}
	rebuilt := append([]string{args[0], "permission", "check"}, args[1:]...)
	return m.cmdUser(sender, label, rebuilt)
}

func (m *Manager) cmdSearch(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermSearch) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s search [==|!=|~~|!~] <query> [page]", label))
		return 0
	}
	cmpOp := core.BulkEq
	idx := 0
	if op, ok := core.ParseBulkComparison(args[0]); ok {
		cmpOp = op
		idx = 1
	}
	if idx >= len(args) {
		core.SendError(sender, "Missing search query.")
		return 0
	}
	query := args[idx]
	page := parsePage(args, idx+1)

	bu := &core.BulkUpdate{
		Constraints: []core.BulkConstraint{{
			Field:      core.BulkFieldPermission,
			Comparison: cmpOp,
			Value:      query,
		}},
	}
	match := func(n core.Node) bool { return bu.Matches(n) }
	groupRes, _ := m.lp.Storage.SearchGroupNodes(match)
	userRes, _ := m.lp.Storage.SearchUserNodes(match)
	all := append(groupRes, userRes...)
	slice, curPage, totalPages := paginate(all, page, 8)

	core.SendPrefixed(sender,
		plugin.Plain(fmt.Sprintf("Search results for %s %s ", cmpOp, query)).Color("green"),
		plugin.Plain(fmt.Sprintf("(page %d of %d - %d entries)", curPage, totalPages, len(all))).Color("gray"),
	)
	if len(slice) == 0 {
		sender.SendMessage(plugin.Plain("  No matching entries found.").Color("gray"))
		return 1
	}
	for _, r := range slice {
		sender.SendMessage(plugin.Plain("> ").Color("dark_aqua").Append(
			plugin.Plain(fmt.Sprintf("[%s] %s: ", r.HolderType, r.HolderName)).Color("aqua"),
			plugin.Plain(r.Node.Key).Color("white"),
			plugin.Plain(" (").Color("gray"),
			core.FormatBool(r.Node.Value),
			plugin.Plain(")").Color("gray"),
			core.FormatContexts(r.Node.Contexts),
		))
	}
	return 1
}

func (m *Manager) cmdVerbose(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermVerbose) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s verbose <on|record|off|paste|upload|command> [filter...]", label))
		return 0
	}
	mode := strings.ToLower(args[0])
	rest := args[1:]

	switch mode {
	case "on", "true", "record":
		notify := mode != "record"
		filterExpr := strings.Join(rest, " ")
		filter, err := core.CompileVerboseFilter(filterExpr)
		if err != nil {
			core.SendError(sender, "Invalid verbose filter: "+err.Error())
			return 0
		}
		m.lp.Verbose.Register(sender, filter, notify)
		if notify {
			core.SendInfo(sender, fmt.Sprintf("Verbose logging enabled with filter: %s.", filter.String()))
		} else {
			core.SendInfo(sender, fmt.Sprintf("Verbose recording enabled with filter: %s.", filter.String()))
		}
		return 1

	case "off", "false":
		vl := m.lp.Verbose.Unregister(sender)
		if vl == nil {
			core.SendError(sender, "Verbose logging is not currently enabled.")
			return 0
		}
		_, matched, total := vl.Snapshot()
		core.SendInfo(sender, fmt.Sprintf("Verbose logging disabled (matched %d of %d checks).", matched, total))
		return 1

	case "paste":
		vl := m.lp.Verbose.Get(sender)
		if vl == nil {
			core.SendError(sender, "Verbose logging is not currently enabled.")
			return 0
		}
		events, matched, total := vl.Snapshot()
		outName := fmt.Sprintf("verbose-%s.txt", time.Now().Format("20060102-150405"))
		outPath := filepath.Join(m.lp.DataDir(), outName)
		var sb strings.Builder
		fmt.Fprintf(&sb, "# LuckPerms Verbose Log (%d matched / %d total, filter=%s)\n", matched, total, vl.Filter.String())
		for _, ev := range events {
			fmt.Fprintf(&sb, "[%s] %s | %s | %s | processor=%s | contexts=%s\n",
				ev.Timestamp.Format(time.RFC3339), ev.Target, ev.Permission, ev.Result.Result, ev.Result.Processor, ev.Contexts.String())
		}
		if err := os.WriteFile(outPath, []byte(sb.String()), 0o644); err != nil {
			core.SendError(sender, "Failed to write verbose log: "+err.Error())
			return 0
		}
		core.SendInfo(sender, fmt.Sprintf("Saved %d verbose events to %s.", len(events), outPath))
		return 1

	case "upload":
		vl := m.lp.Verbose.Unregister(sender)
		if vl == nil {
			core.SendError(sender, "Verbose logging is not currently enabled.")
			return 0
		}
		events, matched, total := vl.Snapshot()
		data := make([]map[string]any, len(events))
		for i, ev := range events {
			entry := map[string]any{
				"who": map[string]string{
					"type":       string(ev.TargetType),
					"identifier": ev.Target,
				},
				"permission": ev.Permission,
				"result":     ev.Result.Result.String(),
				"origin":     string(ev.Origin),
				"context":    core.ContextSetToJSONMap(ev.Contexts),
			}
			if ev.Result.Processor != "" {
				entry["processor"] = ev.Result.Processor
			}
			if ev.Result.Node != nil {
				entry["resultInfo"] = ev.Result.Node.ToJSON()
			}
			data[i] = entry
		}
		payload := map[string]any{
			"metadata": map[string]any{
				"startTime":     vl.Started.UnixMilli(),
				"endTime":       time.Now().UnixMilli(),
				"duration":      core.FormatDuration(time.Since(vl.Started)),
				"count":         map[string]int{"matched": matched, "total": total},
				"uploader":      map[string]string{"name": sender.Name()},
				"filter":        vl.Filter.String(),
				"pluginVersion": core.Version,
				"platform":      "golem",
			},
			"data": data,
		}
		key, err := m.lp.Bytebin.PostJSON(payload, "verbose")
		if err != nil {
			core.SendError(sender, "Failed to upload verbose log: "+err.Error())
			return 0
		}
		url := m.lp.Config().VerboseViewerURL + key
		core.SendPrefixed(sender,
			plugin.Plain("Verbose output uploaded: ").Color("green"),
			plugin.Plain(url).Color("aqua").Underlined().OnClick(plugin.OpenURL, url),
		)
		return 1

	case "command":
		if len(rest) < 2 {
			core.SendError(sender, fmt.Sprintf("Usage: /%s verbose command <player> <command...>", label))
			return 0
		}
		target, ok := m.lp.Server.PlayerNamed(rest[0])
		if !ok {
			core.SendError(sender, "Player "+rest[0]+" is not online.")
			return 0
		}
		if sp, isP := sender.(plugin.Player); isP && sp.UUID() != target.UUID() {
			if !m.requirePerm(sender, core.PermVerboseCommandOthers) {
				return 0
			}
		}
		cmdLine := strings.TrimPrefix(strings.Join(rest[1:], " "), "/")
		filter, _ := core.CompileVerboseFilter(target.Name())
		prev := m.lp.Verbose.Get(sender)
		m.lp.Verbose.Register(sender, filter, true)
		core.SendInfo(sender, fmt.Sprintf("Forcing %s to run '/%s' with verbose enabled...", target.Name(), cmdLine))
		target.RunCommand(cmdLine)
		if prev != nil {
			m.lp.Verbose.Register(sender, prev.Filter, prev.Notify)
		} else {
			m.lp.Verbose.Unregister(sender)
		}
		return 1

	default:
		core.SendError(sender, fmt.Sprintf("Usage: /%s verbose <on|record|off|paste|upload|command> [filter...]", label))
		return 0
	}
}

func (m *Manager) cmdTree(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermTree) {
		return 0
	}
	uploadWeb := false
	var posArgs []string
	for _, a := range args {
		if strings.EqualFold(a, "--web") {
			uploadWeb = true
			continue
		}
		posArgs = append(posArgs, a)
	}
	scope := "."
	if len(posArgs) >= 1 {
		scope = posArgs[0]
	}
	var checkFn func(string) core.Tristate
	var targetName string
	if len(posArgs) >= 2 {
		targetName = posArgs[1]
		if p, ok := m.lp.Server.PlayerNamed(targetName); ok {
			if u := m.lp.Users.GetIfLoaded(p.UUID()); u != nil {
				qo := m.lp.Contexts.QueryOptionsForPlayer(p)
				pd := u.CachedData().PermissionData(qo)
				checkFn = func(node string) core.Tristate {
					return pd.CheckPermission(node, core.OriginInternal).Result
				}
			}
		} else {
			core.SendError(sender, "Player "+targetName+" is not online.")
			return 0
		}
	}

	lines := m.lp.Registry.BuildTree(scope, 6, checkFn)
	if len(lines) == 0 {
		core.SendError(sender, "No permissions found in tree for scope "+scope+".")
		return 0
	}

	if uploadWeb {
		nodes := make([]map[string]any, len(lines))
		for i, l := range lines {
			entry := map[string]any{"node": l.FullNode, "depth": l.Indent}
			if l.HasCheck {
				entry["result"] = l.CheckState.String()
			}
			nodes[i] = entry
		}
		payload := map[string]any{
			"metadata": map[string]any{
				"uploader":      map[string]string{"name": sender.Name()},
				"time":          time.Now().UnixMilli(),
				"root":          scope,
				"referenceUser": targetName,
				"pluginVersion": core.Version,
			},
			"data": nodes,
		}
		key, err := m.lp.Bytebin.PostJSON(payload, "treeview")
		if err != nil {
			core.SendError(sender, "Failed to upload permission tree: "+err.Error())
			return 0
		}
		url := m.lp.Config().TreeViewerURL + key
		core.SendPrefixed(sender,
			plugin.Plain("Tree view uploaded: ").Color("green"),
			plugin.Plain(url).Color("aqua").Underlined().OnClick(plugin.OpenURL, url),
		)
		return 1
	}

	header := fmt.Sprintf("Permission Tree (%s - %d nodes)", scope, len(lines))
	if targetName != "" {
		header += " for " + targetName
	}
	core.SendInfo(sender, header)

	maxLines := min(len(lines), 40)
	for i := range maxLines {
		l := lines[i]
		indent := strings.Repeat("  ", l.Indent)
		txt := plugin.Plain(indent + "|- " + l.Label).Color("gray")
		if l.HasCheck {
			txt = txt.Append(plugin.Plain(" (").Color("dark_gray"), core.FormatTristate(l.CheckState), plugin.Plain(")").Color("dark_gray"))
		}
		sender.SendMessage(txt)
	}
	if len(lines) > maxLines {
		sender.SendMessage(plugin.Plain(fmt.Sprintf("  ... and %d more nodes (use a narrower scope or --web)", len(lines)-maxLines)).Color("gray"))
	}
	return 1
}

func (m *Manager) cmdBulkUpdate(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermBulkUpdate) {
		return 0
	}
	cfg := m.lp.Config()
	if cfg.DisableBulkUpdate {
		core.SendError(sender, "The bulkupdate command is disabled in config.yml.")
		return 0
	}
	if !core.IsConsole(sender) {
		core.SendError(sender, "The bulkupdate command can only be run from the console.")
		return 0
	}
	if len(args) == 2 && strings.EqualFold(args[0], "confirm") {
		id := args[1]
		m.bulkMu.Lock()
		entry, ok := m.pendingBulk[id]
		if ok {
			delete(m.pendingBulk, id)
		}
		m.bulkMu.Unlock()
		if !ok || time.Now().After(entry.expiresAt) {
			core.SendError(sender, "Unknown or expired bulkupdate confirmation ID: "+id)
			return 0
		}
		return m.runBulkUpdate(sender, entry.bu)
	}

	if len(args) < 2 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s bulkupdate <all|users|groups> <delete|update <field> <value>> [\"<field> <cmp> <value>\"...]", label))
		return 0
	}

	bu := &core.BulkUpdate{TrackStatistics: true}
	var filtered []string
	for _, a := range args {
		if a == "-s" {
			bu.TrackStatistics = false
			continue
		}
		filtered = append(filtered, a)
	}
	if len(filtered) < 2 {
		core.SendError(sender, "Not enough arguments for bulkupdate.")
		return 0
	}

	switch strings.ToUpper(filtered[0]) {
	case "ALL":
		bu.DataType = core.BulkDataAll
	case "USERS":
		bu.DataType = core.BulkDataUsers
	case "GROUPS":
		bu.DataType = core.BulkDataGroups
	default:
		core.SendError(sender, "Invalid data type (expected all, users, or groups): "+filtered[0])
		return 0
	}

	idx := 2
	switch strings.ToLower(filtered[1]) {
	case "delete":
		bu.Action = core.BulkActionDelete
	case "update":
		if len(filtered) < 4 {
			core.SendError(sender, "Usage: bulkupdate <type> update <permission|server|world> <value> [constraints...]")
			return 0
		}
		f, ok := core.ParseBulkField(filtered[2])
		if !ok {
			core.SendError(sender, "Invalid update field: "+filtered[2])
			return 0
		}
		bu.Action = core.BulkActionUpdate
		bu.UpdateField = f
		bu.UpdateValue = filtered[3]
		idx = 4
	default:
		core.SendError(sender, "Invalid bulkupdate action (expected delete or update): "+filtered[1])
		return 0
	}

	for _, cStr := range filtered[idx:] {
		parts := strings.Fields(cStr)
		if len(parts) != 3 {
			core.SendError(sender, fmt.Sprintf("Invalid constraint %q (expected \"<field> <==|!=|~~|!~> <value>\").", cStr))
			return 0
		}
		f, ok := core.ParseBulkField(parts[0])
		if !ok {
			core.SendError(sender, "Invalid constraint field: "+parts[0])
			return 0
		}
		op, ok := core.ParseBulkComparison(parts[1])
		if !ok {
			core.SendError(sender, "Invalid comparison operator: "+parts[1])
			return 0
		}
		bu.Constraints = append(bu.Constraints, core.BulkConstraint{
			Field:      f,
			Comparison: op,
			Value:      parts[2],
		})
	}

	if cfg.SkipBulkUpdateConfirmation {
		return m.runBulkUpdate(sender, bu)
	}

	id := fmt.Sprintf("%04d", rand.IntN(10000))
	m.bulkMu.Lock()
	m.pendingBulk[id] = pendingBulkEntry{bu: bu, expiresAt: time.Now().Add(30 * time.Second)}
	m.bulkMu.Unlock()

	core.SendInfo(sender, "Bulk update operation queued: "+bu.ReadableSummary())
	core.SendInfo(sender, fmt.Sprintf("Run '/%s bulkupdate confirm %s' within 30 seconds to execute.", label, id))
	return 1
}

func (m *Manager) runBulkUpdate(sender plugin.CommandSender, bu *core.BulkUpdate) int {
	core.SendInfo(sender, "Running bulk update...")
	if err := m.lp.Storage.ApplyBulkUpdate(bu); err != nil {
		core.SendError(sender, "Bulk update failed: "+err.Error())
		return 0
	}
	_ = m.lp.Sync()
	m.lp.PushUpdateAsync()
	core.SendInfo(sender, "Bulk update completed successfully.")
	if bu.TrackStatistics {
		core.SendInfo(sender, fmt.Sprintf("Affected %d node(s), %d user(s), and %d group(s).",
			bu.Stats.AffectedNodes, bu.Stats.AffectedUsers, bu.Stats.AffectedGroups))
	}
	return 1
}

func (m *Manager) cmdExport(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermExport) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s export <file|--upload> [--without-users] [--without-groups]", label))
		return 0
	}
	includeUsers := true
	includeGroups := true
	upload := false
	fileName := ""
	for _, f := range args {
		switch strings.ToLower(f) {
		case "--without-users":
			includeUsers = false
		case "--without-groups":
			includeGroups = false
		case "--upload", "--web":
			upload = true
		default:
			if fileName == "" {
				fileName = f
			}
		}
	}
	if upload {
		key, err := m.lp.ExportToBytebin(sender.Name(), includeUsers, includeGroups)
		if err != nil {
			core.SendError(sender, "Web export failed: "+err.Error())
			return 0
		}
		core.SendInfo(sender, fmt.Sprintf("Successfully uploaded export to bytebin (code: %s). Import with '/%s import --web %s'.", key, label, key))
		return 1
	}
	if fileName == "" {
		core.SendError(sender, "Missing export filename.")
		return 0
	}
	path, err := m.lp.ExportToFile(fileName, sender.Name(), includeUsers, includeGroups)
	if err != nil {
		core.SendError(sender, "Export failed: "+err.Error())
		return 0
	}
	core.SendInfo(sender, "Successfully exported data to "+path+".")
	return 1
}

func (m *Manager) cmdImport(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermImport) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s import <file|--web code> [--replace]", label))
		return 0
	}
	replace := false
	fromWeb := false
	target := ""
	for _, f := range args {
		switch strings.ToLower(f) {
		case "--replace":
			replace = true
		case "--web":
			fromWeb = true
		default:
			if target == "" {
				target = f
			}
		}
	}
	if target == "" {
		core.SendError(sender, "Missing import filename or bytebin code.")
		return 0
	}
	var summary core.ImportSummary
	var err error
	if fromWeb {
		summary, err = m.lp.ImportFromBytebin(target, replace)
	} else {
		summary, err = m.lp.ImportFromFile(target, replace)
	}
	if err != nil {
		core.SendError(sender, "Import failed: "+err.Error())
		return 0
	}
	m.lp.PushUpdateAsync()
	core.SendInfo(sender, fmt.Sprintf("Import completed: %d group(s), %d track(s), %d user(s).", summary.Groups, summary.Tracks, summary.Users))
	return 1
}

// --- Web Editor commands (/lp editor, /lp applyedits, /lp trusteditor) ---

func (m *Manager) cmdEditor(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermEditor) {
		return 0
	}
	mode := "all"
	filter := ""
	if len(args) >= 1 {
		switch strings.ToLower(args[0]) {
		case "all", "online", "users", "groups":
			mode = strings.ToLower(args[0])
			if len(args) >= 2 {
				filter = strings.ToLower(args[1])
			}
		default:
			filter = strings.ToLower(args[0])
		}
	}

	includeGroups := mode == "all" || mode == "online" || mode == "groups"
	includeUsers := mode == "all" || mode == "online" || mode == "users"
	includeOffline := mode == "all" || mode == "users"

	var groups []*core.Group
	var tracks []*core.Track
	if includeGroups {
		groups = m.lp.Groups.All()
		tracks = m.lp.Tracks.All()
	}

	var users []*core.User
	if includeUsers {
		seen := make(map[plugin.UUID]bool)
		addUser := func(u *core.User) {
			if seen[u.UUID()] {
				return
			}
			if filter != "" {
				matched := false
				for _, n := range u.NormalNodes() {
					if strings.HasPrefix(strings.ToLower(n.Key), filter) {
						matched = true
						break
					}
				}
				if !matched {
					return
				}
			}
			seen[u.UUID()] = true
			users = append(users, u)
		}
		for _, u := range m.lp.Users.Loaded() {
			addUser(u)
		}
		if includeOffline && len(users) < 500 {
			if ids, err := m.lp.Storage.GetUniqueUsers(); err == nil {
				for _, id := range ids {
					if len(users) >= 500 {
						break
					}
					if seen[id] {
						continue
					}
					wasLoaded := m.lp.Users.GetIfLoaded(id) != nil
					if u, err := m.lp.Storage.LoadUser(id, ""); err == nil {
						addUser(u)
						if !wasLoaded {
							m.lp.Users.Unload(id)
						}
					}
				}
			}
		}
	}

	return m.openEditorSession(sender, label, groups, users, tracks)
}

func (m *Manager) openEditorSession(sender plugin.CommandSender, label string, groups []*core.Group, users []*core.User, tracks []*core.Track) int {
	core.SendInfo(sender, "Preparing a new editor session, please wait...")
	_, url, err := m.lp.CreateWebEditorSession(sender, label, groups, users, tracks, true)
	if err != nil {
		core.SendError(sender, "Failed to open web editor: "+err.Error())
		return 0
	}
	core.SendPrefixed(sender,
		plugin.Plain("Click the link below to open the editor: ").Color("green"),
		plugin.Plain(url).Color("aqua").Underlined().OnClick(plugin.OpenURL, url),
	)
	return 1
}

func (m *Manager) cmdApplyEdits(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermApplyEdits) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s applyedits <code> [--force]", label))
		return 0
	}
	force := false
	code := ""
	for _, a := range args {
		if strings.EqualFold(a, "--force") {
			force = true
		} else if code == "" {
			code = a
		}
	}
	if code == "" {
		core.SendError(sender, "Invalid applyedits code.")
		return 0
	}
	var payload core.WebEditorResponsePayload
	if err := m.lp.Bytebin.GetJSON(code, &payload); err != nil {
		core.SendError(sender, "Failed to read editor data from bytebin: "+err.Error())
		return 0
	}
	if err := m.lp.ApplyWebEditorResponse(sender, code, &payload, label, force); err != nil {
		core.SendError(sender, err.Error())
		return 0
	}
	return 1
}

func (m *Manager) cmdTrustEditor(sender plugin.CommandSender, label string, args []string) int {
	if !m.requirePerm(sender, core.PermTrustEditor) {
		return 0
	}
	if len(args) < 1 {
		core.SendError(sender, fmt.Sprintf("Usage: /%s trusteditor <id>", label))
		return 0
	}
	sock := m.lp.WebEditor.GetSocket(sender)
	if sock == nil || !sock.TrustConnection(args[0]) {
		core.SendError(sender, "Unable to trust the given editor session.")
		return 0
	}
	core.SendInfo(sender, "The editor session has been marked as trusted.")
	return 1
}
