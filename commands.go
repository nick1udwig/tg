package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

type commandEnv struct {
	stdout        io.Writer
	stderr        io.Writer
	rawConfigPath string
	rootToken     string
	rootBot       string
}

type tokenSelection struct {
	explicitToken string
	tokenChanged  bool
	explicitAlias string
	aliasChanged  bool
}

type botListEntry struct {
	Alias         string `json:"alias"`
	Default       bool   `json:"default"`
	DefaultChatID string `json:"default_chat_id,omitempty"`
	Description   string `json:"description,omitempty"`
	Token         string `json:"token,omitempty"`
}

func newRootCommand(stdout, stderr io.Writer) *cobra.Command {
	env := &commandEnv{
		stdout: stdout,
		stderr: stderr,
	}

	root := &cobra.Command{
		Use:           "tg",
		Short:         "Telegram bot CLI for agents",
		SilenceErrors: true,
		SilenceUsage:  true,
		Example: strings.TrimSpace(`
tg config init
tg bot add A --token 123456:ABC... --chat-id 123456789 --description "primary bot" --default
tg bot add B --token 654321:XYZ... --chat-id @alerts --description "backup bot"
tg send "hello from tg"
tg --bot B watch --timeout 30 --json
`),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	root.SetOut(stdout)
	root.SetErr(stderr)
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().StringVar(&env.rawConfigPath, "config", "", "path to config file (default: ~/.tg/config.toml)")
	root.PersistentFlags().StringVar(&env.rootToken, "token", "", "bot token override for the command")
	root.PersistentFlags().StringVar(&env.rootBot, "bot", "", "bot alias override for the command")

	root.AddCommand(
		newSendCommand(env),
		newPollCommand(env),
		newWatchCommand(env),
		newWebhookCommand(env),
		newConfigCommand(env),
		newBotCommand(env),
	)

	return root
}

func (e *commandEnv) loadRuntimeConfig() (Config, string, error) {
	cfg, cfgPath, err := LoadConfig(e.rawConfigPath)
	if err != nil {
		return Config{}, "", err
	}
	cfg, err = applyEnvOverrides(cfg)
	if err != nil {
		return Config{}, "", err
	}
	return cfg, cfgPath, nil
}

func (e *commandEnv) loadFileConfig() (Config, string, error) {
	return LoadConfig(e.rawConfigPath)
}

func (e *commandEnv) selection(cmd *cobra.Command) tokenSelection {
	return tokenSelection{
		explicitToken: e.rootToken,
		tokenChanged:  cmd.Flags().Changed("token"),
		explicitAlias: e.rootBot,
		aliasChanged:  cmd.Flags().Changed("bot"),
	}
}

func newSendCommand(env *commandEnv) *cobra.Command {
	var opts struct {
		chatID              string
		parseMode           string
		apiBaseURL          string
		threadID            int64
		dmTopicID           int64
		replyTo             int64
		httpTimeout         int
		disableNotification bool
		protectContent      bool
		allowPaidBroadcast  bool
		useStdin            bool
		jsonOut             bool
	}

	cmd := &cobra.Command{
		Use:   "send [flags] [text]",
		Short: "Send a text message with sendMessage",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := env.loadRuntimeConfig()
			if err != nil {
				return err
			}

			bot, err := resolveBot(cfg, env.selection(cmd))
			if err != nil {
				return err
			}

			chatID, err := resolveSendChatID(opts.chatID, cmd.Flags().Changed("chat-id"), bot)
			if err != nil {
				return err
			}

			httpTimeout := resolvedInt(cmd.Flags().Changed("http-timeout"), opts.httpTimeout, cfg.HTTPTimeoutSeconds)
			if httpTimeout <= 0 {
				return fmt.Errorf("--http-timeout must be a positive integer")
			}

			apiBaseURL := resolvedString(cmd.Flags().Changed("api-base-url"), opts.apiBaseURL, cfg.APIBaseURL)
			jsonOut := resolvedBool(cmd.Flags().Changed("json"), opts.jsonOut, strings.EqualFold(cfg.Output, "json"))

			text := strings.TrimSpace(strings.Join(args, " "))
			if text == "" {
				stdinText, err := readMessageFromStdin(opts.useStdin)
				if err != nil {
					return err
				}
				text = strings.TrimSpace(stdinText)
			}
			if text == "" {
				return fmt.Errorf("message text is required as argument or stdin")
			}

			client, err := NewClient(bot.Token, apiBaseURL, time.Duration(httpTimeout)*time.Second)
			if err != nil {
				return err
			}

			msg, err := client.SendMessage(cmd.Context(), SendMessageRequest{
				ChatID:              parseChatID(chatID),
				Text:                text,
				ParseMode:           strings.TrimSpace(opts.parseMode),
				MessageThreadID:     opts.threadID,
				DirectMessagesTopic: opts.dmTopicID,
				DisableNotification: opts.disableNotification,
				ProtectContent:      opts.protectContent,
				AllowPaidBroadcast:  opts.allowPaidBroadcast,
				ReplyToMessageID:    opts.replyTo,
			})
			if err != nil {
				return err
			}

			if jsonOut {
				return writePrettyJSON(env.stdout, msg)
			}
			_, _ = fmt.Fprintf(env.stdout, "sent message_id=%d chat_id=%d\n", msg.MessageID, msg.Chat.ID)
			return nil
		},
	}

	cmd.Flags().StringVar(&opts.chatID, "chat-id", "", "chat id or @channel username")
	cmd.Flags().StringVar(&opts.parseMode, "parse-mode", "", "parse mode (MarkdownV2, HTML, Markdown)")
	cmd.Flags().Int64Var(&opts.threadID, "thread-id", 0, "message thread id for forum topics")
	cmd.Flags().Int64Var(&opts.dmTopicID, "dm-topic-id", 0, "direct messages topic id")
	cmd.Flags().Int64Var(&opts.replyTo, "reply-to", 0, "reply to message id")
	cmd.Flags().BoolVar(&opts.disableNotification, "disable-notification", false, "send silently")
	cmd.Flags().BoolVar(&opts.protectContent, "protect-content", false, "protect forwarded/saved content")
	cmd.Flags().BoolVar(&opts.allowPaidBroadcast, "allow-paid-broadcast", false, "allow paid broadcast throughput")
	cmd.Flags().BoolVar(&opts.useStdin, "stdin", false, "read message text from stdin")
	cmd.Flags().BoolVar(&opts.jsonOut, "json", false, "print API result as JSON")
	cmd.Flags().StringVar(&opts.apiBaseURL, "api-base-url", "", "Bot API base URL")
	cmd.Flags().IntVar(&opts.httpTimeout, "http-timeout", 0, "HTTP timeout seconds")

	return cmd
}

func newPollCommand(env *commandEnv) *cobra.Command {
	var opts struct {
		offsetRaw     string
		allowedRaw    string
		stateFileRaw  string
		apiBaseURL    string
		limit         int
		timeout       int
		httpTimeout   int
		deleteWebhook bool
		dropPending   bool
		allUpdates    bool
		jsonOut       bool
		saveOffset    bool
	}

	cmd := &cobra.Command{
		Use:   "poll",
		Short: "Run one getUpdates request",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := env.loadRuntimeConfig()
			if err != nil {
				return err
			}

			token, err := resolveBotToken(cfg, env.selection(cmd))
			if err != nil {
				return err
			}

			httpTimeout := resolvedInt(cmd.Flags().Changed("http-timeout"), opts.httpTimeout, cfg.HTTPTimeoutSeconds)
			if httpTimeout <= 0 {
				return fmt.Errorf("--http-timeout must be a positive integer")
			}

			limit := resolvedInt(cmd.Flags().Changed("limit"), opts.limit, cfg.PollLimit)
			if limit < 1 || limit > 100 {
				return fmt.Errorf("--limit must be between 1 and 100")
			}

			timeout := resolvedInt(cmd.Flags().Changed("timeout"), opts.timeout, cfg.PollTimeoutSeconds)
			if timeout < 0 {
				return fmt.Errorf("--timeout must be >= 0")
			}

			deleteWebhook := resolvedBool(cmd.Flags().Changed("delete-webhook"), opts.deleteWebhook, cfg.AutoDeleteWebhook)
			jsonOut := resolvedBool(cmd.Flags().Changed("json"), opts.jsonOut, strings.EqualFold(cfg.Output, "json"))
			apiBaseURL := resolvedString(cmd.Flags().Changed("api-base-url"), opts.apiBaseURL, cfg.APIBaseURL)

			client, err := NewClient(token, apiBaseURL, time.Duration(httpTimeout)*time.Second)
			if err != nil {
				return err
			}

			if deleteWebhook {
				if _, err := client.DeleteWebhook(cmd.Context(), opts.dropPending); err != nil {
					return err
				}
			}

			offset, hasOffset, err := parseOptionalInt64(opts.offsetRaw)
			if err != nil {
				return fmt.Errorf("--offset: %w", err)
			}

			req := GetUpdatesRequest{
				Limit:          limit,
				TimeoutSeconds: timeout,
			}
			if hasOffset {
				req.Offset = &offset
			}
			if opts.allUpdates {
				req.HasAllowedUpdates = true
				req.AllowedUpdates = []string{}
			} else {
				updates := parseAllowedUpdates(opts.allowedRaw)
				if len(updates) > 0 {
					req.HasAllowedUpdates = true
					req.AllowedUpdates = updates
				}
			}

			updates, err := client.GetUpdates(cmd.Context(), req)
			if err != nil {
				return err
			}

			if opts.saveOffset && len(updates) > 0 {
				next := nextUpdateOffset(updates, offset)
				statePath, err := resolveStatePath(cfg, opts.stateFileRaw)
				if err != nil {
					return err
				}
				state, err := LoadState(statePath)
				if err != nil {
					return err
				}
				state.Offsets[tokenKey(token)] = next
				if err := SaveState(statePath, state); err != nil {
					return err
				}
			}

			if jsonOut {
				return writePrettyJSON(env.stdout, updates)
			}
			if len(updates) == 0 {
				_, _ = fmt.Fprintln(env.stdout, "no updates")
				return nil
			}
			for _, update := range updates {
				_, _ = fmt.Fprintln(env.stdout, summarizeUpdate(update))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&opts.offsetRaw, "offset", "", "first update id to return (can be negative)")
	cmd.Flags().IntVar(&opts.limit, "limit", 0, "max updates to retrieve (1-100)")
	cmd.Flags().IntVar(&opts.timeout, "timeout", 0, "long-poll timeout seconds")
	cmd.Flags().StringVar(&opts.allowedRaw, "allowed-updates", "", "comma-separated update types")
	cmd.Flags().BoolVar(&opts.allUpdates, "all-updates", false, "explicitly request all update types (sets empty allowed_updates list)")
	cmd.Flags().BoolVar(&opts.deleteWebhook, "delete-webhook", false, "delete webhook before polling")
	cmd.Flags().BoolVar(&opts.dropPending, "drop-pending-updates", false, "drop pending updates when deleting webhook")
	cmd.Flags().BoolVar(&opts.jsonOut, "json", false, "print result as JSON")
	cmd.Flags().BoolVar(&opts.saveOffset, "save-offset", false, "save next offset in state file")
	cmd.Flags().StringVar(&opts.stateFileRaw, "state-file", "", "state file path (default ~/.tg/state.json)")
	cmd.Flags().StringVar(&opts.apiBaseURL, "api-base-url", "", "Bot API base URL")
	cmd.Flags().IntVar(&opts.httpTimeout, "http-timeout", 0, "HTTP timeout seconds")

	return cmd
}

func newWatchCommand(env *commandEnv) *cobra.Command {
	var opts struct {
		offsetRaw     string
		allowedRaw    string
		stateFileRaw  string
		apiBaseURL    string
		limit         int
		timeout       int
		httpTimeout   int
		deleteWebhook bool
		dropPending   bool
		allUpdates    bool
		jsonOut       bool
		useState      bool
		backoff       time.Duration
	}

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Long-poll continuously using getUpdates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := env.loadRuntimeConfig()
			if err != nil {
				return err
			}

			token, err := resolveBotToken(cfg, env.selection(cmd))
			if err != nil {
				return err
			}

			httpTimeout := resolvedInt(cmd.Flags().Changed("http-timeout"), opts.httpTimeout, cfg.HTTPTimeoutSeconds)
			if httpTimeout <= 0 {
				return fmt.Errorf("--http-timeout must be a positive integer")
			}

			limit := resolvedInt(cmd.Flags().Changed("limit"), opts.limit, cfg.PollLimit)
			if limit < 1 || limit > 100 {
				return fmt.Errorf("--limit must be between 1 and 100")
			}

			timeout := resolvedInt(cmd.Flags().Changed("timeout"), opts.timeout, cfg.PollTimeoutSeconds)
			if timeout < 0 {
				return fmt.Errorf("--timeout must be >= 0")
			}
			if opts.backoff < 0 {
				return fmt.Errorf("--backoff must be >= 0")
			}

			deleteWebhook := resolvedBool(cmd.Flags().Changed("delete-webhook"), opts.deleteWebhook, cfg.AutoDeleteWebhook)
			jsonOut := resolvedBool(cmd.Flags().Changed("json"), opts.jsonOut, strings.EqualFold(cfg.Output, "json"))
			apiBaseURL := resolvedString(cmd.Flags().Changed("api-base-url"), opts.apiBaseURL, cfg.APIBaseURL)

			client, err := NewClient(token, apiBaseURL, time.Duration(httpTimeout)*time.Second)
			if err != nil {
				return err
			}

			var statePath string
			var state State
			if opts.useState {
				statePath, err = resolveStatePath(cfg, opts.stateFileRaw)
				if err != nil {
					return err
				}
				state, err = LoadState(statePath)
				if err != nil {
					return err
				}
			}

			offset, hasOffset, err := parseOptionalInt64(opts.offsetRaw)
			if err != nil {
				return fmt.Errorf("--offset: %w", err)
			}
			if !hasOffset && opts.useState {
				if saved, ok := state.Offsets[tokenKey(token)]; ok {
					offset = saved
					hasOffset = true
				}
			}

			if deleteWebhook {
				if _, err := client.DeleteWebhook(cmd.Context(), opts.dropPending); err != nil {
					return err
				}
			}

			var allowedUpdates []string
			var hasAllowedUpdates bool
			if opts.allUpdates {
				hasAllowedUpdates = true
				allowedUpdates = []string{}
			} else {
				allowedUpdates = parseAllowedUpdates(opts.allowedRaw)
				if len(allowedUpdates) > 0 {
					hasAllowedUpdates = true
				}
			}

			for {
				select {
				case <-cmd.Context().Done():
					return nil
				default:
				}

				req := GetUpdatesRequest{
					Limit:             limit,
					TimeoutSeconds:    timeout,
					AllowedUpdates:    allowedUpdates,
					HasAllowedUpdates: hasAllowedUpdates,
				}
				if hasOffset {
					req.Offset = &offset
				}

				updates, err := client.GetUpdates(cmd.Context(), req)
				if err != nil {
					if cmd.Context().Err() != nil {
						return nil
					}
					var apiErr *BotAPIError
					if errors.As(err, &apiErr) && apiErr.ErrorCode == 409 {
						_, _ = fmt.Fprintln(env.stderr, "hint: webhook is configured; run with --delete-webhook or `tg webhook clear`")
					}
					_, _ = fmt.Fprintf(env.stderr, "watch poll error: %v\n", err)
					if opts.backoff == 0 {
						continue
					}
					select {
					case <-cmd.Context().Done():
						return nil
					case <-time.After(opts.backoff):
						continue
					}
				}

				if len(updates) == 0 {
					continue
				}

				for _, update := range updates {
					if jsonOut {
						if err := writeJSONLine(env.stdout, update); err != nil {
							return err
						}
						continue
					}
					_, _ = fmt.Fprintln(env.stdout, summarizeUpdate(update))
				}

				offset = nextUpdateOffset(updates, offset)
				hasOffset = true

				if opts.useState {
					state.Offsets[tokenKey(token)] = offset
					if err := SaveState(statePath, state); err != nil {
						return err
					}
				}
			}
		},
	}

	cmd.Flags().StringVar(&opts.offsetRaw, "offset", "", "starting update offset (can be negative)")
	cmd.Flags().IntVar(&opts.limit, "limit", 0, "max updates per getUpdates call (1-100)")
	cmd.Flags().IntVar(&opts.timeout, "timeout", 0, "long-poll timeout seconds")
	cmd.Flags().StringVar(&opts.allowedRaw, "allowed-updates", "", "comma-separated update types")
	cmd.Flags().BoolVar(&opts.allUpdates, "all-updates", false, "explicitly request all update types (sets empty allowed_updates list)")
	cmd.Flags().BoolVar(&opts.deleteWebhook, "delete-webhook", false, "delete webhook before polling")
	cmd.Flags().BoolVar(&opts.dropPending, "drop-pending-updates", false, "drop pending updates when deleting webhook")
	cmd.Flags().BoolVar(&opts.jsonOut, "json", false, "emit newline-delimited JSON updates")
	cmd.Flags().BoolVar(&opts.useState, "state", true, "persist offsets in a local state file")
	cmd.Flags().StringVar(&opts.stateFileRaw, "state-file", "", "state file path (default ~/.tg/state.json)")
	cmd.Flags().DurationVar(&opts.backoff, "backoff", 2*time.Second, "delay after polling errors")
	cmd.Flags().StringVar(&opts.apiBaseURL, "api-base-url", "", "Bot API base URL")
	cmd.Flags().IntVar(&opts.httpTimeout, "http-timeout", 0, "HTTP timeout seconds")

	return cmd
}

func newWebhookCommand(env *commandEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhook",
		Short: "Manage webhook state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	var clearOpts struct {
		apiBaseURL  string
		httpTimeout int
		dropPending bool
	}
	clearCmd := &cobra.Command{
		Use:   "clear",
		Short: "Delete the configured webhook",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := env.loadRuntimeConfig()
			if err != nil {
				return err
			}

			token, err := resolveBotToken(cfg, env.selection(cmd))
			if err != nil {
				return err
			}

			httpTimeout := resolvedInt(cmd.Flags().Changed("http-timeout"), clearOpts.httpTimeout, cfg.HTTPTimeoutSeconds)
			if httpTimeout <= 0 {
				return fmt.Errorf("--http-timeout must be a positive integer")
			}
			apiBaseURL := resolvedString(cmd.Flags().Changed("api-base-url"), clearOpts.apiBaseURL, cfg.APIBaseURL)

			client, err := NewClient(token, apiBaseURL, time.Duration(httpTimeout)*time.Second)
			if err != nil {
				return err
			}

			ok, err := client.DeleteWebhook(cmd.Context(), clearOpts.dropPending)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(env.stdout, "webhook deleted=%t\n", ok)
			return nil
		},
	}
	clearCmd.Flags().BoolVar(&clearOpts.dropPending, "drop-pending-updates", false, "drop all pending updates")
	clearCmd.Flags().StringVar(&clearOpts.apiBaseURL, "api-base-url", "", "Bot API base URL")
	clearCmd.Flags().IntVar(&clearOpts.httpTimeout, "http-timeout", 0, "HTTP timeout seconds")

	var infoOpts struct {
		apiBaseURL  string
		httpTimeout int
		jsonOut     bool
	}
	infoCmd := &cobra.Command{
		Use:   "info",
		Short: "Show current webhook information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := env.loadRuntimeConfig()
			if err != nil {
				return err
			}

			token, err := resolveBotToken(cfg, env.selection(cmd))
			if err != nil {
				return err
			}

			httpTimeout := resolvedInt(cmd.Flags().Changed("http-timeout"), infoOpts.httpTimeout, cfg.HTTPTimeoutSeconds)
			if httpTimeout <= 0 {
				return fmt.Errorf("--http-timeout must be a positive integer")
			}
			apiBaseURL := resolvedString(cmd.Flags().Changed("api-base-url"), infoOpts.apiBaseURL, cfg.APIBaseURL)

			client, err := NewClient(token, apiBaseURL, time.Duration(httpTimeout)*time.Second)
			if err != nil {
				return err
			}

			info, err := client.GetWebhookInfo(cmd.Context())
			if err != nil {
				return err
			}
			if infoOpts.jsonOut {
				return writePrettyJSON(env.stdout, info)
			}
			_, _ = fmt.Fprintf(env.stdout, "url=%q pending_updates=%d\n", info.URL, info.PendingUpdateCount)
			return nil
		},
	}
	infoCmd.Flags().StringVar(&infoOpts.apiBaseURL, "api-base-url", "", "Bot API base URL")
	infoCmd.Flags().IntVar(&infoOpts.httpTimeout, "http-timeout", 0, "HTTP timeout seconds")
	infoCmd.Flags().BoolVar(&infoOpts.jsonOut, "json", true, "print result as JSON")

	cmd.AddCommand(clearCmd, infoCmd)
	return cmd
}

func newConfigCommand(env *commandEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or write config",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, cfgPath, err := env.loadFileConfig()
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(env.stdout, "config: %s\n", cfgPath)
			return writePrettyConfig(env.stdout, RedactedConfig(cfg))
		},
	}

	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Show the redacted config file",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, cfgPath, err := env.loadFileConfig()
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(env.stdout, "config: %s\n", cfgPath)
			return writePrettyConfig(env.stdout, RedactedConfig(cfg))
		},
	}

	pathCmd := &cobra.Command{
		Use:   "path",
		Short: "Print the resolved config path",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			_, cfgPath, err := env.loadFileConfig()
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(env.stdout, cfgPath)
			return nil
		},
	}

	var initOpts struct {
		apiBaseURL        string
		output            string
		stateFile         string
		httpTimeout       int
		pollTimeout       int
		pollLimit         int
		autoDeleteWebhook bool
		force             bool
	}
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Write a config file template",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, cfgPath, err := env.loadFileConfig()
			if err != nil {
				return err
			}

			if _, err := os.Stat(cfgPath); err == nil && !initOpts.force {
				return fmt.Errorf("config already exists at %s (use --force to overwrite)", cfgPath)
			}

			newCfg := defaultConfig()
			if cmd.Flags().Changed("api-base-url") {
				newCfg.APIBaseURL = strings.TrimSpace(initOpts.apiBaseURL)
			}
			if cmd.Flags().Changed("http-timeout") {
				newCfg.HTTPTimeoutSeconds = initOpts.httpTimeout
			}
			if cmd.Flags().Changed("poll-timeout") {
				newCfg.PollTimeoutSeconds = initOpts.pollTimeout
			}
			if cmd.Flags().Changed("poll-limit") {
				newCfg.PollLimit = initOpts.pollLimit
			}
			if cmd.Flags().Changed("output") {
				newCfg.Output = strings.TrimSpace(initOpts.output)
			}
			if cmd.Flags().Changed("state-file") {
				newCfg.StateFile = strings.TrimSpace(initOpts.stateFile)
			}
			if cmd.Flags().Changed("auto-delete-webhook") {
				newCfg.AutoDeleteWebhook = initOpts.autoDeleteWebhook
			}

			if newCfg.PollLimit < 1 || newCfg.PollLimit > 100 {
				return fmt.Errorf("--poll-limit must be between 1 and 100")
			}
			if newCfg.HTTPTimeoutSeconds <= 0 {
				return fmt.Errorf("--http-timeout must be a positive integer")
			}
			if newCfg.PollTimeoutSeconds < 0 {
				return fmt.Errorf("--poll-timeout must be >= 0")
			}
			if err := SaveConfig(cfgPath, newCfg); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(env.stdout, "wrote config %s\n", cfgPath)
			return nil
		},
	}
	initCmd.Flags().StringVar(&initOpts.apiBaseURL, "api-base-url", "", "Bot API base URL")
	initCmd.Flags().IntVar(&initOpts.httpTimeout, "http-timeout", 0, "HTTP timeout seconds")
	initCmd.Flags().IntVar(&initOpts.pollTimeout, "poll-timeout", 0, "poll timeout seconds")
	initCmd.Flags().IntVar(&initOpts.pollLimit, "poll-limit", 0, "poll limit (1-100)")
	initCmd.Flags().StringVar(&initOpts.output, "output", "", "default output mode: summary|json")
	initCmd.Flags().StringVar(&initOpts.stateFile, "state-file", "", "state file path")
	initCmd.Flags().BoolVar(&initOpts.autoDeleteWebhook, "auto-delete-webhook", false, "delete webhook before poll/watch")
	initCmd.Flags().BoolVar(&initOpts.force, "force", false, "overwrite existing config file")

	cmd.AddCommand(showCmd, pathCmd, initCmd)
	return cmd
}

func newBotCommand(env *commandEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bot",
		Short: "Manage named bot aliases",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	var addOpts struct {
		token       string
		chatID      string
		description string
		force       bool
		makeDefault bool
	}
	addCmd := &cobra.Command{
		Use:   "add <alias>",
		Short: "Add or update a named bot alias",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, cfgPath, err := env.loadFileConfig()
			if err != nil {
				return err
			}

			alias := strings.TrimSpace(args[0])
			if err := validateBotAlias(alias); err != nil {
				return err
			}
			if strings.TrimSpace(addOpts.token) == "" {
				return fmt.Errorf("--token is required")
			}

			if cfg.Bots == nil {
				cfg.Bots = map[string]BotConfig{}
			}
			if _, exists := cfg.Bots[alias]; exists && !addOpts.force {
				return fmt.Errorf("bot alias %q already exists (use --force to overwrite)", alias)
			}

			cfg.Bots[alias] = BotConfig{
				Token:         strings.TrimSpace(addOpts.token),
				DefaultChatID: strings.TrimSpace(addOpts.chatID),
				Description:   strings.TrimSpace(addOpts.description),
			}
			if addOpts.makeDefault {
				cfg.DefaultBot = alias
			}

			if err := SaveConfig(cfgPath, cfg); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(env.stdout, "saved bot alias %q in %s\n", alias, cfgPath)
			return nil
		},
	}
	addCmd.Flags().StringVar(&addOpts.token, "token", "", "bot token")
	addCmd.Flags().StringVar(&addOpts.chatID, "chat-id", "", "default chat id for send")
	addCmd.Flags().StringVar(&addOpts.description, "description", "", "optional alias description")
	addCmd.Flags().BoolVar(&addOpts.force, "force", false, "overwrite an existing alias")
	addCmd.Flags().BoolVar(&addOpts.makeDefault, "default", false, "set this alias as the default bot")

	var listOpts struct {
		jsonOut bool
	}
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List configured bot aliases",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, _, err := env.loadFileConfig()
			if err != nil {
				return err
			}
			if len(cfg.Bots) == 0 {
				if listOpts.jsonOut {
					return writePrettyJSON(env.stdout, []botListEntry{})
				}
				_, _ = fmt.Fprintln(env.stdout, "no bot aliases configured")
				return nil
			}

			entries := listBotEntries(cfg)
			if listOpts.jsonOut {
				return writePrettyJSON(env.stdout, entries)
			}

			tw := tabwriter.NewWriter(env.stdout, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "ALIAS\tDEFAULT\tCHAT\tDESCRIPTION\tTOKEN")
			for _, entry := range entries {
				defaultMark := ""
				if entry.Default {
					defaultMark = "*"
				}
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", entry.Alias, defaultMark, entry.DefaultChatID, entry.Description, entry.Token)
			}
			return tw.Flush()
		},
	}
	listCmd.Flags().BoolVar(&listOpts.jsonOut, "json", false, "print aliases as JSON")

	removeCmd := &cobra.Command{
		Use:     "rm <alias>",
		Aliases: []string{"remove"},
		Short:   "Remove a named bot alias",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, cfgPath, err := env.loadFileConfig()
			if err != nil {
				return err
			}

			alias := strings.TrimSpace(args[0])
			if _, ok := cfg.Bots[alias]; !ok {
				return fmt.Errorf("unknown bot alias %q", alias)
			}

			delete(cfg.Bots, alias)
			if len(cfg.Bots) == 0 {
				cfg.Bots = nil
			}
			if cfg.DefaultBot == alias {
				cfg.DefaultBot = ""
			}

			if err := SaveConfig(cfgPath, cfg); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(env.stdout, "removed bot alias %q from %s\n", alias, cfgPath)
			return nil
		},
	}

	defaultCmd := &cobra.Command{
		Use:   "default <alias>",
		Short: "Set the default bot alias",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, cfgPath, err := env.loadFileConfig()
			if err != nil {
				return err
			}

			alias := strings.TrimSpace(args[0])
			if _, ok := cfg.Bots[alias]; !ok {
				return fmt.Errorf("unknown bot alias %q", alias)
			}

			cfg.DefaultBot = alias
			if err := SaveConfig(cfgPath, cfg); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(env.stdout, "default bot set to %q\n", alias)
			return nil
		},
	}

	cmd.AddCommand(addCmd, listCmd, removeCmd, defaultCmd)
	return cmd
}

type resolvedBot struct {
	Token string
	Alias string
	Bot   *BotConfig
}

func resolveBotToken(cfg Config, selection tokenSelection) (string, error) {
	bot, err := resolveBot(cfg, selection)
	if err != nil {
		return "", err
	}
	return bot.Token, nil
}

func resolveBot(cfg Config, selection tokenSelection) (resolvedBot, error) {
	if selection.tokenChanged {
		token := strings.TrimSpace(selection.explicitToken)
		if token == "" {
			return resolvedBot{}, fmt.Errorf("--token cannot be empty")
		}
		return resolvedBot{Token: token}, nil
	}

	if selection.aliasChanged {
		alias := strings.TrimSpace(selection.explicitAlias)
		if alias == "" {
			return resolvedBot{}, fmt.Errorf("--bot cannot be empty")
		}
		return botForAlias(cfg, alias)
	}

	if envToken := strings.TrimSpace(os.Getenv("TG_TOKEN")); envToken != "" {
		return resolvedBot{Token: envToken}, nil
	}
	if envAlias := strings.TrimSpace(os.Getenv("TG_BOT")); envAlias != "" {
		return botForAlias(cfg, envAlias)
	}
	if strings.TrimSpace(cfg.DefaultBot) != "" {
		return botForAlias(cfg, cfg.DefaultBot)
	}
	if len(cfg.Bots) == 0 {
		return resolvedBot{}, fmt.Errorf("no bot is configured (use `tg bot add <alias> --token ...` or pass --token)")
	}
	return resolvedBot{}, fmt.Errorf(
		"no bot selected; use --bot, TG_BOT, or set default_bot / `tg bot default <alias>` (available: %s)",
		strings.Join(sortedBotAliases(cfg.Bots), ", "),
	)
}

func botForAlias(cfg Config, alias string) (resolvedBot, error) {
	alias = strings.TrimSpace(alias)
	if err := validateBotAlias(alias); err != nil {
		return resolvedBot{}, err
	}

	bot, ok := cfg.Bots[alias]
	if !ok {
		aliases := sortedBotAliases(cfg.Bots)
		if len(aliases) == 0 {
			return resolvedBot{}, fmt.Errorf("unknown bot alias %q (no aliases are configured)", alias)
		}
		return resolvedBot{}, fmt.Errorf("unknown bot alias %q (available: %s)", alias, strings.Join(aliases, ", "))
	}
	if strings.TrimSpace(bot.Token) == "" {
		return resolvedBot{}, fmt.Errorf("bot alias %q does not have a token", alias)
	}
	return resolvedBot{
		Token: bot.Token,
		Alias: alias,
		Bot:   &bot,
	}, nil
}

func resolveSendChatID(explicitChatID string, chatChanged bool, bot resolvedBot) (string, error) {
	if chatChanged {
		chatID := strings.TrimSpace(explicitChatID)
		if chatID == "" {
			return "", fmt.Errorf("--chat-id cannot be empty")
		}
		return chatID, nil
	}
	if bot.Bot != nil && strings.TrimSpace(bot.Bot.DefaultChatID) != "" {
		return strings.TrimSpace(bot.Bot.DefaultChatID), nil
	}
	if bot.Alias != "" {
		return "", fmt.Errorf("chat id is required (use --chat-id or set bots.%s.default_chat_id in config)", bot.Alias)
	}
	return "", fmt.Errorf("chat id is required when using --token directly (use --chat-id)")
}

func listBotEntries(cfg Config) []botListEntry {
	aliases := sortedBotAliases(cfg.Bots)
	entries := make([]botListEntry, 0, len(aliases))
	for _, alias := range aliases {
		bot := cfg.Bots[alias]
		entries = append(entries, botListEntry{
			Alias:         alias,
			Default:       alias == cfg.DefaultBot,
			DefaultChatID: bot.DefaultChatID,
			Description:   bot.Description,
			Token:         redactToken(bot.Token),
		})
	}
	return entries
}

func sortedBotAliases(bots map[string]BotConfig) []string {
	aliases := make([]string, 0, len(bots))
	for alias := range bots {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	return aliases
}

func resolvedString(changed bool, value, fallback string) string {
	if changed {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(fallback)
}

func resolvedInt(changed bool, value, fallback int) int {
	if changed {
		return value
	}
	return fallback
}

func resolvedBool(changed bool, value, fallback bool) bool {
	if changed {
		return value
	}
	return fallback
}

func parseChatID(raw string) any {
	raw = strings.TrimSpace(raw)
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return n
	}
	return raw
}

func parseOptionalInt64(raw string) (int64, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("must be a valid integer")
	}
	return n, true, nil
}

func parseAllowedUpdates(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func readMessageFromStdin(force bool) (string, error) {
	if !force {
		info, err := os.Stdin.Stat()
		if err != nil {
			return "", fmt.Errorf("inspect stdin: %w", err)
		}
		if (info.Mode() & os.ModeCharDevice) != 0 {
			return "", nil
		}
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return string(data), nil
}

func writePrettyJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeJSONLine(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	return enc.Encode(v)
}

func summarizeUpdate(update Update) string {
	switch {
	case update.Message != nil:
		return fmt.Sprintf(
			"update_id=%d type=message chat_id=%d from=%s text=%q",
			update.UpdateID,
			update.Message.Chat.ID,
			displayUser(update.Message.From),
			clip(update.Message.Text, 100),
		)
	case update.EditedMessage != nil:
		return fmt.Sprintf(
			"update_id=%d type=edited_message chat_id=%d from=%s text=%q",
			update.UpdateID,
			update.EditedMessage.Chat.ID,
			displayUser(update.EditedMessage.From),
			clip(update.EditedMessage.Text, 100),
		)
	case update.ChannelPost != nil:
		return fmt.Sprintf(
			"update_id=%d type=channel_post chat_id=%d text=%q",
			update.UpdateID,
			update.ChannelPost.Chat.ID,
			clip(update.ChannelPost.Text, 100),
		)
	case update.EditedChannelPost != nil:
		return fmt.Sprintf(
			"update_id=%d type=edited_channel_post chat_id=%d text=%q",
			update.UpdateID,
			update.EditedChannelPost.Chat.ID,
			clip(update.EditedChannelPost.Text, 100),
		)
	case update.CallbackQuery != nil:
		chatID := int64(0)
		if update.CallbackQuery.Message != nil {
			chatID = update.CallbackQuery.Message.Chat.ID
		}
		return fmt.Sprintf(
			"update_id=%d type=callback_query chat_id=%d from=%s data=%q",
			update.UpdateID,
			chatID,
			displayUser(update.CallbackQuery.From),
			clip(update.CallbackQuery.Data, 100),
		)
	default:
		return fmt.Sprintf("update_id=%d type=unknown", update.UpdateID)
	}
}

func displayUser(user *User) string {
	if user == nil {
		return "-"
	}
	if user.Username != "" {
		return "@" + user.Username
	}
	return strconv.FormatInt(user.ID, 10)
}

func clip(text string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	text = strings.TrimSpace(text)
	if len(text) <= maxLen {
		return text
	}
	if maxLen <= 3 {
		return text[:maxLen]
	}
	return text[:maxLen-3] + "..."
}

func nextUpdateOffset(updates []Update, current int64) int64 {
	next := current
	for _, update := range updates {
		if update.UpdateID == math.MaxInt64 {
			return math.MaxInt64
		}
		candidate := update.UpdateID + 1
		if candidate > next {
			next = candidate
		}
	}
	return next
}
