# skmr

Manage skills for Codex, OpenCode, and Pi from one personal library.

## Install

```sh
brew install faizmokh/tap/skmr
```

Or with Go 1.25+:

```sh
go install github.com/faizmokh/skmr/cmd/skmr@latest
```

## Use

Run `skmr` for the interactive library (`?` for help), or use the CLI:

```sh
skmr add /path/to/my-skill             # store in the library
skmr add https://github.com/owner/repo --skill my-skill
skmr list
skmr enable my-skill                  # make available globally
skmr disable my-skill                 # keep in library, hide globally
skmr update my-skill                  # refresh a remote skill
```

`add` defaults to the library, wherever you run it. New skills stay disabled; existing skills keep their activation and placements. Use `--global` to add and enable in one step. Remote skills require Node.js and a network connection; GitHub and skills.sh URLs are supported.

To install into a project:

```sh
skmr add my-skill --project auto       # nearest Git project
skmr remove my-skill                  # remove from nearest Git project
skmr sync                             # repair project links
```

Use `--project <path>` for another folder or a project outside Git. Use `--dry-run` to preview changes.

Groups are reusable presets:

```sh
skmr group create web skill-one skill-two
skmr add @web --project auto
```

Changing a group does not change existing project installations.

## Help

Use `skmr search "query"` to find remote skills, `skmr doctor` to check problems, and `skmr doctor --recover` to resume an interrupted operation. Run `skmr --help` for all commands.

Skills live in `~/.local/share/skmr` (or `$XDG_DATA_HOME/skmr`). Projects track requests in `.skmr/packages.json` and link skills through `.agents/skills`. Discovered writable skills are automatically adopted into the library; built-in and plugin skills stay view-only.

## Development

```sh
make build
make test
make vet
```
