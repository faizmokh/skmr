# skmr

A local skill manager for Codex, OpenCode, and Pi. Keep skills in one personal library and make them available globally or per project.

## Install

```sh
brew install faizmokh/tap/skmr
```

Or with Go 1.25+:

```sh
go install github.com/faizmokh/skmr/cmd/skmr@latest
```

## Quick start

Run `skmr` for the TUI (`?` for help), or use the CLI:

```sh
skmr list                              # discover skills
skmr add /path/to/my-skill --global     # store a local skill and enable it globally
skmr disable my-skill                  # keep it in the library, hide it globally
skmr enable my-skill                   # enable it globally again

cd my-project
skmr add my-skill                      # install a library skill in this project
skmr remove my-skill                   # remove it from this project
skmr sync                             # repair project links
```

Project commands use the nearest Git root. Use `--project <path>` to target another folder or a project outside Git. Use `--dry-run` to preview changes.

Opening a scope automatically moves unambiguous writable skills into the library. Built-in and plugin skills stay view-only.

## Remote skills

Requires Node.js and a network connection.

```sh
skmr search "react testing"
skmr add https://skills.sh/vercel-labs/skills/find-skills --global
skmr update find-skills
```

GitHub URLs and skills.sh packs also work. Use `--skill <name>` to select a skill from a repository or pack.

## Groups

```sh
skmr group create web skill-one skill-two
skmr add @web
```

Groups are presets: later group changes do not affect installed project skills.

## Troubleshooting

```sh
skmr doctor
skmr doctor --recover   # finish an interrupted operation
skmr --help            # all commands and flags
```

Skills live in `~/.local/share/skmr` (or `$XDG_DATA_HOME/skmr`). Projects save their requests in `.skmr/packages.json` and link skills through `.agents/skills`.

## Development

```sh
make build
make test
make vet
```
