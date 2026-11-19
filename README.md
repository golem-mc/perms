# LuckPerms for golem

An unofficial port of [LuckPerms](https://luckperms.net/) to
[golem](https://github.com/golem-mc/golem), the Minecraft: Java Edition
server written in Go. It implements golem's `plugin.PermissionProvider`
service and keeps LuckPerms's `/luckperms` (`/lp`) commands, permission
nodes, contexts, storage formats, multi-server messaging protocols, and Web
Editor compatibility, so anyone who knows LuckPerms on Bukkit or Paper will
find the same plugin here.

## What's included

- **Permission engine** — Direct nodes, registered golem/Bukkit child
  permissions, regex nodes (`r=...`), wildcard nodes (`foo.*`, `*`), optional
  Sponge-style implicit wildcards, shorthand expansions (`perm.{1-4}`,
  `perm.(a|b)`), temporary nodes with expiration and accumulation policies,
  and operator policies (`enable-ops`, `auto-op`).
- **Groups, inheritance & tracks** — Configurable inheritance graph traversal
  (`depth-first-pre-order`, `depth-first-post-order`, `breadth-first`), group
  weights, display names, primary group calculation (`stored`,
  `parents-by-weight`, `all-parents-by-weight`), tracks, promotion, and
  demotion.
- **Contexts** — Built-in `server`, `world` (with recursive `world-rewrite`),
  `dimension-type`, and `gamemode` contexts, `contexts.json` static/default
  contexts, `at-least-one-value-per-key` and `all-values-per-key` modes, plus
  custom `ContextCalculator` registration via the API.
- **Meta, prefixes & suffixes** — Priority stacking (`highest`, `lowest`,
  `highest_own`, `highest_inherited`, `*_from_group_*`, `*_on_track_*`),
  duplicate removal (`first-only`, `last-only`, `retain-all`), spacers, and
  `meta-value-selection` (`inheritance`, `highest-number`, `lowest-number`).
- **Storage backends** —
  - Local files: `yaml`, `yaml-combined`, `json`, `json-combined`, `toml`,
    `toml-combined` (with `watch-files` auto-sync)
  - Local database: `sqlite` (pure Go, zero CGO; `h2` in migrated configs
    maps automatically to `sqlite`)
  - Remote databases: `mysql`, `mariadb`, `postgresql`, `mongodb` (using
    LuckPerms's table/collection schemas)
  - `split-storage` to route users, groups, tracks, UUID cache, and action
    logs to separate backends
  - `.json.gz` export/import (local files or `--upload`/`--web` via Bytebin)
    compatible with Java LuckPerms
- **Multi-server messaging** — `pluginmsg` (via golem's `Server.Messenger()`),
  `sql`, `postgres` (`LISTEN`/`NOTIFY`), `redis` (standalone, cluster, and
  sentinel), `nats`, `rabbitmq`, `custom` (via `API.RegisterMessengerProvider`),
  and `/lp networksync`.
- **Web Editor & Bytebin / Bytesocks** — `/lp editor` (plus
  `user/group/track <target> editor`), live ECDSA P-256 signed Bytesocks
  WebSocket editing, `/lp trusteditor <id>`, `/lp applyedits <code> [--force]`
  with diff-merging, and web uploads for `/lp verbose upload`,
  `/lp tree --web`, and `/lp export --upload`.
- **Translations & selectors** — Bundled `luckperms_en.properties`, custom
  and repository `.properties` bundles, `/lp translations [install]`, and
  `resolve-command-selectors` (`@s`, `@p`, `@a`, `@r`).

## Installing

### With the golem launcher

Add the plugin with
[`golem-launcher`](https://github.com/golem-mc/golem#installing-plugins):

```bash
./golem-launcher plugin add github.com/golem-mc/perms@<version>
./golem-launcher
```

`<version>` is a release tag (e.g. `v0.1.0`), or a commit hash while there
are no releases. The launcher writes the plugin to `launcher.json`, which you
can also edit by hand:

```json
{
  "server": "<golem version>",
  "plugins": [
    "github.com/golem-mc/perms@<version>"
  ]
}
```

The plugin needs golem at commit `056d502` or newer. If your server is too
old, the launcher says so and tells you how to update it.

### As a ready-built server

This repository also builds a standalone golem server with LuckPerms included.
You need [Go](https://go.dev/dl/) 1.27.4 or newer, or
[mise](https://mise.jdx.dev/), which installs the version in `mise.toml`:

```bash
git clone https://github.com/golem-mc/perms.git
cd perms
go build -o perms-server ./cmd/server   # with mise: mise exec -- go build ...
./perms-server --nogui
```

The server writes `server.properties` and the world into the directory it
runs in, as the vanilla server does. Stop it with `stop` or Ctrl+C.

## Configuration

On first start, LuckPerms writes its files to `plugins/luckperms/`:

- `config.yml` — the main configuration (storage method, split storage,
  database credentials, messaging service, inheritance/meta rules, world
  rewrites, web service URLs, and operator policies).
- `contexts.json` — static and default contexts.
- `yaml-storage/` (or `json-storage/`, `toml-storage/`, `luckperms-sqlite.db`)
  — permission data.
- `translations/` — optional downloaded (`repository/`) and local (`custom/`)
  `.properties` translation bundles.

These match LuckPerms's own files, so its
[documentation](https://luckperms.net/wiki/Home) applies. Run
`/lp reloadconfig` after editing `config.yml` or `contexts.json`, or `/lp sync`
after editing storage files.

### Moving from a Bukkit or Paper server

Copy the files from the old server's `plugins/LuckPerms/` folder into
`plugins/luckperms/`. Existing YAML, JSON, TOML, SQLite, MySQL/MariaDB,
PostgreSQL, and MongoDB data loads as-is, and `.json.gz` files produced by
`/lp export` on Java LuckPerms can be imported directly with `/lp import`.
If your old `config.yml` uses `storage-method: h2`, it automatically maps to
`sqlite`; export from H2 via `/lp export` on Java and import with `/lp import`
to migrate H2 data cleanly.

## Plugin API

LuckPerms registers itself with `plugin.LoadBefore("essentials", "vault")`
and provides both golem's `plugin.PermissionProvider` service and its own
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
`core.MessengerProvider` implementations through `API`, and subscribe to
events (`core.NodeAddEvent`, `core.NodeRemoveEvent`,
`core.UserDataRecalculateEvent`, `core.GroupCreateEvent`,
`core.GroupDeleteEvent`, `core.UserPromoteEvent`, `core.UserDemoteEvent`).

## Differences from Java LuckPerms

- **No H2 engine.** Pure-Go `sqlite` (`modernc.org/sqlite`, zero CGO) is used
  for embedded SQL storage; `h2` in a migrated `config.yml` falls back to
  `sqlite` with a warning.
- **No Vault or PlaceholderAPI bridges.** Other golem plugins query groups,
  prefixes, suffixes, meta, and permissions directly via
  `plugin.Service[luckperms.API](s)`.
- **No legacy Bukkit permissions.yml or `/lp migration` importers.** Use
  `/lp export` and `/lp import` (`.json.gz` or `--web`) to move data between
  installations.

## Development

```bash
mise exec -- go test -race ./...
mise exec -- go vet ./...
mise exec -- go mod tidy -diff
```

The root package tests run a real golem server inside the test process
(golem's `plugintest`), join players, and verify commands, contexts, storage,
and services end to end.

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

## License

MIT — see [LICENSE](LICENSE). Derived from
[LuckPerms](https://github.com/LuckPerms/LuckPerms) by lucko and
contributors, including the bundled `config.yml` and
`luckperms_en.properties`. This project is not affiliated with or endorsed by
LuckPerms.
