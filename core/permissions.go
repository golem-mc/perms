package core

import (
	"strings"

	"github.com/golem-mc/golem/plugin"
)

// CommandPerm defines a LuckPerms command permission node and whether it is read-only.
type CommandPerm struct {
	Node     string
	ReadOnly bool
}

// All LuckPerms command permissions (matching CommandPermission.java).
var (
	PermSync                 = CommandPerm{"luckperms.sync", true}
	PermInfo                 = CommandPerm{"luckperms.info", true}
	PermEditor               = CommandPerm{"luckperms.editor", true}
	PermVerbose              = CommandPerm{"luckperms.verbose", true}
	PermVerboseCommandOthers = CommandPerm{"luckperms.verbose.command.others", false}
	PermTree                 = CommandPerm{"luckperms.tree", true}
	PermSearch               = CommandPerm{"luckperms.search", true}
	PermImport               = CommandPerm{"luckperms.import", false}
	PermExport               = CommandPerm{"luckperms.export", true}
	PermReloadConfig         = CommandPerm{"luckperms.reloadconfig", true}
	PermBulkUpdate           = CommandPerm{"luckperms.bulkupdate", false}
	PermApplyEdits           = CommandPerm{"luckperms.applyedits", false}
	PermTrustEditor          = CommandPerm{"luckperms.trusteditor", false}
	PermTranslations         = CommandPerm{"luckperms.translations", true}
	PermCreateGroup          = CommandPerm{"luckperms.creategroup", false}
	PermDeleteGroup          = CommandPerm{"luckperms.deletegroup", false}
	PermListGroups           = CommandPerm{"luckperms.listgroups", true}
	PermCreateTrack          = CommandPerm{"luckperms.createtrack", false}
	PermDeleteTrack          = CommandPerm{"luckperms.deletetrack", false}
	PermListTracks           = CommandPerm{"luckperms.listtracks", true}

	PermUserInfo                 = CommandPerm{"luckperms.user.info", true}
	PermUserPermInfo             = CommandPerm{"luckperms.user.permission.info", true}
	PermUserPermSet              = CommandPerm{"luckperms.user.permission.set", false}
	PermUserPermUnset            = CommandPerm{"luckperms.user.permission.unset", false}
	PermUserPermSetTemp          = CommandPerm{"luckperms.user.permission.settemp", false}
	PermUserPermUnsetTemp        = CommandPerm{"luckperms.user.permission.unsettemp", false}
	PermUserPermCheck            = CommandPerm{"luckperms.user.permission.check", true}
	PermUserPermClear            = CommandPerm{"luckperms.user.permission.clear", false}
	PermUserParentInfo           = CommandPerm{"luckperms.user.parent.info", true}
	PermUserParentSet            = CommandPerm{"luckperms.user.parent.set", false}
	PermUserParentSetTrack       = CommandPerm{"luckperms.user.parent.settrack", false}
	PermUserParentAdd            = CommandPerm{"luckperms.user.parent.add", false}
	PermUserParentRemove         = CommandPerm{"luckperms.user.parent.remove", false}
	PermUserParentAddTemp        = CommandPerm{"luckperms.user.parent.addtemp", false}
	PermUserParentRemoveTemp     = CommandPerm{"luckperms.user.parent.removetemp", false}
	PermUserParentClear          = CommandPerm{"luckperms.user.parent.clear", false}
	PermUserParentClearTrack     = CommandPerm{"luckperms.user.parent.cleartrack", false}
	PermUserParentSwitchPrimary  = CommandPerm{"luckperms.user.parent.switchprimarygroup", false}
	PermUserMetaInfo             = CommandPerm{"luckperms.user.meta.info", true}
	PermUserMetaSet              = CommandPerm{"luckperms.user.meta.set", false}
	PermUserMetaUnset            = CommandPerm{"luckperms.user.meta.unset", false}
	PermUserMetaSetTemp          = CommandPerm{"luckperms.user.meta.settemp", false}
	PermUserMetaUnsetTemp        = CommandPerm{"luckperms.user.meta.unsettemp", false}
	PermUserMetaAddPrefix        = CommandPerm{"luckperms.user.meta.addprefix", false}
	PermUserMetaAddSuffix        = CommandPerm{"luckperms.user.meta.addsuffix", false}
	PermUserMetaSetPrefix        = CommandPerm{"luckperms.user.meta.setprefix", false}
	PermUserMetaSetSuffix        = CommandPerm{"luckperms.user.meta.setsuffix", false}
	PermUserMetaRemovePrefix     = CommandPerm{"luckperms.user.meta.removeprefix", false}
	PermUserMetaRemoveSuffix     = CommandPerm{"luckperms.user.meta.removesuffix", false}
	PermUserMetaAddTempPrefix    = CommandPerm{"luckperms.user.meta.addtempprefix", false}
	PermUserMetaAddTempSuffix    = CommandPerm{"luckperms.user.meta.addtempsuffix", false}
	PermUserMetaSetTempPrefix    = CommandPerm{"luckperms.user.meta.settempprefix", false}
	PermUserMetaSetTempSuffix    = CommandPerm{"luckperms.user.meta.settempsuffix", false}
	PermUserMetaRemoveTempPrefix = CommandPerm{"luckperms.user.meta.removetempprefix", false}
	PermUserMetaRemoveTempSuffix = CommandPerm{"luckperms.user.meta.removetempsuffix", false}
	PermUserMetaClear            = CommandPerm{"luckperms.user.meta.clear", false}
	PermUserShowTracks           = CommandPerm{"luckperms.user.showtracks", true}
	PermUserEditor               = CommandPerm{"luckperms.user.editor", true}
	PermUserPromote              = CommandPerm{"luckperms.user.promote", false}
	PermUserDemote               = CommandPerm{"luckperms.user.demote", false}
	PermUserClear                = CommandPerm{"luckperms.user.clear", false}
	PermUserClone                = CommandPerm{"luckperms.user.clone", false}

	PermGroupInfo                 = CommandPerm{"luckperms.group.info", true}
	PermGroupPermInfo             = CommandPerm{"luckperms.group.permission.info", true}
	PermGroupPermSet              = CommandPerm{"luckperms.group.permission.set", false}
	PermGroupPermUnset            = CommandPerm{"luckperms.group.permission.unset", false}
	PermGroupPermSetTemp          = CommandPerm{"luckperms.group.permission.settemp", false}
	PermGroupPermUnsetTemp        = CommandPerm{"luckperms.group.permission.unsettemp", false}
	PermGroupPermCheck            = CommandPerm{"luckperms.group.permission.check", true}
	PermGroupPermClear            = CommandPerm{"luckperms.group.permission.clear", false}
	PermGroupParentInfo           = CommandPerm{"luckperms.group.parent.info", true}
	PermGroupParentSet            = CommandPerm{"luckperms.group.parent.set", false}
	PermGroupParentSetTrack       = CommandPerm{"luckperms.group.parent.settrack", false}
	PermGroupParentAdd            = CommandPerm{"luckperms.group.parent.add", false}
	PermGroupParentRemove         = CommandPerm{"luckperms.group.parent.remove", false}
	PermGroupParentAddTemp        = CommandPerm{"luckperms.group.parent.addtemp", false}
	PermGroupParentRemoveTemp     = CommandPerm{"luckperms.group.parent.removetemp", false}
	PermGroupParentClear          = CommandPerm{"luckperms.group.parent.clear", false}
	PermGroupParentClearTrack     = CommandPerm{"luckperms.group.parent.cleartrack", false}
	PermGroupMetaInfo             = CommandPerm{"luckperms.group.meta.info", true}
	PermGroupMetaSet              = CommandPerm{"luckperms.group.meta.set", false}
	PermGroupMetaUnset            = CommandPerm{"luckperms.group.meta.unset", false}
	PermGroupMetaSetTemp          = CommandPerm{"luckperms.group.meta.settemp", false}
	PermGroupMetaUnsetTemp        = CommandPerm{"luckperms.group.meta.unsettemp", false}
	PermGroupMetaAddPrefix        = CommandPerm{"luckperms.group.meta.addprefix", false}
	PermGroupMetaAddSuffix        = CommandPerm{"luckperms.group.meta.addsuffix", false}
	PermGroupMetaSetPrefix        = CommandPerm{"luckperms.group.meta.setprefix", false}
	PermGroupMetaSetSuffix        = CommandPerm{"luckperms.group.meta.setsuffix", false}
	PermGroupMetaRemovePrefix     = CommandPerm{"luckperms.group.meta.removeprefix", false}
	PermGroupMetaRemoveSuffix     = CommandPerm{"luckperms.group.meta.removesuffix", false}
	PermGroupMetaAddTempPrefix    = CommandPerm{"luckperms.group.meta.addtempprefix", false}
	PermGroupMetaAddTempSuffix    = CommandPerm{"luckperms.group.meta.addtempsuffix", false}
	PermGroupMetaSetTempPrefix    = CommandPerm{"luckperms.group.meta.settempprefix", false}
	PermGroupMetaSetTempSuffix    = CommandPerm{"luckperms.group.meta.settempsuffix", false}
	PermGroupMetaRemoveTempPrefix = CommandPerm{"luckperms.group.meta.removetempprefix", false}
	PermGroupMetaRemoveTempSuffix = CommandPerm{"luckperms.group.meta.removetempsuffix", false}
	PermGroupMetaClear            = CommandPerm{"luckperms.group.meta.clear", false}
	PermGroupListMembers          = CommandPerm{"luckperms.group.listmembers", true}
	PermGroupShowTracks           = CommandPerm{"luckperms.group.showtracks", true}
	PermGroupEditor               = CommandPerm{"luckperms.group.editor", true}
	PermGroupSetWeight            = CommandPerm{"luckperms.group.setweight", false}
	PermGroupSetDisplayName       = CommandPerm{"luckperms.group.setdisplayname", false}
	PermGroupClear                = CommandPerm{"luckperms.group.clear", false}
	PermGroupRename               = CommandPerm{"luckperms.group.rename", false}
	PermGroupClone                = CommandPerm{"luckperms.group.clone", false}

	PermTrackInfo   = CommandPerm{"luckperms.track.info", true}
	PermTrackEditor = CommandPerm{"luckperms.track.editor", true}
	PermTrackAppend = CommandPerm{"luckperms.track.append", false}
	PermTrackInsert = CommandPerm{"luckperms.track.insert", false}
	PermTrackRemove = CommandPerm{"luckperms.track.remove", false}
	PermTrackClear  = CommandPerm{"luckperms.track.clear", false}
	PermTrackRename = CommandPerm{"luckperms.track.rename", false}
	PermTrackClone  = CommandPerm{"luckperms.track.clone", false}

	PermLogRecent       = CommandPerm{"luckperms.log.recent", true}
	PermLogUserHistory  = CommandPerm{"luckperms.log.userhistory", true}
	PermLogGroupHistory = CommandPerm{"luckperms.log.grouphistory", true}
	PermLogTrackHistory = CommandPerm{"luckperms.log.trackhistory", true}
	PermLogSearch       = CommandPerm{"luckperms.log.search", true}
	PermLogNotify       = CommandPerm{"luckperms.log.notify", true}
)

var allCommandPerms = []CommandPerm{
	PermSync, PermInfo, PermEditor, PermVerbose, PermVerboseCommandOthers,
	PermTree, PermSearch, PermImport, PermExport, PermReloadConfig,
	PermBulkUpdate, PermApplyEdits, PermTrustEditor, PermTranslations, PermCreateGroup, PermDeleteGroup, PermListGroups,
	PermCreateTrack, PermDeleteTrack, PermListTracks,
	PermUserInfo, PermUserPermInfo, PermUserPermSet, PermUserPermUnset,
	PermUserPermSetTemp, PermUserPermUnsetTemp, PermUserPermCheck, PermUserPermClear,
	PermUserParentInfo, PermUserParentSet, PermUserParentSetTrack, PermUserParentAdd,
	PermUserParentRemove, PermUserParentAddTemp, PermUserParentRemoveTemp,
	PermUserParentClear, PermUserParentClearTrack, PermUserParentSwitchPrimary,
	PermUserMetaInfo, PermUserMetaSet, PermUserMetaUnset, PermUserMetaSetTemp,
	PermUserMetaUnsetTemp, PermUserMetaAddPrefix, PermUserMetaAddSuffix,
	PermUserMetaSetPrefix, PermUserMetaSetSuffix, PermUserMetaRemovePrefix,
	PermUserMetaRemoveSuffix, PermUserMetaAddTempPrefix, PermUserMetaAddTempSuffix,
	PermUserMetaSetTempPrefix, PermUserMetaSetTempSuffix, PermUserMetaRemoveTempPrefix,
	PermUserMetaRemoveTempSuffix, PermUserMetaClear, PermUserShowTracks, PermUserEditor,
	PermUserPromote, PermUserDemote, PermUserClear, PermUserClone,
	PermGroupInfo, PermGroupPermInfo, PermGroupPermSet, PermGroupPermUnset,
	PermGroupPermSetTemp, PermGroupPermUnsetTemp, PermGroupPermCheck, PermGroupPermClear,
	PermGroupParentInfo, PermGroupParentSet, PermGroupParentSetTrack, PermGroupParentAdd,
	PermGroupParentRemove, PermGroupParentAddTemp, PermGroupParentRemoveTemp,
	PermGroupParentClear, PermGroupParentClearTrack, PermGroupMetaInfo,
	PermGroupMetaSet, PermGroupMetaUnset, PermGroupMetaSetTemp, PermGroupMetaUnsetTemp,
	PermGroupMetaAddPrefix, PermGroupMetaAddSuffix, PermGroupMetaSetPrefix,
	PermGroupMetaSetSuffix, PermGroupMetaRemovePrefix, PermGroupMetaRemoveSuffix,
	PermGroupMetaAddTempPrefix, PermGroupMetaAddTempSuffix, PermGroupMetaSetTempPrefix,
	PermGroupMetaSetTempSuffix, PermGroupMetaRemoveTempPrefix, PermGroupMetaRemoveTempSuffix,
	PermGroupMetaClear, PermGroupListMembers, PermGroupShowTracks, PermGroupEditor,
	PermGroupSetWeight, PermGroupSetDisplayName, PermGroupClear, PermGroupRename, PermGroupClone,
	PermTrackInfo, PermTrackEditor, PermTrackAppend, PermTrackInsert, PermTrackRemove,
	PermTrackClear, PermTrackRename, PermTrackClone,
	PermLogRecent, PermLogUserHistory, PermLogGroupHistory, PermLogTrackHistory,
	PermLogSearch, PermLogNotify,
}

func (lp *LuckPerms) registerPermissions() {
	if lp.Server == nil {
		return
	}
	opLevel := 5 // nobody by default unless granted
	if lp.Config().CommandsAllowOp {
		opLevel = 1 // any op
	}
	for _, cp := range allCommandPerms {
		lp.Server.Permissions().Register(plugin.Permission{
			Name:    cp.Node,
			Default: plugin.PermissionDefault{OpLevel: opLevel},
		})
		lp.Registry.Offer(cp.Node)
	}
	lp.Server.Permissions().Register(plugin.Permission{
		Name:    "luckperms.autoop",
		Default: plugin.PermissionDefault{OpLevel: 5},
	})
	lp.Registry.Offer("luckperms.autoop")
}

// IsConsole reports whether sender is not a player (console or command block).
func IsConsole(sender plugin.CommandSender) bool {
	_, isPlayer := sender.(plugin.Player)
	return !isPlayer
}

// CanUseCommand checks if sender is authorized to run a command with perm.
func (lp *LuckPerms) CanUseCommand(sender plugin.CommandSender, perm CommandPerm) bool {
	cfg := lp.Config()
	if IsConsole(sender) {
		if cfg.DisableConsole {
			return false
		}
		if cfg.ReadOnlyConsole && !perm.ReadOnly {
			return false
		}
		return true
	}
	if cfg.DisablePlayers {
		return false
	}
	if cfg.ReadOnlyPlayers && !perm.ReadOnly {
		return false
	}
	p := sender.(plugin.Player)
	u := lp.Users.GetIfLoaded(p.UUID())
	if u != nil {
		res := u.CachedData().PermissionData(lp.Contexts.QueryOptionsForPlayer(p)).CheckPermission(perm.Node, OriginInternal)
		if res.Result != TristateUndefined {
			return res.Result.AsBool()
		}
	}
	return cfg.CommandsAllowOp && p.OpLevel() > 0
}

// HasAnyCommandPermission reports whether sender can run at least one LP command.
func (lp *LuckPerms) HasAnyCommandPermission(sender plugin.CommandSender) bool {
	for _, cp := range allCommandPerms {
		if lp.CanUseCommand(sender, cp) {
			return true
		}
	}
	return false
}

// CheckModifyGroupPermission checks argument-based permissions and require-sender-group-membership-to-modify.
func (lp *LuckPerms) CheckModifyGroupPermission(sender plugin.CommandSender, groupName string, action string) bool {
	if IsConsole(sender) {
		return true
	}
	cfg := lp.Config()
	p := sender.(plugin.Player)
	if cfg.RequireSenderGroupMembershipToMod {
		if !p.HasPermission("group." + strings.ToLower(groupName)) {
			return false
		}
	}
	if cfg.ArgumentBasedCommandPerms {
		if !p.HasPermission("luckperms.modify.group." + strings.ToLower(groupName)) {
			return false
		}
		if action != "" && !p.HasPermission("luckperms."+action+"."+strings.ToLower(groupName)) {
			return false
		}
	}
	return true
}

// CheckModifyUserPermission checks argument-based permissions for modifying a user.
func (lp *LuckPerms) CheckModifyUserPermission(sender plugin.CommandSender, target *User) bool {
	if IsConsole(sender) {
		return true
	}
	cfg := lp.Config()
	if !cfg.ArgumentBasedCommandPerms {
		return true
	}
	p := sender.(plugin.Player)
	if p.UUID() == target.UUID() {
		return p.HasPermission("luckperms.modify.user.self")
	}
	return p.HasPermission("luckperms.modify.user.other")
}

// CheckModifyNodePermission checks argument-based permissions for modifying a node.
func (lp *LuckPerms) CheckModifyNodePermission(sender plugin.CommandSender, nodeKey string) bool {
	if IsConsole(sender) {
		return true
	}
	if !lp.Config().ArgumentBasedCommandPerms {
		return true
	}
	return sender.HasPermission("luckperms.modify.permission." + strings.ToLower(nodeKey))
}
