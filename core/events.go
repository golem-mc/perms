package core

import "github.com/golem-mc/golem/plugin"

// NodeAddEvent fires when a node is added to a user or group.
type NodeAddEvent struct {
	plugin.Base
	Holder   *PermissionHolder
	DataType DataType
	Node     Node
}

// NodeRemoveEvent fires when a node is removed from a user or group.
type NodeRemoveEvent struct {
	plugin.Base
	Holder   *PermissionHolder
	DataType DataType
	Node     Node
}

// UserDataRecalculateEvent fires when a user's cached permissions/meta are invalidated.
type UserDataRecalculateEvent struct {
	plugin.Base
	User *User
}

// GroupCreateEvent fires when a new group is created.
type GroupCreateEvent struct {
	plugin.Base
	Group *Group
}

// GroupDeleteEvent fires when a group is deleted.
type GroupDeleteEvent struct {
	plugin.Base
	GroupName string
}

// UserPromoteEvent fires when a user is promoted on a track.
type UserPromoteEvent struct {
	plugin.Base
	User      *User
	Track     *Track
	GroupFrom string
	GroupTo   string
}

// UserDemoteEvent fires when a user is demoted on a track.
type UserDemoteEvent struct {
	plugin.Base
	User      *User
	Track     *Track
	GroupFrom string
	GroupTo   string
}

func (lp *LuckPerms) defineEvents() {
	if lp.Server == nil {
		return
	}
	plugin.DefineEvent[NodeAddEvent](lp.Server)
	plugin.DefineEvent[NodeRemoveEvent](lp.Server)
	plugin.DefineEvent[UserDataRecalculateEvent](lp.Server)
	plugin.DefineEvent[GroupCreateEvent](lp.Server)
	plugin.DefineEvent[GroupDeleteEvent](lp.Server)
	plugin.DefineEvent[UserPromoteEvent](lp.Server)
	plugin.DefineEvent[UserDemoteEvent](lp.Server)
}

func (lp *LuckPerms) fireNodeAdd(h *PermissionHolder, dt DataType, n Node) {
	if lp.Server != nil {
		lp.Server.Events().Fire(&NodeAddEvent{Holder: h, DataType: dt, Node: n})
	}
}

func (lp *LuckPerms) fireNodeRemove(h *PermissionHolder, dt DataType, n Node) {
	if lp.Server != nil {
		lp.Server.Events().Fire(&NodeRemoveEvent{Holder: h, DataType: dt, Node: n})
	}
}

func (lp *LuckPerms) fireUserDataRecalculate(u *User) {
	if lp.Server != nil {
		lp.Server.Events().Fire(&UserDataRecalculateEvent{User: u})
	}
}

func (lp *LuckPerms) FireGroupCreate(g *Group) {
	if lp.Server != nil {
		lp.Server.Events().Fire(&GroupCreateEvent{Group: g})
	}
}

func (lp *LuckPerms) FireGroupDelete(name string) {
	if lp.Server != nil {
		lp.Server.Events().Fire(&GroupDeleteEvent{GroupName: name})
	}
}

func (lp *LuckPerms) fireUserPromote(u *User, t *Track, from, to string) {
	if lp.Server != nil {
		lp.Server.Events().Fire(&UserPromoteEvent{User: u, Track: t, GroupFrom: from, GroupTo: to})
	}
}

func (lp *LuckPerms) fireUserDemote(u *User, t *Track, from, to string) {
	if lp.Server != nil {
		lp.Server.Events().Fire(&UserDemoteEvent{User: u, Track: t, GroupFrom: from, GroupTo: to})
	}
}
