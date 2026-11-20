# LuckPerms for golem

An unofficial port of [LuckPerms](https://luckperms.net/) to
[golem](https://github.com/golem-mc/golem), implementing golem's permission
provider with LuckPerms's `/lp` commands, permission nodes, contexts, storage
formats, multi-server messaging, and Web Editor. See the
[LuckPerms wiki](https://luckperms.net/wiki/Home) and
[Command Usage](https://luckperms.net/wiki/Command-Usage) for full command
reference.

## Install

Add the plugin with
[`golem-launcher`](https://github.com/golem-mc/golem#installing-plugins):

```bash
golem-launcher plugin add github.com/golem-mc/perms
```

Requires golem `4f95109` or newer.

## What's included

- **Users & permissions** — direct, child, wildcard, regex, shorthand, and
  temporary permission nodes, plus operator policies (`/lp user`, `/lp check`,
  `/lp search`, `/lp bulkupdate`).
- **Groups & tracks** — group inheritance, weights, display names, primary
  groups, and promotion/demotion tracks (`/lp group`, `/lp track`,
  `/lp creategroup`, `/lp deletegroup`, `/lp listgroups`, `/lp createtrack`,
  `/lp deletetrack`, `/lp listtracks`).
- **Contexts** — built-in `server`, `world`, `dimension-type`, and `gamemode`
  contexts, plus static and default contexts in `contexts.json`.
- **Meta, prefixes & suffixes** — priority-based meta, prefix, and suffix
  stacking.
- **Storage** — YAML, JSON, TOML, SQLite, MySQL, MariaDB, PostgreSQL, MongoDB,
  split storage, and Java-compatible `.json.gz` export/import (`/lp export`,
  `/lp import`, `/lp sync`).
- **Multi-server messaging** — plugin messaging, SQL, PostgreSQL, Redis, NATS,
  and RabbitMQ (`/lp networksync`).
- **Web Editor & diagnostics** — live Web Editor, verbose permission checks,
  permission tree, and action logs (`/lp editor`, `/lp applyedits`,
  `/lp trusteditor`, `/lp verbose`, `/lp tree`, `/lp log`, `/lp info`).
- **Translations** — repository and custom translation bundles
  (`/lp translations`).
- **Vault & plugin API** — provides
  [Vault for golem](https://github.com/golem-mc/vault) permission and chat
  services alongside `luckperms.API`.

Differences from Java LuckPerms a server owner may notice: embedded SQL uses
SQLite instead of H2 (`storage-method: h2` maps to `sqlite`), there is no
PlaceholderAPI bridge, and legacy Bukkit `permissions.yml` / `/lp migration`
importers are omitted (use `/lp export` and `/lp import` instead). See
[PORTING.md](PORTING.md) for porting and developer details.

## Configuration

On first start, LuckPerms writes `config.yml`, `contexts.json`, and storage
files to `plugins/luckperms/`. They match LuckPerms's own
[configuration](https://luckperms.net/wiki/Configuration); run
`/lp reloadconfig` after editing config files or `/lp sync` after editing
storage files. To migrate from Bukkit or Paper, copy `plugins/LuckPerms/` into
`plugins/luckperms/` (or transfer data with `/lp export` and `/lp import`).

## License

MIT — see [LICENSE](LICENSE). Derived from
[LuckPerms](https://github.com/LuckPerms/LuckPerms) by lucko and contributors.
Not affiliated with or endorsed by LuckPerms.
