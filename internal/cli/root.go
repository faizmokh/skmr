// Package cli exposes the manager through scriptable Cobra commands.
package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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
	var addCommand *cobra.Command
	service := func() (*manager.Service, error) { return manager.DefaultEnvironment(project, global) }
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		name := cmd.Name()
		if name == "skmr" || name == "version" || name == "tui" || name == "doctor" {
			return nil
		}
		// Explicit dry runs preserve their source files and state. Normal first
		// access adopts discovered skills; diagnostics can inspect old journals.
		if flag := cmd.Flags().Lookup("dry-run"); flag != nil && flag.Value.String() == "true" {
			return nil
		}
		target := project
		if name == "update" || name == "enable" || name == "disable" || name == "restore" || name == "delete" {
			target = ""
		}
		if (name == "remove" || name == "sync") && !global && target == "" {
			target = "auto"
		}
		s, err := manager.Environment(target)
		if name == "list" || name == "show" || name == "adopt" || name == "resolve" || name == "search" {
			s, err = service()
		}
		if err != nil {
			return err
		}
		if name == "adopt" {
			return s.Upgrade()
		}
		var excluded []string
		if name == "add" && len(args) == 1 && looksLikePath(args[0]) && !strings.Contains(args[0], "://") {
			excluded = args
		}
		if err = s.AutoAdoptExcept(excluded); err != nil {
			return err
		}
		if name == "list" || name == "show" {
			return nil
		}
		if conflicts, err := s.AutoAdoptConflicts(); err != nil {
			return err
		} else if conflicts != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "Writable copies need a choice before adoption: %s\n", terminal.Safe(conflicts))
		}
		return nil
	}
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
	{
		cmd := &cobra.Command{Use: "search [query]", Short: "Search skills.sh for skills to add", Args: cobra.ArbitraryArgs}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if !isTerminal(cmd.InOrStdin()) {
					return fmt.Errorf("provide a search query")
				}
				fmt.Fprint(cmd.OutOrStdout(), "Search skills.sh: ")
				line, e := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if e != nil {
					return e
				}
				args = []string{strings.TrimSpace(line)}
			}
			results, err := manager.SearchRemote(strings.Join(args, " "))
			if err != nil {
				return err
			}
			if len(results) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No skills found.")
				return nil
			}
			for i, result := range results {
				fmt.Fprintf(cmd.OutOrStdout(), "%d. %s\t%s\n", i+1, terminal.Safe(result.Name), terminal.Safe(result.URL))
			}
			if !isTerminal(cmd.InOrStdin()) {
				return nil
			}
			fmt.Fprint(cmd.OutOrStdout(), "Open result number to add (Enter to skip): ")
			line, e := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
			if e != nil {
				return e
			}
			line = strings.TrimSpace(line)
			if line == "" {
				return nil
			}
			n, e := strconv.Atoi(line)
			if e != nil || n < 1 || n > len(results) {
				return fmt.Errorf("invalid result number")
			}
			selection := []string{results[n-1].URL}
			if err := root.PersistentPreRunE(addCommand, selection); err != nil {
				return err
			}
			return addCommand.RunE(addCommand, selection)
		}
		root.AddCommand(cmd)
	}
	{
		var dry, yes, replace bool
		cmd := &cobra.Command{Use: "update <id>", Short: "Refresh an imported skill from its source", Args: cobra.ExactArgs(1)}
		cmd.Flags().BoolVar(&dry, "dry-run", false, "Preview without changing the library")
		cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Apply the preview without prompting")
		cmd.Flags().BoolVar(&replace, "replace", false, "Replace locally edited skill content")
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			if project != "" {
				return fmt.Errorf("updates use the personal library; omit --project")
			}
			s, err := manager.Environment("")
			if err != nil {
				return err
			}
			plan, err := s.PreviewOperation(manager.OperationRequest{Action: "update", Arguments: args, Replace: replace})
			if err != nil {
				return err
			}
			defer plan.Discard()
			fmt.Fprintln(cmd.OutOrStdout(), terminal.Safe(plan.String()))
			if dry || len(plan.Content) == 0 {
				return nil
			}
			if !yes {
				if !isTerminal(cmd.InOrStdin()) {
					return fmt.Errorf("review with --dry-run, then pass --yes in noninteractive use")
				}
				fmt.Fprint(cmd.OutOrStdout(), "Apply this update? [y/N] ")
				line, e := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if e != nil {
					return e
				}
				if response := strings.ToLower(strings.TrimSpace(line)); response != "y" && response != "yes" {
					fmt.Fprintln(cmd.OutOrStdout(), "No changes made.")
					return nil
				}
			}
			if err = s.ApplyOperation(plan); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Done: skill updated.")
			return nil
		}
		root.AddCommand(cmd)
	}
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
			if kind == "list" || kind == "show" {
				if err = s.AutoAdopt(); err != nil {
					return err
				}
			}
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
				if item.Remote {
					fmt.Fprintln(out, "Source:", terminal.Safe(item.SourceURL))
				}
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
			action := "adopt-batch"
			if len(paths) == 1 {
				action = "adopt"
			}
			plan, err := s.PreviewOperation(manager.OperationRequest{Action: action, Arguments: paths})
			if err != nil {
				return err
			}
			defer plan.Discard()
			fmt.Fprintln(cmd.OutOrStdout(), terminal.Safe(plan.String()))
			if dry {
				return nil
			}
			if err = confirmCLI(cmd, yes, "Apply these changes?"); err != nil {
				return err
			}
			if err = s.ApplyOperation(plan); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Done: added %d skills to the library.\n", len(paths))
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
			if project != "" && action != "resolve" {
				return fmt.Errorf("%s changes the personal library; use add or remove for project placements", action)
			}
			s, err := manager.Environment("")
			if action == "resolve" {
				s, err = service()
			}
			if err != nil {
				return err
			}
			p, err := s.PreviewOperation(manager.OperationRequest{Action: action, Arguments: args})
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
			if err = s.ApplyOperation(p); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Done: %s %s.\n", action, oneLine(args[0]))
			return nil
		}
		root.AddCommand(cmd)
	}
	{
		var dry, yes bool
		cmd := &cobra.Command{Use: "delete <name-or-id>", Short: "Permanently delete a library skill", Args: cobra.ExactArgs(1)}
		cmd.Flags().BoolVar(&dry, "dry-run", false, "Preview deletion without changing files")
		cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Delete after reviewing the preview without prompting")
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			if project != "" {
				return fmt.Errorf("delete changes the personal library; remove project placements first")
			}
			s, err := manager.Environment("")
			if err != nil {
				return err
			}
			plan, err := s.PreviewOperation(manager.OperationRequest{Action: "delete", Arguments: args})
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
				fmt.Fprint(cmd.OutOrStdout(), "Permanently delete this skill? [y/N] ")
				response, e := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if e != nil {
					return e
				}
				if response = strings.ToLower(strings.TrimSpace(response)); response != "y" && response != "yes" {
					fmt.Fprintln(cmd.OutOrStdout(), "No changes made.")
					return nil
				}
			}
			if err := s.ApplyOperation(plan); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Done: deleted %s.\n", oneLine(args[0]))
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
		var dry, yes bool
		var selected []string
		use := action + " <skill|@group>..."
		short := map[string]string{
			"add":    "Store skills in the personal library; use --global or --project to install",
			"remove": "Remove direct skill or group requests from the current project",
			"sync":   "Reconcile current-project skills with its package manifest",
		}[action]
		args := cobra.MinimumNArgs(1)
		if action == "sync" {
			use = "sync"
			args = cobra.NoArgs
		} else if action == "add" {
			use = "add <skill|@group>... | <url>"
			args = cobra.ArbitraryArgs
		}
		cmd := &cobra.Command{Use: use, Short: short, Args: args}
		if action == "add" {
			cmd.Aliases = []string{"install"}
			addCommand = cmd
			cmd.Flags().StringSliceVar(&selected, "skill", nil, "Select a remote skill by name (repeatable)")
			cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Apply a remote add without prompting")
		}
		cmd.Flags().BoolVar(&dry, "dry-run", false, "Preview changes without writing files")
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			if action == "add" && len(args) == 0 && isTerminal(cmd.InOrStdin()) {
				fmt.Fprint(cmd.OutOrStdout(), "GitHub or skills.sh URL: ")
				line, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if readErr != nil {
					return readErr
				}
				args = []string{strings.TrimSpace(line)}
			}
			if action == "add" && len(args) == 0 {
				return fmt.Errorf("provide a skill name, group, or GitHub/skills.sh URL")
			}
			if global || action == "add" && project == "" {
				return runPersonalPlacement(cmd, action, args, selected, dry, yes, action == "add" && !global)
			}
			s, err := packageService()
			if err != nil {
				return err
			}
			if action == "add" && len(args) == 1 && !strings.Contains(args[0], "://") && looksLikePath(args[0]) {
				plan, err := s.PreviewOperation(manager.OperationRequest{Action: "add", Arguments: args})
				if err != nil {
					return err
				}
				defer plan.Discard()
				fmt.Fprintln(cmd.OutOrStdout(), terminal.Safe(plan.String()))
				if dry {
					return nil
				}
				if err = confirmCLI(cmd, yes, "Add this skill to the project?"); err != nil {
					return err
				}
				return s.ApplyOperation(plan)
			}
			if action == "add" && len(args) == 1 && strings.Contains(args[0], "://") {
				globalService, e := manager.Environment("")
				if e != nil {
					return e
				}
				candidates, e := globalService.DiscoverRemote(args[0])
				if e != nil {
					return e
				}
				choices := append([]string{}, selected...)
				if len(choices) == 0 {
					if len(candidates) == 1 {
						choices = []string{candidates[0].Name}
					} else {
						if !isTerminal(cmd.InOrStdin()) {
							return fmt.Errorf("source contains %d skills; pass --skill <name> to select", len(candidates))
						}
						fmt.Fprintln(cmd.OutOrStdout(), "Available skills:")
						for i, candidate := range candidates {
							fmt.Fprintf(cmd.OutOrStdout(), "  %d. %s\n", i+1, terminal.Safe(candidate.Name))
						}
						fmt.Fprint(cmd.OutOrStdout(), "Select numbers (comma separated): ")
						line, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
						if readErr != nil {
							return readErr
						}
						for _, part := range strings.Split(strings.TrimSpace(line), ",") {
							n, parseErr := strconv.Atoi(strings.TrimSpace(part))
							if parseErr != nil || n < 1 || n > len(candidates) {
								return fmt.Errorf("invalid skill selection %q", part)
							}
							choices = append(choices, candidates[n-1].Name)
						}
					}
				}
				allowed := map[string]bool{}
				for _, candidate := range candidates {
					allowed[candidate.Name] = true
				}
				for _, choice := range choices {
					if !allowed[choice] {
						return fmt.Errorf("skill %q is not available from this source", choice)
					}
				}
				plan, e := s.PreviewOperation(manager.OperationRequest{Action: "add", Arguments: args, Skills: choices})
				if e != nil {
					return e
				}
				defer plan.Discard()
				fmt.Fprintln(cmd.OutOrStdout(), terminal.Safe(plan.String()))
				if dry {
					return nil
				}
				if !yes {
					if !isTerminal(cmd.InOrStdin()) {
						return fmt.Errorf("review with --dry-run, then pass --yes in noninteractive use")
					}
					fmt.Fprint(cmd.OutOrStdout(), "Add these skills to the project? [y/N] ")
					line, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
					if readErr != nil {
						return readErr
					}
					if response := strings.ToLower(strings.TrimSpace(line)); response != "y" && response != "yes" {
						fmt.Fprintln(cmd.OutOrStdout(), "No changes made.")
						return nil
					}
				}
				if e = s.ApplyOperation(plan); e != nil {
					return e
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Done: project now has %d skills from %d requests.\n", projectSkillCount(plan), projectRequestCount(plan))
				return nil
			}
			if action == "add" && (len(selected) > 0 || len(args) != 1 && containsURL(args)) {
				return fmt.Errorf("--skill requires exactly one remote URL")
			}
			plan, err := s.PreviewOperation(manager.OperationRequest{Action: action, Arguments: args})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), terminal.Safe(plan.String()))
			if dry {
				return nil
			}
			if err = s.ApplyOperation(plan); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Done: project now has %d skills from %d requests.\n", projectSkillCount(plan), projectRequestCount(plan))
			return nil
		}
		root.AddCommand(cmd)
	}
	{
		group := &cobra.Command{Use: "group", Short: "Manage reusable central-library skill groups"}
		var groupDry bool
		group.PersistentFlags().BoolVar(&groupDry, "dry-run", false, "Preview preset changes without writing files")
		group.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
			if project != "" {
				return fmt.Errorf("groups are global definitions; omit --project")
			}
			if groupDry || cmd.Name() == "delete" {
				return nil
			}
			s, err := manager.Environment("")
			if err != nil {
				return err
			}
			return s.AutoAdopt()
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
			plan, err := s.PreviewOperation(manager.OperationRequest{Action: "group-create", Arguments: args})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), terminal.Safe(plan.String()))
			if groupDry {
				return nil
			}
			return s.ApplyOperation(plan)
		}})
		group.AddCommand(&cobra.Command{Use: "delete <name>", Short: "Delete an installable skill group", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			s, err := manager.Environment("")
			if err != nil {
				return err
			}
			plan, err := s.PreviewOperation(manager.OperationRequest{Action: "group-delete", Arguments: []string{strings.TrimPrefix(args[0], "@")}})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), terminal.Safe(plan.String()))
			if groupDry {
				return nil
			}
			return s.ApplyOperation(plan)
		}})
		root.AddCommand(group)
	}
	return root
}

func runPersonalPlacement(cmd *cobra.Command, action string, args, selected []string, dry, yes, libraryOnly bool) error {
	if action == "sync" {
		return fmt.Errorf("sync requires a project")
	}
	s, err := manager.Environment("")
	if err != nil {
		return err
	}
	if len(args) == 1 && action == "add" && strings.Contains(args[0], "://") {
		candidates, err := s.DiscoverRemote(args[0])
		if err != nil {
			return err
		}
		choices := append([]string{}, selected...)
		if len(choices) == 0 {
			if len(candidates) == 1 {
				choices = []string{candidates[0].Name}
			} else {
				if !isTerminal(cmd.InOrStdin()) {
					return fmt.Errorf("source contains %d skills; pass --skill <name> to select", len(candidates))
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Available skills:")
				for i, candidate := range candidates {
					fmt.Fprintf(cmd.OutOrStdout(), "  %d. %s\n", i+1, terminal.Safe(candidate.Name))
				}
				fmt.Fprint(cmd.OutOrStdout(), "Select numbers (comma separated): ")
				line, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if readErr != nil {
					return readErr
				}
				for _, part := range strings.Split(strings.TrimSpace(line), ",") {
					n, parseErr := strconv.Atoi(strings.TrimSpace(part))
					if parseErr != nil || n < 1 || n > len(candidates) {
						return fmt.Errorf("invalid skill selection %q", part)
					}
					choices = append(choices, candidates[n-1].Name)
				}
			}
		}
		plan, err := s.PreviewOperation(manager.OperationRequest{Action: "add", Arguments: args, Skills: choices, LibraryOnly: libraryOnly})
		if err != nil {
			return err
		}
		defer plan.Discard()
		fmt.Fprintln(cmd.OutOrStdout(), terminal.Safe(plan.String()))
		if dry {
			return nil
		}
		prompt := "Add and enable these skills globally?"
		if libraryOnly {
			prompt = "Add these skills to the personal library?"
		}
		if err = confirmCLI(cmd, yes, prompt); err != nil {
			return err
		}
		if err = s.ApplyOperation(plan); err != nil {
			return err
		}

		if libraryOnly {
			fmt.Fprintln(cmd.OutOrStdout(), "Done: skills are in the personal library.")
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "Done: skills enabled globally.")
		}
		return nil
	}
	if len(selected) > 0 {
		return fmt.Errorf("--skill requires one remote URL")
	}
	if len(args) == 1 && action == "add" && looksLikePath(args[0]) {
		plan, err := s.PreviewOperation(manager.OperationRequest{Action: "add", Arguments: args, LibraryOnly: libraryOnly})
		if err != nil {
			return err
		}
		defer plan.Discard()
		fmt.Fprintln(cmd.OutOrStdout(), terminal.Safe(plan.String()))
		if dry {
			return nil
		}
		prompt := "Add and enable this skill globally?"
		if libraryOnly {
			prompt = "Add this skill to the personal library?"
		}
		if err = confirmCLI(cmd, yes, prompt); err != nil {
			return err
		}
		if err := s.ApplyOperation(plan); err != nil {
			return err
		}
		if action == "add" {
			if libraryOnly {
				fmt.Fprintln(cmd.OutOrStdout(), "Done: skills are in the personal library.")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Done: skills enabled globally.")
			}
		}
		return nil
	}
	if action == "remove" && len(args) == 0 {
		return fmt.Errorf("provide at least one skill")
	}
	plan, err := s.PreviewOperation(manager.OperationRequest{Action: action, Arguments: args, LibraryOnly: libraryOnly})
	if err != nil {
		return err
	}
	defer plan.Discard()
	fmt.Fprintln(cmd.OutOrStdout(), terminal.Safe(plan.String()))
	if dry {
		return nil
	}
	if err := s.ApplyOperation(plan); err != nil {
		return err
	}
	if action == "add" {
		if libraryOnly {
			fmt.Fprintln(cmd.OutOrStdout(), "Done: skills are in the personal library.")
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "Done: skills enabled globally.")
		}
	}
	return nil
}

func looksLikePath(value string) bool {
	if filepath.IsAbs(value) || strings.HasPrefix(value, ".") || strings.ContainsRune(value, filepath.Separator) {
		return true
	}
	info, err := os.Stat(value)
	return err == nil && info.IsDir()
}

func projectSkillCount(p manager.OperationPlan) int {
	for _, scope := range p.Scopes {
		if scope.RequestsAfter != nil {
			return len(scope.RequestsAfter.Skills)
		}
	}
	return 0
}
func projectRequestCount(p manager.OperationPlan) int {
	for _, scope := range p.Scopes {
		if scope.RequestsAfter != nil {
			return len(scope.RequestsAfter.Requests)
		}
	}
	return 0
}

func confirmCLI(cmd *cobra.Command, yes bool, prompt string) error {
	if yes {
		return nil
	}
	if !isTerminal(cmd.InOrStdin()) {
		return fmt.Errorf("review with --dry-run, then pass --yes in noninteractive use")
	}
	fmt.Fprint(cmd.OutOrStdout(), prompt+" [y/N] ")
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil {
		return err
	}
	if response := strings.ToLower(strings.TrimSpace(line)); response != "y" && response != "yes" {
		return fmt.Errorf("no changes made")
	}
	return nil
}

func containsURL(args []string) bool {
	for _, arg := range args {
		if strings.Contains(arg, "://") {
			return true
		}
	}
	return false
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
