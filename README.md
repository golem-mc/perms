# LuckPerms for golem

An unofficial port of [LuckPerms](https://luckperms.net/) to
[golem](https://github.com/golem-mc/golem). It replaces golem's permission
system with LuckPerms's groups, tracks, contexts, prefixes and suffixes, the
`/lp` commands, the Web Editor and the same storage formats as the Bukkit and
Paper plugin.

## Install

```bash
golem-launcher plugin add github.com/golem-mc/perms
```

Requires golem `4f95109` or newer. For chat prefixes and suffixes in plugins
that use [Vault for golem](https://github.com/golem-mc/vault), install it too
(optional):

```bash
golem-launcher plugin add github.com/golem-mc/vault
```

## Getting started

Operators can use every command by default (see `commands-allow-op` below).
Run these from the console or in game:

```
/lp creategroup moderator
/lp group moderator permission set essentials.kick true
/lp group moderator meta setprefix 100 "&c[Mod] "
/lp user Notch parent add moderator
/lp user Notch info
```

Every player is in the `default` group. Give it permissions that everyone
should have with `/lp group default permission set <node> true`. Add
`temporary` durations (`/lp user Notch permission settemp fly.use true 1d`) and
contexts (`world=nether`, `server=survival`) at the end of a command.
Use `/lp editor` for the browser-based Web Editor.

## Commands

The command is `/luckperms`, also `/lp`, `/perm`, `/perms`, `/permission` and
`/permissions`. `user`, `group`, `track` also work as `u`, `g`, `t`; in a user
or group command `permission` = `perm` = `p`, `parent` = `group` = `g`,
`meta` = `m`, `info` = `i`, `editor` = `e`.
`<user>` is a name, a UUID, or, with `resolve-command-selectors: true` in `config.yml`, a
selector (`@s`, `@p`, `@a`, `@r`). Most
commands that change data accept a duration and a context list at the end.
Run `/lp help` to see the commands you may use. Each command needs the permission
`luckperms.<command>` (for subcommands `luckperms.<user|group|track>.<sub>`,
for example `luckperms.user.permission.set`).

### General

| Usage | What it does | Permission |
|---|---|---|
| `/lp info` | Plugin version, storage and messaging status | `luckperms.info` |
| `/lp sync` | Reload all data from storage | `luckperms.sync` |
| `/lp networksync` | Sync, then tell other servers to sync | `luckperms.sync` |
| `/lp reloadconfig` | Reload `config.yml` | `luckperms.reloadconfig` |
| `/lp editor [all\|online\|users\|groups] [filter]` | Open the Web Editor | `luckperms.editor` |
| `/lp applyedits <code> [--force]` | Apply changes saved in the Web Editor | `luckperms.applyedits` |
| `/lp trusteditor <id>` | Trust a Web Editor session | `luckperms.trusteditor` |
| `/lp search [comparison] <query> [page]` | Find users and groups with a permission or group | `luckperms.search` |
| `/lp check <user> <permission>` | Check whether a user has a permission | `luckperms.user.permission.check` |
| `/lp verbose <on\|record\|off\|paste\|upload\|command> [filter]` | Log permission checks | `luckperms.verbose` |
| `/lp tree [scope] [player] [--web]` | Show the permission tree | `luckperms.tree` |
| `/lp bulkupdate <all\|users\|groups> <delete\|update> ...` | Change or delete many nodes at once | `luckperms.bulkupdate` |
| `/lp export <file\|--upload> [--without-users] [--without-groups]` | Export data to a `.json.gz` file | `luckperms.export` |
| `/lp import <file\|--web code> [--replace]` | Import an export | `luckperms.import` |
| `/lp translations [install]` | List or download translations | `luckperms.translations` |

### Users: `/lp user <user> ...`

| Usage | What it does |
|---|---|
| `info` | Show the user's groups, prefix, suffix and permission counts |
| `permission info` / `set <node> [true\|false] [context]` / `unset <node>` | List, set or remove a permission |
| `permission settemp <node> <true\|false> <duration> [temporary-modifier] [context]` / `unsettemp <node> [duration]` | Temporary permissions |
| `permission check <node>` | Check whether the user has a permission |
| `permission clear [context]` | Remove all permissions |
| `parent info` / `add <group>` / `set <group>` / `remove <group>` | Manage the groups a user is in |
| `parent addtemp <group> <duration>` / `removetemp <group>` | Temporary group membership |
| `parent settrack <track> <group>` / `cleartrack <track>` / `clear` / `switchprimarygroup <group>` | Track and primary group handling |
| `meta info` / `set <key> <value>` / `unset <key>` / `settemp` / `unsettemp` | Meta values |
| `meta addprefix\|addsuffix\|setprefix\|setsuffix <priority> <text>` / `removeprefix\|removesuffix <priority> [text]` | Prefixes and suffixes (also `addtemp…`, `settemp…`, `removetemp…`) |
| `meta clear [type]` | Remove meta |
| `promote <track>` / `demote <track>` | Move along a track |
| `showtracks` | Show the tracks the user is on |
| `editor` | Open the Web Editor for this user |
| `clone <user>` / `clear` | Copy this user's data to another, or delete it |

Each subcommand needs `luckperms.user.<subcommand>` (for example
`luckperms.user.promote`).

### Groups: `/lp group <group> ...`

Groups support the same `info`, `permission`, `parent`, `meta`, `editor`,
`showtracks`, `clone` and `clear` subcommands as users, plus:

| Usage | What it does |
|---|---|
| `listmembers [page]` | List the users and groups that inherit this group |
| `setweight <weight>` | Set the weight used to order groups |
| `setdisplayname <name>` | Set a display name |
| `rename <new-name>` | Rename the group |
| `/lp creategroup <group> [weight] [displayname]` | Create a group (`luckperms.creategroup`) |
| `/lp deletegroup <group>` | Delete a group (`luckperms.deletegroup`) |
| `/lp listgroups` | List all groups (`luckperms.listgroups`) |

### Tracks: `/lp track <track> ...`

| Usage | What it does |
|---|---|
| `info` | Show the groups on the track |
| `append <group>` / `insert <group> <position>` / `remove <group>` | Edit the track |
| `clear` / `rename <name>` / `clone <name>` | Empty, rename or copy the track |
| `editor` | Open the Web Editor |
| `/lp createtrack <track>` | Create a track (`luckperms.createtrack`) |
| `/lp deletetrack <track>` | Delete a track (`luckperms.deletetrack`) |
| `/lp listtracks` | List tracks (`luckperms.listtracks`) |

### Logs: `/lp log ...`

| Usage | What it does | Permission |
|---|---|---|
| `recent [user] [page]` | Recent changes | `luckperms.log.recent` |
| `userhistory <user> [page]` / `grouphistory <group> [page]` / `trackhistory <track> [page]` | History of one object | `luckperms.log.userhistory` / `grouphistory` / `trackhistory` |
| `search <query> [page]` | Search the log | `luckperms.log.search` |
| `notify [on\|off]` | Toggle log messages in chat | `luckperms.log.notify` |

## Permissions

- All `luckperms.*` command nodes above default to operators
  (`commands-allow-op: true`). Give them to a group with
  `/lp group admin permission set luckperms.* true`.
- `luckperms.autoop` — with `auto-op: true`, players who have it are made
  operators.
- Nodes support wildcards (`essentials.*`), regex (`r=...`), shorthand
  (`essentials.(kick|ban)`) and negation (`false`) as in LuckPerms.
- With `argument-based-command-permissions: true`, restrict what a user may
  change: `luckperms.modify.group.<group>`, `luckperms.modify.user.self`,
  `luckperms.modify.user.other`, `luckperms.modify.permission.<node>` and
  `luckperms.<action>.<group>`.
- `luckperms.verbose.command.others` lets a user run `/lp verbose command`
  for someone else.
- Contexts available in commands and the Web Editor: `server`, `world`,
  `dimension-type` and `gamemode`, plus static and default contexts you define
  in `contexts.json`.

## Configuration

On first start the plugin writes `config.yml` and the data files to
`plugins/luckperms/`. You can also create a `contexts.json` there to define
static contexts. Run `/lp reloadconfig` after editing the
config. Main options and defaults, with the full list in the
[LuckPerms configuration wiki](https://luckperms.net/wiki/Configuration):

| Option | Default | Meaning |
|---|---|---|
| `server` | `global` | Name of this server for the `server` context |
| `storage-method` | `yaml` | `yaml`, `json`, `toml`, `sqlite`, `mysql`, `mariadb`, `postgresql`, `mongodb`, or a `*-combined` file variant |
| `data` | `localhost`, db `minecraft`, user `root` | Database address and credentials |
| `split-storage.enabled` | `false` | Use a different storage per data type |
| `watch-files` | `true` | Reload data when its files change on disk |
| `sync-minutes` | `-1` | Periodically reload from storage (off) |
| `messaging-service` | `auto` | `sql`, `redis`, `nats`, `rabbitmq`, `pluginmsg` or `none` to sync several servers |
| `auto-push-updates` | `true` | Push changes to other servers |
| `primary-group-calculation` | `parents-by-weight` | How the primary group is picked |
| `temporary-add-behaviour` | `deny` | What happens when a temporary node already exists |
| `include-global` / `apply-global-groups` | `true` | Apply `server=global` data |
| `apply-wildcards`, `apply-regex`, `apply-shorthand` | `true` | Node expansion rules |
| `apply-bukkit-child-permissions` / `apply-bukkit-default-permissions` | `true` | Honour permissions' children and defaults from plugins |
| `meta-formatting` | highest prefix/suffix | How prefixes and suffixes are stacked |
| `world-rewrite` / `group-weight` | empty | Treat worlds as one, assign weights to groups |
| `enable-ops` / `auto-op` | `true` / `false` | Whether `op` status grants all permissions; give op to `luckperms.autoop` holders |
| `commands-allow-op` | `true` | Operators may use `/lp` |
| `commands-read-only-mode` | off | Only allow read-only commands for players or the console |
| `commands-rate-limit` | `false` | One command per 500ms per player |
| `log-notify` | `true` | Tell staff with `luckperms.log.notify` about changes |
| `auto-install-translations` | `false` | Download translation bundles on start |
| `vault-*` | see below | Options for Vault users |

Data is stored beside the config (`yaml-storage/`, `json-storage/`,
`toml-storage/` or `luckperms-sqlite.db`). Run `/lp sync` after editing data
files by hand. Translations go in `translations/custom/` as
`luckperms_<lang>.properties`; install the official ones with
`/lp translations install`.

## Working with other plugins

With Vault installed, this plugin provides its permission and chat services (groups,
prefixes, suffixes, and permissions for online and offline players). The
`vault-group-use-displaynames`, `vault-npc-group`, `vault-npc-op-status`,
`use-vault-server`, `vault-server`, `vault-include-global` and
`vault-ignore-world` options work as on Bukkit. Plugins written for golem can
also use the `luckperms.API` service.

## Migrating from Bukkit or Paper

Copy `plugins/LuckPerms/` from your old server to `plugins/luckperms/`, or
run `/lp export` on the old server and `/lp import` here. YAML, JSON, TOML,
SQLite, MySQL, MariaDB, PostgreSQL and MongoDB data and exports from Java
LuckPerms load as they are.

## Differences from LuckPerms

- Embedded SQL uses SQLite instead of H2; `storage-method: h2` is treated as
  `sqlite` (export from H2 on the old server and import here).
- No PlaceholderAPI bridge.
- No importers for other permission plugins (`/lp migration`) or Bukkit's
  `permissions.yml`; use `/lp export` and `/lp import`.
- `vault-unsafe-lookups` is not read.

See the [LuckPerms wiki](https://luckperms.net/wiki/Home) for the rarely used
corners.

## License

MIT, see [LICENSE](LICENSE). Derived from
[LuckPerms](https://github.com/LuckPerms/LuckPerms) by lucko and contributors.
Not affiliated with or endorsed by LuckPerms.
