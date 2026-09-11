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

Launch `skmr` to open the interactive library. On a new global setup, skmr finds writable local skills and opens a checklist. Safe skills are selected by default, duplicate names wait for you to choose the copy to keep, and Codex system and plugin-cache skills are ignored.

```sh
skmr list
skmr show <id>
skmr adopt ~/.codex/skills/my-skill --dry-run
skmr adopt ~/.codex/skills/my-skill
skmr adopt ~/.codex/skills/one ~/.pi/agent/skills/two
skmr adopt --all --dry-run
skmr adopt --all --yes
skmr disable <id>
skmr enable <id>
skmr move <id> --to-project ./my-project
skmr copy <id> --project ./my-project --to-global
skmr restore <id>
```

`adopt`, `move`, `copy`, and `restore` print their exact move/link changes and ask for confirmation. `adopt` accepts multiple paths; `adopt --all` selects every valid writable skill with a unique name and reports duplicate-name groups that need an explicit path choice. Use `--yes` after reviewing the preview in scripts. `enable` and `disable` apply directly. Every write command supports `--dry-run`, which creates no directories or state.

Use an ID from `list`, or an unambiguous skill name. Adoption takes the path to the containing folder, not to `SKILL.md`. skmr adopts existing folders from standard discovery directories; it does not import arbitrary folders, fetch repositories, edit skills, or manage plugin lifecycles.

### Scopes

Global scope is the default. `--global` makes it explicit.

```sh
skmr list --project ./my-project
skmr list --project auto        # nearest Git root from the current directory
skmr adopt ./my-project/.pi/skills/my-skill --project ./my-project
skmr move <id> --global --to-project ./my-project
skmr move <id> --project ./my-project --to-global
skmr copy <id> --project ./my-project --to-project ../other-project
skmr tui --project ./my-project
```

An explicit project directory is required outside Git. Project views include global and ancestor skills as inherited, view-only entries. Changes apply only to the selected scope. Nested project selection includes ancestors up to the nearest Git root; outside Git, discovery continues to the filesystem root. Managed global and ancestor-project skills remain visible as inherited entries even when disabled. Interrupted operations in inherited libraries are reported with the owning library path; recover them from that scope.

### Commands

| Command | Purpose |
| --- | --- |
| `list [--json]` | Inventory, IDs, agent visibility, scope, and problems |
| `show <id> [--json]` | Metadata, source path, and skill instructions |
| `adopt <path>... [--yes]` | Keep the selected copies, back up duplicates, and manage the skills |
| `adopt --all [--yes]` | Adopt every safe unmanaged skill in the current scope |
| `enable <id>` | Create the shared discovery link |
| `disable <id>` | Remove owned discovery links; keep library content |
| `move <id> <destination>` | Transfer ownership using `--to-global` or `--to-project <path>` |
| `copy <id> <destination>` | Create an independent managed copy in another scope |
| `restore <id> [--yes]` | Move content back and stop managing it |
| `doctor [--json]` | Diagnose invalid skills, broken links, duplicates, and recovery needs |
| `doctor --recover` | Resume an interrupted operation, then diagnose |
| `tui` | Open the interactive library |
| `version` | Show version, commit, and build date |

### Duplicate copies

Skills with the same name appear as copies. **Same copies** have matching contents. **Same content, different agent config** means only `agents/openai.yaml` differs or is missing (including its otherwise-empty parent directory). **Different copies** have other differences in files, paths, entry types, or link targets. Agent configuration remains visible in comparisons and is preserved during adoption and restoration. File and folder permissions do not affect this comparison. **View-only copies** include at least one copy that skmr cannot move automatically.

Structured JSON keeps its existing field names and values for compatibility.

Run `skmr adopt <path> --dry-run` on the copy you want to keep. After review, rerun with `--yes`. skmr moves writable alternatives into tracked backup storage and leaves only `.agents/skills/<name>` discoverable. Restoring the managed skill returns every preserved copy to its original path.

Read commands produce JSON only on stdout with `--json`. Human-readable errors go to stderr. Exit status is `0` for success (including cancellation) and `1` for errors; `doctor` also returns `1` when problems are found. JSON skill content is escaped JSON; human-readable output strips terminal control sequences.

### TUI keys

The TUI is a skill library and discovery controller. **Library** skills are stored by skmr in the current scope and can be enabled or disabled through the shared discovery folder. **Other folders** contains local skills that skmr found but does not store. In project scope, **Parent scopes** contains global and ancestor-project skills that are visible here but owned elsewhere.

| Key | Action |
| --- | --- |
| `↑` / `↓`, `k` / `j` | Select a group or skill |
| `Enter`, `→`, `←` | Toggle a group, expand/enter it, return to its header/collapse it |
| `/`, `Enter`, `Esc` | Start search, finish editing, clear search |
| `Tab` | Switch global/project scope (uses nearest Git root if no project was supplied) |
| `f`, `1`–`4` | Cycle or directly select All skills, Library, Other folders, or project-only Parent scopes |
| `x` | Open Actions for the selected skill |
| `A` | Open the multi-select Add skills flow |
| `↑` / `↓`, `k` / `j`, `Enter` | Choose and run an action while Actions is open |
| `!` | Open the deduplicated problems list and recovery guidance |
| `y`, `n` / `Esc` | Apply or cancel a change preview |
| `PgUp` / `PgDn` | Scroll details, instructions, help, problems, or a change preview |
| `R`, `?`, `q` | Refresh, toggle help, quit |

All skill operations start in **Actions**. Inside the menu, use `i` to read instructions, `v` to compare different copies, `o` to open a parent skill’s owning scope, `Space` (or the applicable `e`/`d`) to enable or disable, and `a`, `c`, `m`, `p`, or `r` to add, keep a copy, move, copy, or remove. Only available actions are shown, and these shortcuts work only while Actions is open. Press `Esc` to return.

The **Add skills** flow is the batch exception. Press `Space` to select, `a` to select every safe skill, `n` to clear, and `←` or `→` to browse duplicate-name copies. Press `Enter` to review one combined filesystem plan. The first-run version can be skipped with `Esc`; skmr remembers the choice, and `A` reopens the flow later.

Plugin bundles and nested skill folders appear as collapsed groups with skill counts. Skills directly inside a discovery root remain standalone. Select a group to inspect its source, scope, version, and member states; select an individual skill to manage it. Group headers never apply changes to their members.

Below 76 columns, the TUI shows one pane at a time. The skill list opens first; press `Enter` for details, `x` then `i` for instructions, and `Esc` to return. At the 30×10 minimum, secondary guidance is hidden so the Actions, help, and quit controls remain available.

For different copies or copies with different agent config, open Actions with `x`, then press `v` to compare complete package contents side by side. The selected copy stays on the left and the comparison copy appears on the right, with aligned line numbers and filename-aware syntax highlighting. Added lines use `+`, removed lines use `-`, and file and hunk headers are highlighted. Use `←` and `→` to pan long lines, or `[` and `]` to switch comparison copies when more than two locations exist.

Search matches group labels as well as skill names, descriptions, and paths, and reveals matching children automatically. Views hide empty groups and show matching/total counts. Clear search to restore your expansion choices, which are retained per scope for the current session. Library skills retain their original folder grouping after adoption or disabling. Separate plugin versions and discovery locations remain separate groups. CLI text inventories remain flat; JSON results include optional `group` metadata.

Mouse-capable terminals can click group headers to select and expand/collapse them, or click skills, views, scope, search, and footer actions. The scroll wheel moves through the skill list or the active detail/review pane.

## Files and ownership

Global storage:

```text
$XDG_DATA_HOME/skmr/              # default: ~/.local/share/skmr
  manifest.json                  # versioned ownership records
  library/<id>/<name>/            # complete original skill folder
  library/<id>/.skmr-duplicates/  # preserved additional copies
  .lock                          # advisory process lock
  journal.json                   # present only during an unfinished operation
  batch.json                     # present only during an unfinished batch adoption
  transfer.json                  # present in both scopes during an unfinished transfer
  setup.json                     # first-run migration completion or dismissal
```

Project storage uses `<project>/.skmr/` with the same operational layout except for the global-only setup marker. Project manifests and discovery symlinks use relative paths, so a completed installation can move with its repository. To share a project installation, commit the library, manifest, and discovery symlinks together; keep `.skmr/.lock`, `.skmr/journal.json`, `.skmr/transfer.json`, `.skmr/.skmr-write-*`, and `.skmr/.skmr-copy-*` out of Git. Do not move a project while an operation is pending recovery.

Adoption moves the selected folder into the library and exposes it through one managed `.agents/skills/<name>` link. When duplicates exist, the selected copy is kept and other writable copies move into tracked backup storage. Move transfers ownership and preserves the ID and enabled state. Copy creates an independent package with a new ID and the same enabled state. Disable removes the discovery link. Stopping management after a transfer places the skill in the destination scope's `.agents/skills` directory; older records still restore to their original locations. Restoration retains all edits made while a skill was managed. Unrecorded links and files are never deleted or overwritten.

skmr uses filesystem rename to preserve contents, permissions, and internal relative symlinks. Source and library must be on the same filesystem; choose `XDG_DATA_HOME` accordingly. Relative links escaping the package and absolute links back into the package must be corrected before adoption. Existing symlinked skills, built-in `.system` skills, plugin-cache skills, and inherited entries are view only. Symlinked parent directories are rejected for writes.

### Recovery

If a write is interrupted, skmr keeps a single-operation, batch, or transfer journal and blocks further management changes in that scope. Reads and diagnostics remain available.

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
| `~/.codex/plugins/cache` | Codex plugin inventory, view only |

`XDG_CONFIG_HOME` defaults to `~/.config`. Skill directories are scanned recursively, stopping at a `SKILL.md` package; `.git` and `node_modules` are skipped. Symlink cycles, unreadable directories, malformed frontmatter, and duplicate names are reported. Duplicate packages are compared by content, paths, entry types, and symlink targets, with differences limited to `agents/openai.yaml` classified separately. Full-package verification still includes every file. Permissions are ignored. When copies differ, adopt the copy you want to keep.

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
