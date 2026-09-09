# skmr

A local skill library for **Codex, OpenCode, and Pi**, with a scriptable CLI and a keyboard-driven TUI. Written in Go; supports macOS and Linux.

skmr discovers existing `SKILL.md` packages, lets you adopt selected folders into a central library, and enables them through the agents' shared `.agents/skills` directory. It runs entirely offline and never executes skill instructions or helper scripts.

## Install

With Homebrew:

```sh
brew install faizmokh/tap/skmr
```

With Go:

```sh
go install github.com/faizmokh/skmr/cmd/skmr@latest
```

## Build and run

Requires Go 1.25 or later.

```sh
go build -o bin/skmr ./cmd/skmr
./bin/skmr          # TUI when attached to a terminal; help otherwise
./bin/skmr list
```

Alternatively, run `go install ./cmd/skmr` from a source checkout.

## First use

```sh
skmr list
skmr show <id>
skmr adopt ~/.codex/skills/my-skill --dry-run
skmr adopt ~/.codex/skills/my-skill
skmr resolve <id>
skmr disable <id>
skmr enable <id>
skmr restore <id>
```

`adopt`, `resolve`, and `restore` print their exact move/link changes and ask for confirmation. Use `--yes` after reviewing the preview in scripts. `enable` and `disable` apply directly. All five support `--dry-run`, which creates no directories or state.

Use an ID from `list`, or an unambiguous skill name. Adoption takes the path to the containing folder, not to `SKILL.md`. skmr adopts existing folders from standard discovery directories; it does not import arbitrary folders, fetch repositories, edit skills, or manage plugin lifecycles.

### Scopes

Global scope is the default. `--global` makes it explicit.

```sh
skmr list --project ./my-project
skmr list --project auto        # nearest Git root from the current directory
skmr adopt ./my-project/.pi/skills/my-skill --project ./my-project
skmr tui --project ./my-project
```

An explicit project directory is required outside Git. Project views include global and ancestor skills as inherited, read-only entries. Changes apply only to the selected scope. Nested project selection includes ancestors up to the nearest Git root; outside Git, discovery continues to the filesystem root. Managed global and ancestor-project skills remain visible as inherited entries even when disabled. Interrupted operations in inherited libraries are reported with the owning library path; recover them from that scope.

### Commands

| Command | Purpose |
| --- | --- |
| `list [--json]` | Inventory, IDs, agent visibility, scope, and problems |
| `show <id> [--json]` | Metadata, source path, and skill instructions |
| `adopt <path> [--yes]` | Move a discovered skill into the library |
| `resolve <id> [--yes]` | Choose a canonical duplicate and suppress writable copies |
| `enable <id>` | Create the shared discovery link |
| `disable <id>` | Remove owned discovery links; keep library content |
| `restore <id> [--yes]` | Move content back and stop managing it |
| `doctor [--json]` | Diagnose invalid skills, broken links, duplicates, and recovery needs |
| `doctor --recover` | Resume an interrupted operation, then diagnose |
| `tui` | Open the interactive library |
| `version` | Show version, commit, and build date |

### Duplicate conflicts

Skills with the same name form a conflict group. `identical` means their complete package trees match, `divergent` means at least one file, permission, or symlink target differs, and `external` means a read-only, inherited, symlinked, or unreadable copy cannot be consolidated automatically.

Select the copy you want to keep and run `skmr resolve <id> --dry-run`. After review, rerun with `--yes`. skmr moves writable alternatives into tracked backup storage and leaves only `.agents/skills/<name>` discoverable. Restoring the managed skill returns every preserved copy to its original path.

Read commands produce JSON only on stdout with `--json`. Human-readable errors go to stderr. Exit status is `0` for success (including cancellation) and `1` for errors; `doctor` also returns `1` when problems are found. JSON skill content is escaped JSON; human-readable output strips terminal control sequences.

### TUI keys

| Key | Action |
| --- | --- |
| `↑` / `↓`, `k` / `j` | Select a skill |
| `/`, `Enter`, `Esc` | Start search, finish editing, clear search |
| `Tab` | Switch global/project scope (uses nearest Git root if no project was supplied) |
| `f`, `1`–`4` | Cycle or directly select all, managed, discovered, or inherited skills |
| `a`, `c`, `e`, `d`, `r` | Adopt, resolve conflict, enable, disable, restore |
| `y`, `n` / `Esc` | Apply or cancel a change preview |
| `PgUp` / `PgDn` | Scroll instructions or the change preview |
| `R`, `?`, `q` | Refresh, toggle help, quit |

Mouse-capable terminals can click skills, filters, scope, search, and footer actions. The scroll wheel moves through the skill list or the active detail/review pane.

## Files and ownership

Global storage:

```text
$XDG_DATA_HOME/skmr/              # default: ~/.local/share/skmr
  manifest.json                  # versioned ownership records
  library/<id>/<name>/            # complete original skill folder
  library/<id>/.skmr-duplicates/  # preserved non-canonical copies
  .lock                          # advisory process lock
  journal.json                   # present only during an unfinished operation
```

Project storage uses `<project>/.skmr/` with the same layout. Project manifests and discovery symlinks use relative paths, so a completed installation can move with its repository. To share a project installation, commit the library, manifest, and discovery symlinks together; keep `.skmr/.lock`, `.skmr/journal.json`, and `.skmr/.skmr-write-*` out of Git. Do not move a project while an operation is pending recovery.

Adoption moves the complete folder into the library and exposes it through one managed `.agents/skills/<name>` link. Resolve chooses one duplicate as canonical, moves other writable copies into tracked backup storage, and uses the same shared link. Disable removes that link; restoration returns every preserved copy to its exact original path. Restoration retains all edits made while a skill was managed. Unrecorded links and files are never deleted or overwritten.

skmr uses filesystem rename to preserve contents, permissions, and internal relative symlinks. Source and library must be on the same filesystem; choose `XDG_DATA_HOME` accordingly. Relative links escaping the package and absolute links back into the package must be corrected before adoption. Existing symlinked skills, built-in `.system` skills, plugin-cache skills, and inherited entries are read-only. Symlinked parent directories are rejected for writes.

### Recovery

If a write is interrupted, skmr keeps a journal and blocks further management changes in that scope. Reads and diagnostics remain available.

```sh
skmr doctor
# Resolve the reported permission problem or conflicting path, preserving your files.
skmr doctor --recover
# Or: skmr doctor --recover --project ./my-project
```

Recovery resumes the journaled operation; it does not roll it back. It verifies folder identity and symlink targets before acting, and can be repeated safely. Do not delete the journal to bypass a problem. A replaced link is a conflict, not permission to delete the new file. `doctor` reports occupied managed paths even when the skill is disabled. Restore recovery verifies the library folder before removing discovery links.

## Discovery and compatibility

| Location | Agents associated with discovery |
| --- | --- |
| `~/.agents/skills`, `<project>/.agents/skills` | Codex, OpenCode, Pi |
| `~/.codex/skills`, `<project>/.codex/skills` | Codex legacy inventory |
| `$XDG_CONFIG_HOME/opencode/skills`, `<project>/.opencode/skills` | OpenCode |
| `~/.pi/agent/skills`, `<project>/.pi/skills` | Pi |
| `~/.claude/skills`, `<project>/.claude/skills` | OpenCode compatibility inventory |
| `~/.codex/plugins/cache` | Codex plugin inventory, read-only |

`XDG_CONFIG_HOME` defaults to `~/.config`. Skill directories are scanned recursively, stopping at a `SKILL.md` package; `.git` and `node_modules` are skipped. Symlink cycles, unreadable directories, malformed frontmatter, and duplicate names are reported. Duplicate packages are compared by content, permissions, and symlink targets. Adoption requires a unique portable name/description package; use `resolve <id>` when copies conflict.

“Enabled” means skmr's discovery links are enabled. It does not override agent permissions, project trust, native disabled-skill settings, or an already-running session. Agent badges describe directory-based visibility, not a live query of each agent. Shared enablement applies to all three agents; another unmanaged or inherited copy may remain discoverable after disabling a managed copy. Restart or reload an agent if changes do not appear.

Custom discovery settings, administrator-installed skills, Pi flat Markdown skills, remote installation, independent per-agent toggles, and Windows support are outside v1. SKILL.md previews are limited to 1 MiB. No agent executable is required.

Directory behavior is based on the official [Codex skill documentation](https://learn.chatgpt.com/docs/build-skills), [OpenCode skills documentation](https://opencode.ai/docs/skills/), and [Pi skill documentation](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/skills.md).

## Development

```sh
make test       # race-enabled tests using isolated temporary homes
make vet
make build
make release-check
make snapshot
make cross      # Darwin/Linux, amd64/arm64 binaries under dist/
```

`release-check` accepts GoReleaser's expected deprecation warning for the selected Homebrew formula publisher while still failing on an invalid configuration.

Both interfaces call `internal/manager`. Agent roots live in `internal/agents`, parsing/discovery in `internal/skills`, and terminal sanitization in `internal/terminal`. There is no database, daemon, network client, or agent-specific config rewriting.
