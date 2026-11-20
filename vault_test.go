package luckperms_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/golem-mc/golem/plugin"
	"github.com/golem-mc/perms/core"
	"github.com/golem-mc/perms/internal/lptest"
	"github.com/golem-mc/vault"
)

// whoami formats a player's name through Vault only, as a chat plugin does.
type whoami struct{}

func init() { plugin.Register("whoami", whoami{}) }

func (whoami) Enable(s plugin.Server) error {
	return s.Commands().Register(plugin.Command{Name: "whoami", Node: plugin.Node{Run: func(ctx *plugin.CommandContext) (int, error) {
		p := ctx.Sender.(plugin.Player)
		perm, ok1 := vault.LookupPermission(s)
		chat, ok2 := vault.LookupChat(s)
		if !ok1 || !ok2 {
			p.SendMessage(plugin.Plain("no vault services"))
			return 0, nil
		}
		w, id := p.World(), p.UUID()
		p.SendMessage(plugin.Plain(fmt.Sprintf("%s%s%s in %s, may fly: %v, homes: %d", chat.PlayerPrefix(w, id), p.Name(), chat.PlayerSuffix(w, id),
			perm.PrimaryGroup(w, id), perm.PlayerHas(w, id, "demo.fly"), vault.PlayerInfoOr(chat, w, id, "home-limit", 1))))
		return 1, nil
	}}})
}

func (whoami) Disable() {}

func TestVaultPermissionAndChat(t *testing.T) {
	env := lptest.Start(t)
	alice := env.Join("Alice", false)
	bob := env.Join("Bob", false)
	host := env.Srv.Host()
	aliceID, bobID := alice.Handle().UUID(), bob.Handle().UUID()

	env.Expect(env.Console("lp creategroup vip 50"), "created")
	env.Expect(env.Console(`lp g vip m addprefix 100 "[VIP] "`), "Added prefix")
	env.Expect(env.Console(`lp g vip m set home-limit 5`), "Set meta value")
	env.Expect(env.Console("lp u Alice parent add vip"), "now inherits")
	env.Expect(env.Run(alice, "whoami"), "[VIP] Alice in vip, may fly: false, homes: 5")

	perm, ok := vault.LookupPermission(host)
	if !ok || perm.Name() != "LuckPerms" || !perm.GroupSupport() {
		t.Fatalf("LookupPermission answered %v", perm)
	}
	chat, ok := vault.LookupChat(host)
	if !ok || chat.Name() != "LuckPerms" {
		t.Fatalf("LookupChat answered %v", chat)
	}

	// Changes through Vault reach the player's golem permission checks.
	if err := perm.PlayerAdd(nil, aliceID, "demo.fly"); err != nil {
		t.Fatal(err)
	}
	if !alice.Handle().HasPermission("demo.fly") || !perm.PlayerHas(alice.Handle().World(), aliceID, "demo.fly") {
		t.Fatal("demo.fly added through Vault is not had")
	}
	if err := chat.SetPlayerSuffix(nil, aliceID, " *"); err != nil {
		t.Fatal(err)
	}
	if err := chat.SetPlayerInfo(nil, aliceID, "home-limit", "8"); err != nil {
		t.Fatal(err)
	}
	env.Expect(env.Run(alice, "whoami"), "[VIP] Alice * in vip, may fly: true, homes: 8")

	if !perm.PlayerInGroup(nil, aliceID, "vip") || perm.PlayerInGroup(nil, aliceID, "admin") {
		t.Fatal("PlayerInGroup")
	}
	if got := perm.PlayerGroups(nil, aliceID); !slices.Contains(got, "vip") {
		t.Fatalf("PlayerGroups = %q", got)
	}
	if got := perm.Groups(); !slices.Contains(got, "vip") || !slices.Contains(got, "default") {
		t.Fatalf("Groups = %q", got)
	}
	if err := perm.PlayerAddGroup(nil, aliceID, "nosuch"); err == nil {
		t.Fatal("PlayerAddGroup to a missing group succeeded")
	}

	// Groups.
	if err := perm.GroupAdd(nil, "vip", "demo.vip"); err != nil {
		t.Fatal(err)
	}
	if !perm.GroupHas(nil, "vip", "demo.vip") || !perm.PlayerHas(nil, aliceID, "demo.vip") {
		t.Fatal("demo.vip added to vip through Vault is not had")
	}
	if err := chat.SetGroupPrefix(nil, "vip", "[V] "); err != nil {
		t.Fatal(err)
	}
	if got := chat.GroupPrefix(nil, "vip"); got != "[V] " {
		t.Fatalf("GroupPrefix = %q", got)
	}
	if v, ok := chat.GroupInfo(nil, "vip", "home-limit"); !ok || v != "5" {
		t.Fatalf("GroupInfo = %q, %v", v, ok)
	}

	// An offline player is loaded from storage for the call.
	bob.Disconnect()
	env.Srv.RunTicks(5)
	if err := perm.PlayerAdd(nil, bobID, "demo.offline"); err != nil {
		t.Fatal(err)
	}
	if !perm.PlayerHas(nil, bobID, "demo.offline") || perm.PrimaryGroup(nil, bobID) != "default" {
		t.Fatal("Bob, offline, did not keep demo.offline")
	}
	env.Expect(env.Console("lp user Bob permission check demo.offline"), "true")

	// An NPC (version 2 UUID) is the npc group and cannot be changed.
	npc := plugin.UUID{15: 1}
	npc[6] = 0x20
	npc[8] = 0x80
	if err := perm.PlayerAdd(nil, npc, "demo.npc"); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("PlayerAdd for an NPC: %v", err)
	}
	if got := perm.PrimaryGroup(nil, npc); got != "default" {
		t.Fatalf("an NPC's primary group is %q", got)
	}
}

func TestVaultConfigKeys(t *testing.T) {
	cfg, err := core.ParseConfigBytes([]byte("vault-group-use-displaynames: false\nvault-npc-group: npcs\nvault-npc-op-status: true\nuse-vault-server: true\nvault-server: Lobby\nvault-include-global: false\nvault-ignore-world: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VaultGroupUseDisplayNames || cfg.VaultNPCGroup != "npcs" || !cfg.VaultNPCOpStatus || !cfg.UseVaultServer ||
		cfg.VaultServer != "lobby" || cfg.VaultIncludeGlobal || !cfg.VaultIgnoreWorld {
		t.Fatalf("vault keys read as %+v", cfg)
	}
	def, err := core.ParseConfigBytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !def.VaultGroupUseDisplayNames || def.VaultNPCGroup != "default" || def.VaultServer != "global" || !def.VaultIncludeGlobal {
		t.Fatalf("vault defaults read as %+v", def)
	}
}
