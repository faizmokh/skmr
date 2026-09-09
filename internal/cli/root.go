// Package cli exposes the manager through scriptable Cobra commands.
package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/faizmokh/skmr/internal/manager"
	"github.com/faizmokh/skmr/internal/skills"
	"github.com/faizmokh/skmr/internal/terminal"
	"github.com/faizmokh/skmr/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

func New(build BuildInfo) *cobra.Command {
	var project string
	var global bool
	root := &cobra.Command{Use: "skmr", Short: "Organize agent skills across Codex, OpenCode, and Pi", SilenceUsage: true, SilenceErrors: true}
	root.Version = build.Version
	root.SetVersionTemplate("skmr {{.Version}}\n")
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	root.SetIn(os.Stdin)
	root.PersistentFlags().BoolVar(&global, "global", false, "Manage personal skills (default)")
	root.PersistentFlags().StringVar(&project, "project", "", "Manage project skills; use 'auto' for the nearest Git root")
	root.MarkFlagsMutuallyExclusive("global", "project")
	service := func() (*manager.Service, error) { return manager.Environment(project) }
	launch := func(cmd *cobra.Command, args []string) error {
		s, err := service()
		if err != nil {
			return err
		}
		return tui.Run(s)
	}
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if !isTerminal(cmd.InOrStdin()) || !isTerminal(cmd.OutOrStdout()) {
			return cmd.Help()
		}
		return launch(cmd, args)
	}
	root.Args = cobra.NoArgs
	root.AddCommand(&cobra.Command{Use: "tui", Short: "Open the interactive skill library", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if !isTerminal(cmd.InOrStdin()) || !isTerminal(cmd.OutOrStdout()) {
			return fmt.Errorf("the TUI needs an interactive terminal; use list instead")
		}
		return launch(cmd, args)
	}})
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Show build version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "skmr %s\ncommit: %s\nbuilt: %s\n", build.Version, build.Commit, build.Date)
			return err
		},
	})
	for _, kind := range []string{"list", "show", "doctor"} {
		var asJSON, recover bool
		cmd := &cobra.Command{Use: kind, Short: map[string]string{"list": "List discovered and managed skills", "show": "Show a skill and its instructions", "doctor": "Check links, conflicts, and interrupted operations"}[kind], Args: cobra.NoArgs}
		if kind == "show" {
			cmd.Use = "show <id>"
			cmd.Args = cobra.ExactArgs(1)
		}
		cmd.Flags().BoolVar(&asJSON, "json", false, "Write structured JSON")
		if kind == "doctor" {
			cmd.Flags().BoolVar(&recover, "recover", false, "Resume an interrupted operation after resolving its conflict")
		}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			s, err := service()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch kind {
			case "list":
				result, e := s.List()
				if e != nil {
					return e
				}
				if asJSON {
					return writeJSON(out, result)
				}
				if len(result.Skills) == 0 {
					fmt.Fprintln(out, "No skills found in the standard directories.")
				}
				table := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
				if len(result.Skills) > 0 {
					fmt.Fprintln(table, "ID\tNAME\tSCOPE\tSTATUS\tAGENTS")
				}
				for _, item := range result.Skills {
					fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", item.ID, oneLine(item.Name), item.Scope, status(item), strings.Join(item.Agents, ","))
				}
				if e = table.Flush(); e != nil {
					return e
				}
				for _, item := range result.Skills {
					for _, issue := range item.Issues {
						fmt.Fprintf(out, "Warning: %s: %s\n", oneLine(item.Name), terminal.Safe(issue))
					}
				}
				for _, issue := range result.Issues {
					fmt.Fprintln(out, "Warning:", terminal.Safe(issue))
				}
				return nil
			case "show":
				item, e := s.Show(args[0])
				if e != nil {
					return e
				}
				content, e := skills.Read(item.Path)
				if e != nil {
					return e
				}
				if asJSON {
					return writeJSON(out, struct {
						Skill   skills.Skill `json:"skill"`
						Content string       `json:"content"`
					}{item, string(content)})
				}
				fmt.Fprintf(out, "%s · %s · %s\n%s\nAgents: %s\n", oneLine(item.Name), item.Scope, status(item), terminal.Safe(item.Path), strings.Join(item.Agents, ", "))
				for _, issue := range item.Issues {
					fmt.Fprintln(out, "Warning:", terminal.Safe(issue))
				}
				fmt.Fprintln(out, "\n"+terminal.Safe(string(content)))
				return nil
			case "doctor":
				if recover {
					if e := s.Recover(); e != nil {
						return e
					}
				}
				report, e := s.Doctor()
				if e != nil {
					return e
				}
				if asJSON {
					if e = writeJSON(out, report); e != nil {
						return e
					}
				} else if report.Healthy {
					fmt.Fprintln(out, "No skill problems found.")
				} else {
					for _, issue := range report.Issues {
						fmt.Fprintln(out, "Warning:", terminal.Safe(issue))
					}
				}
				if !report.Healthy {
					return fmt.Errorf("skill problems found; see the report above")
				}
				return nil
			}
			return nil
		}
		root.AddCommand(cmd)
	}
	for _, action := range []string{"adopt", "resolve", "enable", "disable", "restore"} {
		var yes, dry bool
		arg := "<id>"
		if action == "adopt" {
			arg = "<path>"
		}
		cmd := &cobra.Command{Use: action + " " + arg, Short: map[string]string{"adopt": "Move an existing skill into the managed library", "resolve": "Choose the canonical copy of a conflicting skill", "enable": "Create shared discovery links for a managed skill", "disable": "Remove discovery links, keeping the library copy", "restore": "Return an adopted skill to its original location"}[action], Args: cobra.ExactArgs(1)}
		cmd.Flags().BoolVar(&dry, "dry-run", false, "Preview changes without writing files")
		if action == "adopt" || action == "resolve" || action == "restore" {
			cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Apply the preview without prompting")
		}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			s, err := service()
			if err != nil {
				return err
			}
			p, err := s.Preview(action, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), terminal.Safe(p.String()))
			if dry {
				return nil
			}
			if (action == "adopt" || action == "resolve" || action == "restore") && !yes {
				if !isTerminal(cmd.InOrStdin()) {
					return fmt.Errorf("review with --dry-run, then pass --yes in noninteractive use")
				}
				fmt.Fprint(cmd.OutOrStdout(), "Apply these changes? [y/N] ")
				response, e := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if e != nil {
					return e
				}
				response = strings.ToLower(strings.TrimSpace(response))
				if response != "y" && response != "yes" {
					fmt.Fprintln(cmd.OutOrStdout(), "No changes made.")
					return nil
				}
			}
			if err = s.Apply(p); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Done: %s %s.\n", action, oneLine(p.Record.Name))
			return nil
		}
		root.AddCommand(cmd)
	}
	return root
}
func oneLine(s string) string {
	return strings.NewReplacer("\n", " ", "\t", " ").Replace(terminal.Safe(s))
}
func isTerminal(v any) bool { f, ok := v.(*os.File); return ok && term.IsTerminal(int(f.Fd())) }
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
func status(s skills.Skill) string {
	state := "discovered"
	if s.Managed {
		if s.Enabled {
			state = "enabled"
		} else {
			state = "disabled"
		}
	}
	if s.Inherited {
		state += " / inherited"
	} else if s.ReadOnly {
		state += " / read-only"
	}
	if s.ConflictKind != "" {
		state += " / conflict:" + s.ConflictKind
	}
	return state
}
