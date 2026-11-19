// Package luckperms is a port of LuckPerms to golem, registered as "luckperms".
//
// It implements golem's plugin.PermissionProvider service and exposes core.API
// via plugin.Service[core.API](s) so other plugins can query groups, prefixes,
// suffixes, meta, and permissions.
package luckperms

import (
	"github.com/golem-mc/golem/plugin"
	"github.com/golem-mc/perms/commands"
	"github.com/golem-mc/perms/core"
)

// API re-exports core.API for callers importing the root luckperms package.
type API = core.API

// LuckPerms is the golem plugin entry point.
type LuckPerms struct {
	lp *core.LuckPerms
}

func init() {
	plugin.Register("luckperms", &LuckPerms{}, plugin.LoadBefore("essentials", "vault"))
}

// Enable initializes LuckPerms storage, services, listeners, and /lp commands.
func (p *LuckPerms) Enable(s plugin.Server) error {
	lp, err := core.Enable(s)
	if err != nil {
		return err
	}
	p.lp = lp
	commands.Register(lp)
	return nil
}

// Disable shuts down background tasks and flushes storage.
func (p *LuckPerms) Disable() {
	if p.lp != nil {
		p.lp.Disable()
	}
}
