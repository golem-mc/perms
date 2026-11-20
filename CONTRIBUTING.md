# Contributing

## Setup

Go and the other tools are pinned in `mise.toml`. With
[mise](https://mise.jdx.dev/) installed, run everything through it so you get
those versions (`mise tasks` lists them):

```bash
mise install
mise run build
```

## Before you open a pull request

```bash
mise run fmt         # gofmt
mise run fix         # go fix's modernizations
mise run tidy        # go.mod and go.sum
mise run build
mise run lint        # golangci-lint
mise run test:changed   # repeats the tests your branch adds or changes
```

Run only the tests you add or change while working; CI runs the rest
(`mise run test`) as separate Build, Lint, Test, Fix, Format and Tidy jobs,
and `mise run check` runs all of them locally.

## Commit messages and releases

Pull requests are squash-merged with their title as the commit message, and
the title must be a [conventional commit](https://www.conventionalcommits.org/):

```
feat(storage): add mongodb connection pooling options
fix(commands): preserve context flags on user parent settemp
docs: document split-storage configuration
feat!: change API.CheckPermission return type
```

Use the scope for the part you changed: `core`, `commands`, `storage`,
`messaging`, `webeditor`, or `translations`. A check on each pull request
rejects a title that isn't a conventional commit.

Releases are made by [release-please](https://github.com/googleapis/release-please).
It keeps a release pull request open that bumps the version and updates
`CHANGELOG.md` from the commits on `main`. Merging it tags the release
(`vX.Y.Z`), which is the version servers put in `launcher.json`. Before
1.0.0, `feat` raises the minor version and `fix` the patch version; a
breaking change (`!`) raises the minor version.
