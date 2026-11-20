// Package commands implements LuckPerms's /luckperms (/lp) command tree and tab completion.
package commands

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golem-mc/golem/plugin"
	"github.com/golem-mc/perms/core"
)

// Manager owns the /luckperms command registration and dispatch.
type Manager struct {
	lp *core.LuckPerms

	bulkMu      sync.Mutex
	pendingBulk map[string]pendingBulkEntry

	rateMu    sync.Mutex
	rateLimit map[plugin.UUID]time.Time
}

type pendingBulkEntry struct {
	bu        *core.BulkUpdate
	expiresAt time.Time
}

// Register registers /luckperms (and aliases lp, perm, perms, permission, permissions) with the server.
func Register(lp *core.LuckPerms) *Manager {
	m := &Manager{
		lp:          lp,
		pendingBulk: make(map[string]pendingBulkEntry),
		rateLimit:   make(map[plugin.UUID]time.Time),
	}

	cfg := lp.Config()
	if cfg.DisablePlayers && cfg.DisableConsole {
		return m
	}

	run := func(ctx *plugin.CommandContext) (int, error) {
		rawArgs := plugin.Arg[string](ctx, "args")
		label := extractLabel(ctx.Input)
		args := TokenizeArgs(rawArgs)
		return m.Execute(ctx.Sender, label, args), nil
	}

	cmd := plugin.Command{
		Name:        "luckperms",
		Aliases:     []string{"lp", "perm", "perms", "permission", "permissions"},
		Description: "Manage permissions",
		Usage:       "/lp <subcommand>",
		Run:         run,
		Children: []plugin.Node{{
			Arg: plugin.GreedyArg("args"),
			Run: run,
			Suggest: func(ctx *plugin.CommandContext, partial string) []string {
				return m.Complete(ctx.Sender, partial)
			},
		}},
	}
	if err := lp.Server.Commands().Register(cmd); err != nil {
		lp.Log.Error("Failed to register /luckperms command", "err", err)
	}
	return m
}

func extractLabel(input string) string {
	first, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(input), "/"), " ")
	if _, after, ok := strings.Cut(first, ":"); ok {
		first = after
	}
	if first == "" {
		return "lp"
	}
	return first
}

// TokenizeArgs splits a LuckPerms command line respecting quotes ("...") and {SPACE}.
func TokenizeArgs(s string) []string {
	var args []string
	var cur strings.Builder
	inQuote := false
	hasToken := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			inQuote = !inQuote
			hasToken = true
			continue
		}
		if !inQuote && (c == ' ' || c == '\t') {
			if hasToken {
				args = append(args, strings.ReplaceAll(cur.String(), "{SPACE}", " "))
				cur.Reset()
				hasToken = false
			}
			continue
		}
		if c == '\\' && i+1 < len(s) && s[i+1] == '"' {
			cur.WriteByte('"')
			i++
			hasToken = true
			continue
		}
		cur.WriteByte(c)
		hasToken = true
	}
	if hasToken {
		args = append(args, strings.ReplaceAll(cur.String(), "{SPACE}", " "))
	}
	return args
}

// applyConvenienceAliases expands LuckPerms short aliases (u/g/t/i/p/perm/group/m/e).
func applyConvenienceAliases(args []string, rewriteLast bool) []string {
	out := slices.Clone(args)
	if len(out) >= 1 && (rewriteLast || len(out) >= 2) {
		switch strings.ToLower(out[0]) {
		case "u":
			out[0] = "user"
		case "g":
			out[0] = "group"
		case "t":
			out[0] = "track"
		case "i":
			out[0] = "info"
		}
	}
	if len(out) >= 3 && (rewriteLast || len(out) >= 4) {
		a0 := strings.ToLower(out[0])
		if a0 == "user" || a0 == "group" {
			switch strings.ToLower(out[2]) {
			case "p", "perm":
				out[2] = "permission"
			case "g", "group":
				out[2] = "parent"
			case "m":
				out[2] = "meta"
			case "i":
				out[2] = "info"
			case "e":
				out[2] = "editor"
			}
			if len(out) >= 4 && (rewriteLast || len(out) >= 5) {
				a2 := strings.ToLower(out[2])
				if (a2 == "permission" || a2 == "parent" || a2 == "meta") && strings.EqualFold(out[3], "i") {
					out[3] = "info"
				}
			}
		}
	}
	return out
}

// Execute runs a parsed LuckPerms command and returns 1 on success, 0 on failure.
func (m *Manager) Execute(sender plugin.CommandSender, label string, rawArgs []string) int {
	cfg := m.lp.Config()
	if core.IsConsole(sender) {
		if cfg.DisableConsole {
			core.SendError(sender, "LuckPerms commands are disabled for the console.")
			return 0
		}
	} else {
		if cfg.DisablePlayers {
			core.SendError(sender, "LuckPerms commands are disabled for players.")
			return 0
		}
		if cfg.CommandsRateLimit {
			p := sender.(plugin.Player)
			m.rateMu.Lock()
			if last, ok := m.rateLimit[p.UUID()]; ok && time.Since(last) < 500*time.Millisecond {
				m.rateMu.Unlock()
				return 0
			}
			m.rateLimit[p.UUID()] = time.Now()
			m.rateMu.Unlock()
		}
	}

	args := applyConvenienceAliases(rawArgs, true)
	if len(args) == 0 {
		m.sendBanner(sender, label)
		return 1
	}

	sub := strings.ToLower(args[0])
	rest := args[1:]

	switch sub {
	case "user":
		return m.cmdUser(sender, label, rest)
	case "group":
		return m.cmdGroup(sender, label, rest)
	case "track":
		return m.cmdTrack(sender, label, rest)
	case "log":
		return m.cmdLog(sender, label, rest)
	case "creategroup":
		return m.cmdCreateGroup(sender, label, rest)
	case "deletegroup":
		return m.cmdDeleteGroup(sender, label, rest)
	case "listgroups":
		return m.cmdListGroups(sender, label, rest)
	case "createtrack":
		return m.cmdCreateTrack(sender, label, rest)
	case "deletetrack":
		return m.cmdDeleteTrack(sender, label, rest)
	case "listtracks":
		return m.cmdListTracks(sender, label, rest)
	case "info":
		return m.cmdInfo(sender, label)
	case "sync":
		return m.cmdSync(sender, label)
	case "networksync":
		return m.cmdNetworkSync(sender, label)
	case "reloadconfig":
		return m.cmdReloadConfig(sender, label)
	case "translations":
		return m.cmdTranslations(sender, label, rest)
	case "search":
		return m.cmdSearch(sender, label, rest)
	case "check":
		return m.cmdQuickCheck(sender, label, rest)
	case "verbose":
		return m.cmdVerbose(sender, label, rest)
	case "tree":
		return m.cmdTree(sender, label, rest)
	case "bulkupdate":
		return m.cmdBulkUpdate(sender, label, rest)
	case "export":
		return m.cmdExport(sender, label, rest)
	case "import":
		return m.cmdImport(sender, label, rest)
	case "editor":
		return m.cmdEditor(sender, label, rest)
	case "applyedits":
		return m.cmdApplyEdits(sender, label, rest)
	case "trusteditor":
		return m.cmdTrustEditor(sender, label, rest)
	case "help", "?":
		m.sendMainUsage(sender, label)
		return 1
	default:
		m.sendMainUsage(sender, label)
		return 0
	}
}

func (m *Manager) requirePerm(sender plugin.CommandSender, perm core.CommandPerm) bool {
	if !m.lp.CanUseCommand(sender, perm) {
		core.SendError(sender, "You do not have permission to use this command.")
		return false
	}
	return true
}

func (m *Manager) sendBanner(sender plugin.CommandSender, label string) {
	core.SendPrefixed(sender,
		plugin.Plain("Running ").Color("dark_green"),
		plugin.Plain("LuckPerms").Color("aqua"),
		plugin.Plain(" v"+core.Version).Color("aqua"),
		plugin.Plain(".").Color("dark_green"),
	)
	if m.lp.HasAnyCommandPermission(sender) {
		core.SendPrefixed(sender,
			plugin.Plain("Use ").Color("green"),
			plugin.Plain("/"+label+" help").Color("aqua").OnClick(plugin.RunCommand, "/"+label+" help"),
			plugin.Plain(" to view available commands.").Color("green"),
		)
		return
	}
	groups := m.lp.Groups.All()
	if len(groups) <= 1 && (len(groups) == 0 || len(groups[0].NormalNodes()) == 0) {
		cmd := fmt.Sprintf("lp user %s permission set luckperms.* true", sender.Name())
		core.SendPrefixed(sender,
			plugin.Plain("It seems that no permissions have been setup yet! Run ").Color("gray"),
			plugin.Plain(cmd).Color("aqua").OnClick(plugin.CopyToClipboard, cmd),
			plugin.Plain(" from the console to grant yourself full access.").Color("gray"),
		)
	} else {
		core.SendError(sender, "You do not have permission to use any LuckPerms commands.")
	}
}

func (m *Manager) sendMainUsage(sender plugin.CommandSender, label string) {
	core.SendPrefixed(sender,
		plugin.Plain("Running ").Color("dark_green"),
		plugin.Plain("LuckPerms").Color("aqua"),
		plugin.Plain(" v"+core.Version).Color("aqua"),
		plugin.Plain(".").Color("dark_green"),
	)
	usages := []struct {
		perm core.CommandPerm
		line string
	}{
		{core.PermUserInfo, "/%s user <user> <subcommand>"},
		{core.PermGroupInfo, "/%s group <group> <subcommand>"},
		{core.PermTrackInfo, "/%s track <track> <subcommand>"},
		{core.PermCreateGroup, "/%s creategroup <group> [weight] [displayname]"},
		{core.PermDeleteGroup, "/%s deletegroup <group>"},
		{core.PermListGroups, "/%s listgroups"},
		{core.PermCreateTrack, "/%s createtrack <track>"},
		{core.PermDeleteTrack, "/%s deletetrack <track>"},
		{core.PermListTracks, "/%s listtracks"},
		{core.PermLogRecent, "/%s log <recent|userhistory|grouphistory|trackhistory|search|notify>"},
		{core.PermInfo, "/%s info"},
		{core.PermSync, "/%s sync"},
		{core.PermSync, "/%s networksync"},
		{core.PermReloadConfig, "/%s reloadconfig"},
		{core.PermEditor, "/%s editor [all|online|users|groups] [filter]"},
		{core.PermTranslations, "/%s translations [install]"},
		{core.PermSearch, "/%s search [comparison] <query> [page]"},
		{core.PermVerbose, "/%s verbose <on|record|off|paste|upload|command> [filter]"},
		{core.PermTree, "/%s tree [scope] [player] [--web]"},
		{core.PermBulkUpdate, "/%s bulkupdate <all|users|groups> <delete|update> ..."},
		{core.PermExport, "/%s export <file|--upload> [--without-users] [--without-groups]"},
		{core.PermImport, "/%s import <file|--web code> [--replace]"},
	}
	for _, u := range usages {
		if !m.lp.CanUseCommand(sender, u.perm) {
			continue
		}
		txt := fmt.Sprintf(u.line, label)
		sender.SendMessage(plugin.Plain("> ").Color("dark_aqua").Append(
			plugin.Plain(txt).Color("green").OnClick(plugin.SuggestCommand, txt),
		))
	}
}

// Complete provides Brigadier greedy-arg tab completion suggestions.
func (m *Manager) Complete(sender plugin.CommandSender, partial string) []string {
	cfg := m.lp.Config()
	if (core.IsConsole(sender) && cfg.DisableConsole) || (!core.IsConsole(sender) && cfg.DisablePlayers) {
		return nil
	}

	words := strings.Split(partial, " ")
	if len(words) == 0 {
		words = []string{""}
	}
	aliased := applyConvenienceAliases(words, false)
	last := strings.ToLower(aliased[len(aliased)-1])
	prefix := strings.Join(words[:len(words)-1], " ")
	if prefix != "" {
		prefix += " "
	}

	cands := m.candidates(sender, aliased)
	var out []string
	for _, c := range cands {
		if strings.HasPrefix(strings.ToLower(c), last) {
			out = append(out, prefix+c)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func (m *Manager) candidates(sender plugin.CommandSender, args []string) []string {
	if len(args) <= 1 {
		return []string{
			"user", "group", "track", "log",
			"creategroup", "deletegroup", "listgroups",
			"createtrack", "deletetrack", "listtracks",
			"info", "sync", "networksync", "reloadconfig", "translations", "search",
			"check", "verbose", "tree", "bulkupdate",
			"export", "import", "editor", "applyedits", "trusteditor", "help",
		}
	}
	sub := strings.ToLower(args[0])
	switch sub {
	case "user":
		return m.completeHolder(true, args[1:])
	case "group":
		return m.completeHolder(false, args[1:])
	case "deletegroup":
		if len(args) == 2 {
			return m.lp.Groups.Names()
		}
	case "deletetrack":
		if len(args) == 2 {
			return m.lp.Tracks.Names()
		}
	case "track":
		if len(args) == 2 {
			return m.lp.Tracks.Names()
		}
		if len(args) == 3 {
			return []string{"info", "editor", "append", "insert", "remove", "clear", "rename", "clone"}
		}
		if len(args) == 4 && (strings.EqualFold(args[2], "append") || strings.EqualFold(args[2], "insert") || strings.EqualFold(args[2], "remove")) {
			return m.lp.Groups.Names()
		}
	case "log":
		if len(args) == 2 {
			return []string{"recent", "userhistory", "grouphistory", "trackhistory", "search", "notify"}
		}
		if len(args) == 3 {
			switch strings.ToLower(args[1]) {
			case "notify":
				return []string{"on", "off"}
			case "userhistory", "recent":
				return m.onlinePlayerNames()
			case "grouphistory":
				return m.lp.Groups.Names()
			case "trackhistory":
				return m.lp.Tracks.Names()
			}
		}
	case "verbose":
		if len(args) == 2 {
			return []string{"on", "record", "off", "paste", "upload", "command"}
		}
	case "editor":
		if len(args) == 2 {
			return []string{"all", "online", "users", "groups"}
		}
	case "translations":
		if len(args) == 2 {
			return []string{"install"}
		}
	case "bulkupdate":
		if len(args) == 2 {
			return []string{"all", "users", "groups", "confirm"}
		}
		if len(args) == 3 && !strings.EqualFold(args[1], "confirm") {
			return []string{"delete", "update"}
		}
	case "search":
		if len(args) == 2 {
			return append([]string{"==", "!=", "~~", "!~"}, m.lp.Registry.All()...)
		}
	case "check":
		if len(args) == 2 {
			return m.onlinePlayerNames()
		}
		if len(args) == 3 {
			return m.lp.Registry.All()
		}
	case "tree":
		if len(args) == 2 {
			return []string{"."}
		}
		if len(args) == 3 {
			return m.onlinePlayerNames()
		}
	}
	return nil
}

func (m *Manager) completeHolder(isUser bool, args []string) []string {
	if len(args) == 1 {
		if isUser {
			return m.onlinePlayerNames()
		}
		return m.lp.Groups.Names()
	}
	if len(args) == 2 {
		if isUser {
			return []string{"info", "permission", "parent", "meta", "editor", "promote", "demote", "showtracks", "clear", "clone"}
		}
		return []string{"info", "permission", "parent", "meta", "editor", "listmembers", "setweight", "setdisplayname", "showtracks", "clear", "rename", "clone"}
	}
	action := strings.ToLower(args[1])
	switch action {
	case "permission":
		if len(args) == 3 {
			return []string{"info", "set", "unset", "settemp", "unsettemp", "check", "clear"}
		}
		if len(args) == 4 && !strings.EqualFold(args[2], "info") && !strings.EqualFold(args[2], "clear") {
			return m.lp.Registry.All()
		}
		if len(args) == 5 && (strings.EqualFold(args[2], "set") || strings.EqualFold(args[2], "settemp")) {
			return []string{"true", "false"}
		}
		return m.contextSuggestions()
	case "parent":
		if len(args) == 3 {
			opts := []string{"info", "set", "add", "remove", "settrack", "addtemp", "removetemp", "clear", "cleartrack"}
			if isUser {
				opts = append(opts, "switchprimarygroup")
			}
			return opts
		}
		if len(args) == 4 {
			switch strings.ToLower(args[2]) {
			case "set", "add", "remove", "addtemp", "removetemp", "switchprimarygroup":
				return m.lp.Groups.Names()
			case "settrack", "cleartrack":
				return m.lp.Tracks.Names()
			}
		}
		if len(args) == 5 && strings.EqualFold(args[2], "settrack") {
			return m.lp.Groups.Names()
		}
		return m.contextSuggestions()
	case "meta":
		if len(args) == 3 {
			return []string{
				"info", "set", "unset", "settemp", "unsettemp",
				"addprefix", "addsuffix", "setprefix", "setsuffix",
				"removeprefix", "removesuffix",
				"addtempprefix", "addtempsuffix", "settempprefix", "settempsuffix",
				"removetempprefix", "removetempsuffix", "clear",
			}
		}
		return m.contextSuggestions()
	case "promote", "demote":
		if len(args) == 3 {
			return m.lp.Tracks.Names()
		}
		return m.contextSuggestions()
	case "clone":
		if len(args) == 3 {
			if isUser {
				return m.onlinePlayerNames()
			}
			return m.lp.Groups.Names()
		}
	}
	return nil
}

func (m *Manager) onlinePlayerNames() []string {
	ps := m.lp.Server.Players()
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name()
	}
	return out
}

func (m *Manager) contextSuggestions() []string {
	entries := m.lp.Contexts.PotentialContexts().Entries()
	out := make([]string, 0, len(entries)+3)
	for _, e := range entries {
		out = append(out, e.Key+"="+e.Value)
	}
	out = append(out, "server=", "world=")
	return out
}

// --- Shared argument helpers ---

func parseTempModifier(s string) (core.TemporaryNodeMergeStrategy, bool) {
	switch strings.ToLower(s) {
	case "accumulate":
		return core.TempMergeAccumulate, true
	case "replace":
		return core.TempMergeReplace, true
	case "deny", "none":
		return core.TempMergeDeny, true
	default:
		return "", false
	}
}

func parseContextsOrDefault(lp *core.LuckPerms, args []string) core.ContextSet {
	if len(args) == 0 {
		return lp.Config().DefaultContexts
	}
	return core.ParseContextSet(args)
}

func parsePage(args []string, idx int) int {
	if idx < len(args) {
		if n, err := strconv.Atoi(args[idx]); err == nil && n >= 1 {
			return n
		}
	}
	return 1
}

func paginate[T any](items []T, page, pageSize int) (slice []T, curPage, totalPages int) {
	if pageSize <= 0 {
		pageSize = 8
	}
	totalPages = max(1, (len(items)+pageSize-1)/pageSize)
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * pageSize
	end := min(len(items), start+pageSize)
	if start >= len(items) {
		return nil, page, totalPages
	}
	return items[start:end], page, totalPages
}

func isValidGroupOrTrackName(name string) bool {
	if name == "" || len(name) > 36 {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

func describeContextSuffix(cs core.ContextSet) string {
	if cs.IsEmpty() {
		return ""
	}
	return " (" + cs.String() + ")"
}
