package cli

import (
	"slices"
	"strings"

	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/settings"
)

func registerConfig(parent *cobra.Command, globals *GlobalFlags) {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Persistent settings (cache TTLs). Precedence: flag > env > config > default",
	}
	parent.AddCommand(configCmd)
	handleUnknownSubcommand(configCmd)

	keyCompletion := func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return settings.KnownKeys(), cobra.ShellCompDirectiveNoFileComp
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "Show the persisted settings and the settable keys",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := settings.Load()
			if err != nil {
				return err
			}
			set := map[string]any{}
			for _, kv := range cfg.Sorted() {
				set[kv[0]] = kv[1]
			}
			path, _ := settings.Path()
			return printSingle(globals, map[string]any{
				"config_path": path,
				"settings":    set,
				"known_keys":  settings.KnownKeys(),
			})
		},
	}
	configCmd.AddCommand(listCmd)

	getCmd := &cobra.Command{
		Use:               "get <key> [key...]",
		Short:             "Show one or more settings; NDJSON by default (one {key,value} line per key)",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: keyCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := settings.Load()
			if err != nil {
				return err
			}
			known := settings.KnownKeys()
			return runEntityGet(globals, args, func(key string) (any, error) {
				if !slices.Contains(known, key) {
					return nil, agenterrors.Newf(agenterrors.FixableByAgent, "unknown config key %q", key).
						WithHint("valid keys: " + strings.Join(known, ", "))
				}
				return map[string]any{"key": key, "value": cfg.Get(key)}, nil
			})
		},
	}
	configCmd.AddCommand(getCmd)

	setCmd := &cobra.Command{
		Use:               "set <key> <value>",
		Short:             "Persist a setting (e.g. config set cache.ttl.channels 30m)",
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: keyCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := settings.Set(args[0], args[1]); err != nil {
				return agenterrors.Wrap(err, agenterrors.FixableByAgent)
			}
			return printSingle(globals, map[string]any{"set": args[0], "value": args[1]})
		},
	}
	configCmd.AddCommand(setCmd)

	unsetCmd := &cobra.Command{
		Use:               "unset <key>",
		Short:             "Remove a persisted setting (revert to env/default)",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: keyCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := settings.Unset(args[0]); err != nil {
				return err
			}
			return printSingle(globals, map[string]any{"unset": args[0]})
		},
	}
	configCmd.AddCommand(unsetCmd)
}
