# skmr

`skmr` is a local skill manager for Codex, OpenCode, and Pi.

It stores skills in one library. You can make a skill global, install it in one project, or install a group of related skills. It works offline and never runs skill instructions or scripts.

## Install

With Homebrew:

```sh
brew install faizmokh/tap/skmr
```

With Go 1.25 or newer:

```sh
go install github.com/faizmokh/skmr/cmd/skmr@latest
```

Run `skmr` to open the TUI. Run `skmr list` to use the CLI.

## Main ideas

- **Library:** the central store for your skills.
- **Global skill:** a library skill available in every project.
- **Project skill:** a library skill available in one project only.
- **Group:** a named set of skills installed together.

Keeping unused skills out of the global scope gives agents a smaller skill list.

## Quick start

### Store an existing skill

First, see what `skmr` found:

```sh
skmr list
```

Then move a skill into the library:

```sh
skmr adopt ~/.agents/skills/my-skill --dry-run
skmr adopt ~/.agents/skills/my-skill --yes
```

To adopt all safe skills:

```sh
skmr adopt --all --dry-run
skmr adopt --all --yes
```

An adopted skill stays global until you disable it.

### Keep a skill out of the global scope

```sh
skmr disable my-skill
```

The skill stays in the library but is no longer global. To make it global again:

```sh
skmr enable my-skill
```

### Install a skill in a project

```sh
cd my-project
skmr add my-skill
```

`add` uses the nearest Git root. `install` is an alias:

```sh
skmr install my-skill
```

To target another folder:

```sh
skmr add my-skill --project ../other-project
```

Outside Git, `--project <path>` is required.

Remove a skill that was added directly:

```sh
skmr remove my-skill
```

Repair missing links and refresh group contents:

```sh
skmr sync
```

## Groups

A group is a reusable list of skills. Groups are stored in your personal library and can be used in any project.

Create one:

```sh
skmr group create swift \
  swift-concurrency-expert \
  swiftui-ui-patterns
```

Install or remove the whole group:

```sh
skmr add @swift
skmr remove @swift
```

Groups can include existing groups:

```sh
skmr group create ios @swift ios-debugger-agent
```

List or delete groups:

```sh
skmr group list
skmr group delete ios
```

The group holds the membership list; skills are not tagged with a group. To change a group today, delete it and create it again.

If two requests need the same skill, `skmr` installs it once. Removing one request keeps the skill while another request still needs it.

## Commands

### Find and check skills

| Command | Action |
| --- | --- |
| `skmr list` | List found and managed skills |
| `skmr list --json` | Print the list as JSON |
| `skmr show <name-or-id>` | Show one skill and its instructions |
| `skmr doctor` | Check skills, links, and unfinished work |

### Manage the library

| Command | Action |
| --- | --- |
| `skmr adopt <path>...` | Move skills into the library |
| `skmr adopt --all` | Adopt every safe skill in the scope |
| `skmr enable <name-or-id>` | Make a library skill available |
| `skmr disable <name-or-id>` | Hide a skill but keep it stored |
| `skmr restore <name-or-id>` | Put a skill back and stop managing it |

### Manage project skills

| Command | Action |
| --- | --- |
| `skmr add <skill>...` | Install skills in the current project |
| `skmr add @<group>` | Install a group in the current project |
| `skmr remove <skill-or-@group>...` | Remove direct project requests |
| `skmr sync` | Match project links to its saved requests |
| `skmr group create <name> <members>...` | Create a group |
| `skmr group list` | List groups |
| `skmr group delete <name>` | Delete a group |

### Move and copy managed skills

```sh
skmr move <id> --to-project ./my-project
skmr move <id> --project ./my-project --to-global
skmr copy <id> --project ./one --to-project ./two
```

`move` changes which library owns the skill. `copy` makes a separate skill with a new ID.

### Common flags

| Flag | Action |
| --- | --- |
| `--project <path>` | Use another project |
| `--project auto` | Use the nearest Git root |
| `--global` | Use the personal scope |
| `--dry-run` | Show changes without writing them |
| `--yes`, `-y` | Skip confirmation after a preview |
| `--json` | Print JSON when supported |

Library commands use the global scope by default. `add`, `remove`, and `sync` use the current Git project by default.

## TUI

Run `skmr` or `skmr tui`.

| Key | Action |
| --- | --- |
| `↑` / `↓` or `k` / `j` | Move through the list |
| `Enter`, `→`, `←` | Open or close a display group |
| `/` | Search |
| `Tab` | Switch global and project scope |
| `x` | Open skill actions |
| `A` | Add several skills to the library |
| `!` | Show problems |
| `R` | Refresh |
| `?` | Show help |
| `q` | Quit |

Display groups in the TUI come from folders and plugins. They are different from install groups made with `skmr group create`.

## Safety and recovery

`skmr` does not overwrite files it does not own. Skill and project commands show their changes and support `--dry-run`.

If a write stops halfway through, fix the reported problem and continue it:

```sh
skmr doctor
skmr doctor --recover
```

For another project:

```sh
skmr doctor --recover --project ./my-project
```

Project installs are journaled. Recovery finishes the whole request, including every skill in a group.

Skills with the same name are shown as copies. Choose the copy to keep with `skmr adopt <path> --dry-run`, then run it again with `--yes`. Other writable copies are kept as backups.

## Files

The personal library is stored at `$XDG_DATA_HOME/skmr`, or `~/.local/share/skmr` by default:

```text
skmr/
  manifest.json      # managed skills
  groups.json        # install groups
  library/           # skill files and backups
```

Project state is stored in the project:

```text
.skmr/
  manifest.json      # skills owned by this project
  packages.json      # skills and groups requested by this project
.agents/skills/      # agent discovery links
```

Project package links point to the personal library. On another computer, add the needed skills to its library and run `skmr sync`.

## Supported skill folders

| Location | Agent |
| --- | --- |
| `~/.agents/skills`, `<project>/.agents/skills` | Codex, OpenCode, Pi |
| `~/.codex/skills`, `<project>/.codex/skills` | Codex |
| `$XDG_CONFIG_HOME/opencode/skills`, `<project>/.opencode/skills` | OpenCode |
| `~/.pi/agent/skills`, `<project>/.pi/skills` | Pi |
| `~/.claude/skills`, `<project>/.claude/skills` | OpenCode compatibility |

Built-in and plugin-cache skills are view only. Remote installs, Windows, Pi flat Markdown skills, custom discovery folders, and separate per-agent switches are not supported yet.

Directory behavior follows the [Codex skill documentation](https://learn.chatgpt.com/docs/build-skills), [OpenCode skill documentation](https://opencode.ai/docs/skills/), and [Pi skill documentation](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/skills.md).

## Development

```sh
go build -o bin/skmr ./cmd/skmr
make test
make vet
make release-check
make snapshot
make cross
```
