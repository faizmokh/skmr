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
	root.PersistentFlags().BoolVar(&global, "global", false, "Manage the personal skill scope explicitly")
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
		cmd := &cobra.Command{Use: kind, Short: map[string]string{"list": "List available and managed skills", "show": "Show a skill and its instructions", "doctor": "Check links, copies, and interrupted operations"}[kind], Args: cobra.NoArgs}
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
					fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", item.ID, oneLine(item.Name), displayScope(item), status(item), strings.Join(item.Agents, ","))
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
				fmt.Fprintf(out, "%s · %s · %s\n%s\nAgents: %s\n", oneLine(item.Name), displayScope(item), status(item), terminal.Safe(item.Path), strings.Join(item.Agents, ", "))
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
	{
		var yes, dry, all bool
		cmd := &cobra.Command{
			Use:   "adopt <path>...",
			Short: "Move existing skills into the managed library",
			Args: func(cmd *cobra.Command, args []string) error {
				if all && len(args) > 0 {
					return fmt.Errorf("use --all or explicit paths, not both")
				}
				if !all && len(args) == 0 {
					return fmt.Errorf("provide at least one skill path or use --all")
				}
				return nil
			},
		}
		cmd.Flags().BoolVar(&all, "all", false, "Adopt every safe unmanaged skill in this scope")
		cmd.Flags().BoolVar(&dry, "dry-run", false, "Preview changes without writing files")
		cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Apply the preview without prompting")
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			s, err := service()
			if err != nil {
				return err
			}
			paths := append([]string{}, args...)
			if all {
				result, listErr := s.List()
				if listErr != nil {
					return listErr
				}
				paths = manager.SafeAdoptionPaths(result)
				conflicts := manager.SortedConflictNames(result)
				if len(conflicts) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "Skipped duplicate-name skills: %s. Pass one path for each copy you want to keep.\n", strings.Join(conflicts, ", "))
				}
				if len(paths) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No safe unmanaged skills to add.")
					return nil
				}
			}

			if len(paths) == 1 {
				plan, previewErr := s.Preview("adopt", paths[0])
				if previewErr != nil {
					return previewErr
				}
				fmt.Fprintln(cmd.OutOrStdout(), terminal.Safe(plan.String()))
				if dry {
					return nil
				}
				if !yes {
					if !isTerminal(cmd.InOrStdin()) {
						return fmt.Errorf("review with --dry-run, then pass --yes in noninteractive use")
					}
					fmt.Fprint(cmd.OutOrStdout(), "Apply these changes? [y/N] ")
					response, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
					if readErr != nil {
						return readErr
					}
					response = strings.ToLower(strings.TrimSpace(response))
					if response != "y" && response != "yes" {
						fmt.Fprintln(cmd.OutOrStdout(), "No changes made.")
						return nil
					}
				}
				if err = s.Apply(plan); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Done: adopted %s.\n", oneLine(plan.Record.Name))
				return nil
			}

			plan, err := s.PreviewBatchAdopt(paths)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), terminal.Safe(plan.String()))
			if dry {
				return nil
			}
			if !yes {
				if !isTerminal(cmd.InOrStdin()) {
					return fmt.Errorf("review with --dry-run, then pass --yes in noninteractive use")
				}
				fmt.Fprint(cmd.OutOrStdout(), "Apply these changes? [y/N] ")
				response, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if readErr != nil {
					return readErr
				}
				response = strings.ToLower(strings.TrimSpace(response))
				if response != "y" && response != "yes" {
					fmt.Fprintln(cmd.OutOrStdout(), "No changes made.")
					return nil
				}
			}
			if err = s.ApplyBatch(plan); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Done: added %d skills to the library.\n", len(plan.Plans))
			return nil
		}
		root.AddCommand(cmd)
	}
	for _, action := range []string{"resolve", "enable", "disable", "restore"} {
		var yes, dry bool
		cmd := &cobra.Command{Use: action + " <id>", Short: map[string]string{"resolve": "Keep one copy and back up the others", "enable": "Create shared discovery links for a managed skill", "disable": "Remove discovery links, keeping the library copy", "restore": "Return an adopted skill to its original location"}[action], Args: cobra.ExactArgs(1)}
		cmd.Hidden = action == "resolve"
		cmd.Flags().BoolVar(&dry, "dry-run", false, "Preview changes without writing files")
		if action == "resolve" || action == "restore" {
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
			if (action == "resolve" || action == "restore") && !yes {
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
	for _, action := range []string{"move", "copy"} {
		var yes, dry, toGlobal bool
		var toProject string
		cmd := &cobra.Command{
			Use:   action + " <id>",
			Short: map[string]string{"move": "Move a managed skill to another scope", "copy": "Copy a managed skill independently to another scope"}[action],
			Args:  cobra.ExactArgs(1),
		}
		cmd.Flags().BoolVar(&dry, "dry-run", false, "Preview changes without writing files")
		cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Apply the preview without prompting")
		cmd.Flags().BoolVar(&toGlobal, "to-global", false, "Use the global library as the destination")
		cmd.Flags().StringVar(&toProject, "to-project", "", "Use a project library as the destination; accepts 'auto'")
		cmd.MarkFlagsMutuallyExclusive("to-global", "to-project")
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			if toGlobal == (toProject != "") {
				return fmt.Errorf("set exactly one of --to-global or --to-project")
			}
			source, err := service()
			if err != nil {
				return err
			}
			destinationProject := toProject
			if toGlobal {
				destinationProject = ""
			}
			destination, err := manager.New(manager.Config{Home: source.Config.Home, DataHome: source.Config.DataHome, ConfigHome: source.Config.ConfigHome, Project: destinationProject})
			if err != nil {
				return err
			}
			plan, err := source.PreviewTransfer(action, args[0], destination)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), terminal.Safe(plan.String()))
			if dry {
				return nil
			}
			if !yes {
				if !isTerminal(cmd.InOrStdin()) {
					return fmt.Errorf("review with --dry-run, then pass --yes in noninteractive use")
				}
				fmt.Fprint(cmd.OutOrStdout(), "Apply these changes? [y/N] ")
				response, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if readErr != nil {
					return readErr
				}
				response = strings.ToLower(strings.TrimSpace(response))
				if response != "y" && response != "yes" {
					fmt.Fprintln(cmd.OutOrStdout(), "No changes made.")
					return nil
				}
			}
			if err = source.ApplyTransfer(destination, plan); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Done: %s %s to %s.\n", action, oneLine(plan.SourceRecord.Name), scopeName(destination))
			return nil
		}
		root.AddCommand(cmd)
	}
	packageService := func() (*manager.Service, error) {
		if global {
			return nil, fmt.Errorf("project packages cannot be managed with --global")
		}
		target := project
		if target == "" {
			target = "auto"
		}
		return manager.Environment(target)
	}
	for _, action := range []string{"add", "remove", "sync"} {
		var dry bool
		use := action + " <skill|@group>..."
		short := map[string]string{
			"add":    "Add central-library skills to the current project",
			"remove": "Remove direct skill or group requests from the current project",
			"sync":   "Reconcile current-project skills with its package manifest",
		}[action]
		args := cobra.MinimumNArgs(1)
		if action == "sync" {
			use = "sync"
			args = cobra.NoArgs
		}
		cmd := &cobra.Command{Use: use, Short: short, Args: args}
		if action == "add" {
			cmd.Aliases = []string{"install"}
		}
		cmd.Flags().BoolVar(&dry, "dry-run", false, "Preview changes without writing files")
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			s, err := packageService()
			if err != nil {
				return err
			}
			plan, err := s.PreviewPackages(action, args)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), terminal.Safe(plan.String()))
			if dry {
				return nil
			}
			if err = s.ApplyPackages(plan); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Done: project now has %d skills from %d requests.\n", len(plan.After.Skills), len(plan.After.Requests))
			return nil
		}
		root.AddCommand(cmd)
	}
	{
		group := &cobra.Command{Use: "group", Short: "Manage reusable central-library skill groups"}
		group.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
			if project != "" {
				return fmt.Errorf("groups are global definitions; omit --project")
			}
			return nil
		}
		var asJSON bool
		list := &cobra.Command{Use: "list", Short: "List installable groups", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			s, err := manager.Environment("")
			if err != nil {
				return err
			}
			groups, err := s.Groups()
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), groups)
			}
			for _, item := range groups {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", oneLine(item.Name), strings.Join(item.Members, ", "))
			}
			return nil
		}}
		list.Flags().BoolVar(&asJSON, "json", false, "Write structured JSON")
		group.AddCommand(list)
		group.AddCommand(&cobra.Command{Use: "create <name> <skill|@group>...", Short: "Create an installable skill group", Args: cobra.MinimumNArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			s, err := manager.Environment("")
			if err != nil {
				return err
			}
			created, err := s.CreateGroup(args[0], args[1:])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created group @%s with %d members.\n", oneLine(created.Name), len(created.Members))
			return nil
		}})
		group.AddCommand(&cobra.Command{Use: "delete <name>", Short: "Delete an installable skill group", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			s, err := manager.Environment("")
			if err != nil {
				return err
			}
			if err = s.DeleteGroup(strings.TrimPrefix(args[0], "@")); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted group @%s.\n", oneLine(strings.TrimPrefix(args[0], "@")))
			return nil
		}})
		root.AddCommand(group)
	}
	return root
}

func scopeName(s *manager.Service) string {
	if s.Config.Project == "" {
		return "global"
	}
	return s.Config.Project
}
func displayScope(s skills.Skill) string {
	if s.OwnerProject != "" {
		return "project:" + oneLine(s.OwnerProject)
	}
	return s.Scope
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
	state := "available"
	if s.Installed {
		state = "installed"
	} else if s.Managed {
		if s.Enabled {
			state = "enabled"
		} else {
			state = "disabled"
		}
	}
	if !s.Installed {
		if s.Inherited {
			state += " / inherited"
		} else if s.ReadOnly {
			state += " / view only"
		}
	}
	if s.ConflictKind != "" {
		state += " / " + skills.ConflictLabel(s.ConflictKind)
	}
	return state
}
