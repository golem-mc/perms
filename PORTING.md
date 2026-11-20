# Porting notes

This document records porting decisions, deliberate differences from Java
LuckPerms, storage/migration details, plugin API integration, and local
development notes for `golem-mc/perms`.

## Differences from Java LuckPerms

- **No H2 engine.** Pure-Go `sqlite` (`modernc.org/sqlite`, zero CGO) is used
  for embedded SQL storage; `h2` in a migrated `config.yml` falls back to
  `sqlite` with a warning.
- **No PlaceholderAPI bridge.** Other golem plugins query groups, prefixes,
  suffixes, meta, and permissions through Vault or directly via
  `plugin.Service[luckperms.API](s)`.
- **`vault-unsafe-lookups` is not read.** golem cannot yet tell a plugin
  whether it runs on the tick goroutine, so a Vault lookup of an offline
  player always loads them from storage on the caller's goroutine.
- **No legacy Bukkit `permissions.yml` or `/lp migration` importers.** Use
  `/lp export` and `/lp import` (`.json.gz` or `--web`) to move data between
  installations.

## Files and Bukkit/Paper migration

On first start, LuckPerms writes its files to `plugins/luckperms/`:

- `config.yml` — main configuration (storage method, split storage, database
  credentials, messaging service, inheritance/meta rules, world rewrites, web
  service URLs, and operator policies).
- `contexts.json` — static and default contexts.
- `yaml-storage/` (or `json-storage/`, `toml-storage/`, `luckperms-sqlite.db`)
  — permission data (`yaml`, `yaml-combined`, `json`, `json-combined`, `toml`,
  `toml-combined` with `watch-files` auto-sync, and `sqlite`).
- `translations/` — optional downloaded (`repository/`) and local (`custom/`)
  `.properties` translation bundles, alongside the bundled
  `luckperms_en.properties`.

To move data from a Bukkit or Paper server, copy `plugins/LuckPerms/` into
`plugins/luckperms/`. Existing YAML, JSON, TOML, SQLite, MySQL/MariaDB,
PostgreSQL, and MongoDB data loads as-is using LuckPerms's table and collection
schemas. `.json.gz` files produced by `/lp export` on Java LuckPerms (local or
via Bytebin `--upload`/`--web`) can be imported directly with `/lp import`.
If an old `config.yml` uses `storage-method: h2`, it maps automatically to
`sqlite`; export from H2 via `/lp export` on Java and import with `/lp import`
to migrate H2 data cleanly.

## Plugin API and Vault integration

LuckPerms registers itself with `plugin.LoadBefore("essentials", "vault")` and
provides golem's `plugin.PermissionProvider` service as well as its own
`luckperms.API` service:

```go
if lp, ok := plugin.Service[luckperms.API](server); ok {
    group := lp.PrimaryGroup(player)
    prefix := lp.Prefix(player)
    suffix := lp.Suffix(player)
    state := lp.CheckPermission(player, "myplugin.use")
}
```

Other plugins can also register custom `core.ContextCalculator` and
`core.MessengerProvider` implementations through `API`, and subscribe to events
(`core.NodeAddEvent`, `core.NodeRemoveEvent`, `core.UserDataRecalculateEvent`,
`core.GroupCreateEvent`, `core.GroupDeleteEvent`, `core.UserPromoteEvent`,
`core.UserDemoteEvent`).

### Vault

LuckPerms provides [Vault for golem](https://github.com/golem-mc/vault)'s
`vault.Permission` and `vault.Chat` services at high priority, matching its
Bukkit Vault hook (`LuckPermsVaultPermission`, `LuckPermsVaultChat`), for both
online and offline players. A world argument becomes the `world` context;
changes are made in the `vault-server` (or `server`) context. The `vault-*`
options in `config.yml` (`vault-group-use-displaynames`, `vault-npc-group`,
`vault-npc-op-status`, `use-vault-server`, `vault-server`,
`vault-include-global`, `vault-ignore-world`) work as on Bukkit.

```go
if chat, ok := vault.LookupChat(server); ok {
    prefix := chat.PlayerPrefix(player.World(), player.UUID())
}
```

## Development

### Running from a checkout

To run a server with this checkout's plugin built in (requires
[mise](https://mise.jdx.dev/), which installs the Go version in `mise.toml`):

```bash
git clone https://github.com/golem-mc/perms.git
cd perms
mise run dev
```

This runs `golem-launcher` (a Go tool pinned in `go.mod`) in `dev/`, with the
golem version `go.mod` requires and this directory as the plugin. The server
writes `server.properties`, the world, and `plugins/` into `dev/`, which git
ignores. Stop it with `stop` or Ctrl+C.

### Testing and updating golem

```bash
mise exec -- go test -race ./...
mise exec -- go vet ./...
mise exec -- go mod tidy -diff
```

The root package tests run a real golem server inside the test process (golem's
`plugintest`), join players, and verify commands, contexts, storage, and
services end to end.

`go.mod` pins a golem commit. To move to a newer one:

```bash
mise exec -- go get github.com/golem-mc/golem@<commit>
```

To build against a local golem checkout instead, create a `go.work`, which git
ignores:

```bash
go work init . ../golem
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for commit conventions and the release
process.
