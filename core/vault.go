package core

import (
	"errors"
	"fmt"
	"strings"

	"github.com/golem-mc/golem/plugin"
	"github.com/golem-mc/vault"
)

// OriginThirdPartyAPI marks a check made through Vault.
const OriginThirdPartyAPI CheckOrigin = "THIRD_PARTY_API"

var (
	errVaultNPC     = fmt.Errorf("%w: unable to modify the permissions of NPC players", errors.ErrUnsupported)
	errVaultNoGroup = errors.New("luckperms: no such group")
	// errVaultUnchanged makes WithUser skip the save of a change that
	// changed nothing; the Vault call still succeeds.
	errVaultUnchanged = errors.New("luckperms: unchanged")
)

// vaultHook is LuckPerms as Vault's Permission and Chat services.
// LuckPerms: LuckPermsVaultPermission, LuckPermsVaultChat
type vaultHook struct{ lp *LuckPerms }

var (
	_ vault.Permission = vaultHook{}
	_ vault.Chat       = vaultHook{}
)

// provideVault provides the hook; LuckPerms: VaultHookManager.hook, at
// ServicePriority.High.
func (lp *LuckPerms) provideVault() {
	h := vaultHook{lp}
	vault.ProvidePermission(lp.Server, h, vault.PriorityHigh)
	vault.ProvideChat(lp.Server, h, vault.PriorityHigh)
}

func (vaultHook) Name() string           { return "LuckPerms" }
func (vaultHook) SuperPermsCompat() bool { return true }
func (vaultHook) GroupSupport() bool     { return true }

// isNPC reports whether id is an NPC's: LuckPerms' UniqueIdType "npc" is
// a version 2 UUID.
func isNPC(id plugin.UUID) bool { return id.Version() == 2 }

// world answers the world context value a Vault call names, "" for none.
func (v vaultHook) world(w plugin.World) string {
	if w == nil || v.lp.Config().VaultIgnoreWorld {
		return ""
	}
	return WorldName(w)
}

// vaultServer answers the server context Vault changes are made in.
func (v vaultHook) vaultServer() string {
	cfg := v.lp.Config()
	s := cfg.Server
	if cfg.UseVaultServer {
		s = cfg.VaultServer
	}
	if s == "" {
		return GlobalContextValue
	}
	return s
}

// queryOptions answers the options a Vault query of player (nil for a
// group's) in world runs with: the online player's contexts, or the static
// ones, with world in place of theirs. LuckPerms:
// LuckPermsVaultPermission.getQueryOptions
func (v vaultHook) queryOptions(player *plugin.UUID, world string) QueryOptions {
	lp := v.lp
	cfg := lp.Config()
	var online plugin.Player
	if player != nil {
		online, _ = lp.Server.Player(*player)
	}
	var m *MutableContextSet
	if online != nil {
		m = lp.Contexts.ContextsForPlayer(online).MutableCopy()
	} else {
		m = lp.Contexts.StaticContexts().MutableCopy()
	}
	if world != "" && (online == nil || !strings.EqualFold(world, WorldName(online.World()))) {
		m.RemoveAll(ContextKeyWorld)
		m.Add(ContextKeyWorld, strings.ToLower(world))
	}
	if cfg.UseVaultServer {
		m.Remove(ContextKeyServer, cfg.Server)
		if cfg.VaultServer != GlobalContextValue {
			m.Add(ContextKeyServer, cfg.VaultServer)
		}
	}
	qo := lp.defaultQueryOptions(m.Immutable())
	qo.Flags.IncludeNodesWithoutServerContext = cfg.VaultIncludeGlobal
	switch {
	case online != nil:
		qo.Op = online.OpLevel() > 0 && cfg.EnableOps
	case player != nil && isNPC(*player):
		qo.Op = cfg.VaultNPCOpStatus
	}
	return qo
}

// nodeIn answers key as a node in the Vault server and world contexts.
func (v vaultHook) nodeIn(key string, world string) Node {
	return v.contexts(ParseNode(key, true), world)
}

func (v vaultHook) contexts(n Node, world string) Node {
	if world == "" {
		world = GlobalContextValue
	}
	return n.WithContexts(NewContextSet(Context{ContextKeyServer, v.vaultServer()}, Context{ContextKeyWorld, world}))
}

// query runs fn on player's holder: the loaded user, the NPC group, or the
// user loaded from storage for the call. LuckPerms: lookupUser
func (v vaultHook) query(player plugin.UUID, fn func(h *PermissionHolder)) {
	if isNPC(player) {
		if g := v.npcGroup(); g != nil {
			fn(&g.PermissionHolder)
		}
		return
	}
	_ = v.lp.WithUser(player, "", false, func(u *User) error {
		fn(&u.PermissionHolder)
		return nil
	})
}

func (v vaultHook) npcGroup() *Group {
	if g := v.lp.Groups.GetIfLoaded(strings.ToLower(v.lp.Config().VaultNPCGroup)); g != nil {
		return g
	}
	return v.lp.Groups.GetIfLoaded(DefaultGroupName)
}

// change runs fn on player's user and saves it when fn reports a change.
func (v vaultHook) change(player plugin.UUID, fn func(h *PermissionHolder) bool) error {
	if isNPC(player) {
		return errVaultNPC
	}
	err := v.lp.WithUser(player, "", true, func(u *User) error {
		if !fn(&u.PermissionHolder) {
			return errVaultUnchanged
		}
		return nil
	})
	if errors.Is(err, errVaultUnchanged) {
		return nil
	}
	return err
}

// changeGroup runs fn on group and saves it when fn reports a change.
func (v vaultHook) changeGroup(name string, fn func(h *PermissionHolder) bool) error {
	g := v.group(name)
	if g == nil {
		return errVaultNoGroup
	}
	if !fn(&g.PermissionHolder) {
		return nil
	}
	return v.lp.Storage.SaveGroup(g)
}

// group answers the group named name, or whose display name is name.
// LuckPerms: GroupManager.getByDisplayName
func (v vaultHook) group(name string) *Group {
	if g := v.lp.Groups.GetIfLoaded(strings.ToLower(name)); g != nil {
		return g
	}
	qo := v.lp.Contexts.StaticQueryOptions()
	for _, g := range v.lp.Groups.All() {
		if dn := g.DisplayName(qo); dn != "" && strings.EqualFold(dn, name) {
			return g
		}
	}
	return nil
}

// groupName answers the name Vault calls g by.
func (v vaultHook) groupName(g *Group) string {
	if v.lp.Config().VaultGroupUseDisplayNames {
		return g.FriendlyDisplayName(v.lp.Contexts.StaticQueryOptions())
	}
	return g.Name()
}

// groupKey answers the real name of the group Vault names, for its
// inheritance node.
func (v vaultHook) groupKey(name string) string {
	if g := v.group(name); g != nil {
		return g.Name()
	}
	return name
}

func (v vaultHook) PlayerHas(w plugin.World, player plugin.UUID, node string) (has bool) {
	qo := v.queryOptions(&player, v.world(w))
	v.query(player, func(h *PermissionHolder) {
		has = h.CachedData().PermissionData(qo).CheckPermission(node, OriginThirdPartyAPI).Result.AsBool()
	})
	return has
}

func (v vaultHook) PlayerAdd(w plugin.World, player plugin.UUID, node string) error {
	return v.add(w, player, node, DataNormal)
}

func (v vaultHook) PlayerRemove(w plugin.World, player plugin.UUID, node string) error {
	return v.remove(w, player, node, DataNormal)
}

func (v vaultHook) PlayerAddTransient(w plugin.World, player plugin.UUID, node string) error {
	return v.add(w, player, node, DataTransient)
}

func (v vaultHook) PlayerRemoveTransient(w plugin.World, player plugin.UUID, node string) error {
	return v.remove(w, player, node, DataTransient)
}

func (v vaultHook) add(w plugin.World, player plugin.UUID, node string, dt DataType) error {
	if node == "" {
		return errors.New("luckperms: empty permission")
	}
	n := v.nodeIn(node, v.world(w))
	return v.change(player, func(h *PermissionHolder) bool { return h.SetNode(dt, n).WasSuccessful() })
}

func (v vaultHook) remove(w plugin.World, player plugin.UUID, node string, dt DataType) error {
	if node == "" {
		return errors.New("luckperms: empty permission")
	}
	n := v.nodeIn(node, v.world(w))
	return v.change(player, func(h *PermissionHolder) bool { return h.UnsetNode(dt, n).WasSuccessful() })
}

func (v vaultHook) GroupHas(w plugin.World, group, node string) bool {
	g := v.group(group)
	if g == nil {
		return false
	}
	qo := v.queryOptions(nil, v.world(w))
	return g.CachedData().PermissionData(qo).CheckPermission(node, OriginThirdPartyAPI).Result.AsBool()
}

func (v vaultHook) GroupAdd(w plugin.World, group, node string) error {
	n := v.nodeIn(node, v.world(w))
	return v.changeGroup(group, func(h *PermissionHolder) bool { return h.SetNode(DataNormal, n).WasSuccessful() })
}

func (v vaultHook) GroupRemove(w plugin.World, group, node string) error {
	n := v.nodeIn(node, v.world(w))
	return v.changeGroup(group, func(h *PermissionHolder) bool { return h.UnsetNode(DataNormal, n).WasSuccessful() })
}

// PlayerInGroup counts only a direct membership, as LuckPerms does: the
// inheritance node decided by the holder's own nodes.
func (v vaultHook) PlayerInGroup(w plugin.World, player plugin.UUID, group string) (in bool) {
	qo := v.queryOptions(&player, v.world(w))
	key := "group." + v.groupKey(group)
	v.query(player, func(h *PermissionHolder) {
		res := h.CachedData().PermissionData(qo).CheckPermission(key, OriginThirdPartyAPI)
		in = res.Processor == (&DirectProcessor{}).Name() && res.Result.AsBool()
	})
	return in
}

func (v vaultHook) PlayerAddGroup(w plugin.World, player plugin.UUID, group string) error {
	if v.group(group) == nil {
		return errVaultNoGroup
	}
	return v.PlayerAdd(w, player, "group."+v.groupKey(group))
}

func (v vaultHook) PlayerRemoveGroup(w plugin.World, player plugin.UUID, group string) error {
	if v.group(group) == nil {
		return errVaultNoGroup
	}
	return v.PlayerRemove(w, player, "group."+v.groupKey(group))
}

func (v vaultHook) PlayerGroups(w plugin.World, player plugin.UUID) (out []string) {
	qo := v.queryOptions(&player, v.world(w))
	v.query(player, func(h *PermissionHolder) {
		for _, n := range h.OwnInheritanceNodes(qo) {
			if g := v.lp.Groups.GetIfLoaded(n.GroupName); g != nil {
				out = append(out, v.groupName(g))
			} else {
				out = append(out, n.GroupName)
			}
		}
	})
	return out
}

func (v vaultHook) PrimaryGroup(w plugin.World, player plugin.UUID) string {
	if isNPC(player) {
		return v.lp.Config().VaultNPCGroup
	}
	qo := v.queryOptions(&player, v.world(w))
	var name string
	_ = v.lp.WithUser(player, "", false, func(u *User) error {
		name = u.PrimaryGroup(qo)
		return nil
	})
	if g := v.group(name); g != nil {
		return v.groupName(g)
	}
	return name
}

func (v vaultHook) Groups() []string {
	var out []string
	for _, g := range v.lp.Groups.All() {
		out = append(out, v.groupName(g))
	}
	return out
}

// Chat.

func (v vaultHook) PlayerPrefix(w plugin.World, player plugin.UUID) string {
	return v.playerMeta(w, player, func(md *MetaData) string { return md.Prefix() })
}

func (v vaultHook) PlayerSuffix(w plugin.World, player plugin.UUID) string {
	return v.playerMeta(w, player, func(md *MetaData) string { return md.Suffix() })
}

func (v vaultHook) PlayerInfo(w plugin.World, player plugin.UUID, node string) (value string, ok bool) {
	v.playerMeta(w, player, func(md *MetaData) string {
		value, ok = md.MetaValue(node)
		return ""
	})
	return value, ok
}

func (v vaultHook) playerMeta(w plugin.World, player plugin.UUID, read func(*MetaData) string) (out string) {
	qo := v.queryOptions(&player, v.world(w))
	v.query(player, func(h *PermissionHolder) { out = read(h.CachedData().MetaData(qo)) })
	return out
}

func (v vaultHook) GroupPrefix(w plugin.World, group string) string {
	s, _ := v.groupMeta(w, group, func(md *MetaData) (string, bool) { return md.Prefix(), true })
	return s
}

func (v vaultHook) GroupSuffix(w plugin.World, group string) string {
	s, _ := v.groupMeta(w, group, func(md *MetaData) (string, bool) { return md.Suffix(), true })
	return s
}

func (v vaultHook) GroupInfo(w plugin.World, group, node string) (string, bool) {
	return v.groupMeta(w, group, func(md *MetaData) (string, bool) { return md.MetaValue(node) })
}

func (v vaultHook) groupMeta(w plugin.World, group string, read func(*MetaData) (string, bool)) (string, bool) {
	g := v.group(group)
	if g == nil {
		return "", false
	}
	return read(g.CachedData().MetaData(v.queryOptions(nil, v.world(w))))
}

func (v vaultHook) SetPlayerPrefix(w plugin.World, player plugin.UUID, prefix string) error {
	return v.change(player, func(h *PermissionHolder) bool { return v.setChatMeta(h, NodeTypePrefix, prefix, v.world(w)) })
}

func (v vaultHook) SetPlayerSuffix(w plugin.World, player plugin.UUID, suffix string) error {
	return v.change(player, func(h *PermissionHolder) bool { return v.setChatMeta(h, NodeTypeSuffix, suffix, v.world(w)) })
}

func (v vaultHook) SetPlayerInfo(w plugin.World, player plugin.UUID, node, value string) error {
	return v.change(player, func(h *PermissionHolder) bool { return v.setMeta(h, node, value, v.world(w)) })
}

func (v vaultHook) SetGroupPrefix(w plugin.World, group, prefix string) error {
	return v.changeGroup(group, func(h *PermissionHolder) bool { return v.setChatMeta(h, NodeTypePrefix, prefix, v.world(w)) })
}

func (v vaultHook) SetGroupSuffix(w plugin.World, group, suffix string) error {
	return v.changeGroup(group, func(h *PermissionHolder) bool { return v.setChatMeta(h, NodeTypeSuffix, suffix, v.world(w)) })
}

func (v vaultHook) SetGroupInfo(w plugin.World, group, node, value string) error {
	return v.changeGroup(group, func(h *PermissionHolder) bool { return v.setMeta(h, node, value, v.world(w)) })
}

// setChatMeta replaces every prefix (or suffix) set on h itself with value,
// at 10 above the highest priority h has in world, inherited or not; an
// empty value only removes. LuckPerms: LuckPermsVaultChat.setChatMeta
func (v vaultHook) setChatMeta(h *PermissionHolder, typ NodeType, value, world string) bool {
	removed := h.ClearNodes(DataNormal, func(n Node) bool { return n.Type == typ }) > 0
	if value == "" {
		return removed
	}
	md := h.CachedData().MetaData(v.worldSetOptions(world))
	byPriority := md.Prefixes()
	newNode := NewPrefixNode
	if typ == NodeTypeSuffix {
		byPriority, newNode = md.Suffixes(), NewSuffixNode
	}
	priority := 10
	if keys := SortIntMapKeysDesc(byPriority); len(keys) > 0 {
		priority = keys[0] + 10
	}
	h.SetNode(DataNormal, v.contexts(newNode(priority, value), world))
	return true
}

// setMeta replaces h's own meta key with value; prefix and suffix keys are
// set as chat meta. LuckPerms: LuckPermsVaultChat.setMeta
func (v vaultHook) setMeta(h *PermissionHolder, key, value, world string) bool {
	switch strings.ToLower(key) {
	case "prefix":
		return v.setChatMeta(h, NodeTypePrefix, value, world)
	case "suffix":
		return v.setChatMeta(h, NodeTypeSuffix, value, world)
	}
	removed := h.ClearNodes(DataNormal, func(n Node) bool { return n.Type == NodeTypeMeta && n.MetaKey == key }) > 0
	if value == "" {
		return removed
	}
	h.SetNode(DataNormal, v.contexts(NewMetaNode(key, value), world))
	return true
}

// worldSetOptions answers the options of a lookup in exactly world and the
// Vault server. LuckPerms: LuckPermsVaultChat.createQueryOptionsForWorldSet
func (v vaultHook) worldSetOptions(world string) QueryOptions {
	m := NewMutableContextSet()
	if world != "" && !strings.EqualFold(world, GlobalContextValue) {
		m.Add(ContextKeyWorld, strings.ToLower(world))
	}
	m.Add(ContextKeyServer, v.vaultServer())
	qo := v.lp.defaultQueryOptions(m.Immutable())
	qo.Flags.IncludeNodesWithoutServerContext = v.lp.Config().VaultIncludeGlobal
	return qo
}
