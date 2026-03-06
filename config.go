package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/pelletier/go-toml/v2"
)

const (
	defaultConfigDirName = ".tg"
	defaultConfigName    = "config.toml"
	defaultStateName     = "state.json"
)

type Config struct {
	DefaultBot         string               `json:"default_bot,omitempty" toml:"default_bot,omitempty"`
	Bots               map[string]BotConfig `json:"bots,omitempty" toml:"bots,omitempty"`
	APIBaseURL         string               `json:"api_base_url,omitempty" toml:"api_base_url,omitempty"`
	HTTPTimeoutSeconds int                  `json:"http_timeout_seconds,omitempty" toml:"http_timeout_seconds,omitempty"`
	PollTimeoutSeconds int                  `json:"poll_timeout_seconds,omitempty" toml:"poll_timeout_seconds,omitempty"`
	PollLimit          int                  `json:"poll_limit,omitempty" toml:"poll_limit,omitempty"`
	AutoDeleteWebhook  bool                 `json:"auto_delete_webhook,omitempty" toml:"auto_delete_webhook,omitempty"`
	StateFile          string               `json:"state_file,omitempty" toml:"state_file,omitempty"`
	Output             string               `json:"output,omitempty" toml:"output,omitempty"`
}

type BotConfig struct {
	Token         string `json:"token,omitempty" toml:"token,omitempty"`
	DefaultChatID string `json:"default_chat_id,omitempty" toml:"default_chat_id,omitempty"`
	Description   string `json:"description,omitempty" toml:"description,omitempty"`
}

type State struct {
	Offsets map[string]int64 `json:"offsets,omitempty"`
}

func defaultConfig() Config {
	return Config{
		APIBaseURL:         defaultAPIBaseURL,
		HTTPTimeoutSeconds: 30,
		PollTimeoutSeconds: 25,
		PollLimit:          100,
		Output:             "summary",
	}
}

func normalizeConfig(cfg *Config) error {
	cfg.DefaultBot = strings.TrimSpace(cfg.DefaultBot)
	cfg.APIBaseURL = strings.TrimSpace(cfg.APIBaseURL)
	cfg.StateFile = strings.TrimSpace(cfg.StateFile)

	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = defaultAPIBaseURL
	}
	if cfg.HTTPTimeoutSeconds <= 0 {
		cfg.HTTPTimeoutSeconds = 30
	}
	if cfg.PollTimeoutSeconds < 0 {
		cfg.PollTimeoutSeconds = 0
	}
	if cfg.PollLimit <= 0 {
		cfg.PollLimit = 100
	}

	cfg.Output = strings.ToLower(strings.TrimSpace(cfg.Output))
	if cfg.Output == "" {
		cfg.Output = "summary"
	}
	if cfg.Output != "summary" && cfg.Output != "json" {
		cfg.Output = "summary"
	}

	if len(cfg.Bots) == 0 {
		cfg.Bots = nil
	} else {
		normalizedBots := make(map[string]BotConfig, len(cfg.Bots))
		for alias, bot := range cfg.Bots {
			alias = strings.TrimSpace(alias)
			if err := validateBotAlias(alias); err != nil {
				return err
			}
			bot.Token = strings.TrimSpace(bot.Token)
			bot.DefaultChatID = strings.TrimSpace(bot.DefaultChatID)
			bot.Description = strings.TrimSpace(bot.Description)
			if bot.Token == "" {
				return fmt.Errorf("bot alias %q must include a token", alias)
			}
			if _, exists := normalizedBots[alias]; exists {
				return fmt.Errorf("duplicate bot alias %q", alias)
			}
			normalizedBots[alias] = bot
		}
		cfg.Bots = normalizedBots
	}

	if cfg.DefaultBot != "" {
		if err := validateBotAlias(cfg.DefaultBot); err != nil {
			return fmt.Errorf("default bot: %w", err)
		}
		if _, ok := cfg.Bots[cfg.DefaultBot]; !ok {
			return fmt.Errorf("default_bot %q is not defined in bots", cfg.DefaultBot)
		}
	}

	return nil
}

func defaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, defaultConfigDirName, defaultConfigName), nil
}

func defaultStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, defaultConfigDirName, defaultStateName), nil
}

func expandPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, path[2:])
		}
	}
	return filepath.Clean(path), nil
}

func resolveConfigPath(rawPath string) (string, error) {
	if rawPath != "" {
		return expandPath(rawPath)
	}
	if envPath := strings.TrimSpace(os.Getenv("TG_CONFIG")); envPath != "" {
		return expandPath(envPath)
	}
	return defaultConfigPath()
}

func mergeConfig(base, override Config) Config {
	cfg := base
	if strings.TrimSpace(override.DefaultBot) != "" {
		cfg.DefaultBot = strings.TrimSpace(override.DefaultBot)
	}
	if len(override.Bots) > 0 {
		cfg.Bots = cloneBots(override.Bots)
	}
	if strings.TrimSpace(override.APIBaseURL) != "" {
		cfg.APIBaseURL = strings.TrimSpace(override.APIBaseURL)
	}
	if override.HTTPTimeoutSeconds > 0 {
		cfg.HTTPTimeoutSeconds = override.HTTPTimeoutSeconds
	}
	if override.PollTimeoutSeconds >= 0 {
		cfg.PollTimeoutSeconds = override.PollTimeoutSeconds
	}
	if override.PollLimit > 0 {
		cfg.PollLimit = override.PollLimit
	}
	if override.AutoDeleteWebhook {
		cfg.AutoDeleteWebhook = true
	}
	if strings.TrimSpace(override.StateFile) != "" {
		cfg.StateFile = strings.TrimSpace(override.StateFile)
	}
	if strings.TrimSpace(override.Output) != "" {
		cfg.Output = strings.TrimSpace(override.Output)
	}
	_ = normalizeConfig(&cfg)
	return cfg
}

func LoadConfig(rawPath string) (Config, string, error) {
	path, err := resolveConfigPath(rawPath)
	if err != nil {
		return Config{}, "", err
	}
	cfg := defaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if err := normalizeConfig(&cfg); err != nil {
				return Config{}, "", err
			}
			return cfg, path, nil
		}
		return Config{}, "", fmt.Errorf("read config %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		if err := normalizeConfig(&cfg); err != nil {
			return Config{}, "", err
		}
		return cfg, path, nil
	}

	var fileCfg Config
	if err := decodeConfigData(data, path, &fileCfg); err != nil {
		return Config{}, "", err
	}
	cfg = mergeConfig(cfg, fileCfg)
	if err := normalizeConfig(&cfg); err != nil {
		return Config{}, "", fmt.Errorf("normalize config %s: %w", path, err)
	}
	return cfg, path, nil
}

func parseBool(raw string) (bool, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	switch raw {
	case "1", "true", "t", "yes", "y", "on":
		return true, nil
	case "0", "false", "f", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", raw)
	}
}

func applyEnvOverrides(cfg Config) (Config, error) {
	out := cfg

	if v := strings.TrimSpace(os.Getenv("TG_API_BASE_URL")); v != "" {
		out.APIBaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("TG_STATE_FILE")); v != "" {
		out.StateFile = v
	}
	if v := strings.TrimSpace(os.Getenv("TG_OUTPUT")); v != "" {
		out.Output = v
	}
	if v := strings.TrimSpace(os.Getenv("TG_HTTP_TIMEOUT")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("TG_HTTP_TIMEOUT must be a positive integer")
		}
		out.HTTPTimeoutSeconds = n
	}
	if v := strings.TrimSpace(os.Getenv("TG_POLL_TIMEOUT")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return Config{}, fmt.Errorf("TG_POLL_TIMEOUT must be a non-negative integer")
		}
		out.PollTimeoutSeconds = n
	}
	if v := strings.TrimSpace(os.Getenv("TG_POLL_LIMIT")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 100 {
			return Config{}, fmt.Errorf("TG_POLL_LIMIT must be an integer between 1 and 100")
		}
		out.PollLimit = n
	}
	if v := strings.TrimSpace(os.Getenv("TG_AUTO_DELETE_WEBHOOK")); v != "" {
		b, err := parseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("TG_AUTO_DELETE_WEBHOOK: %w", err)
		}
		out.AutoDeleteWebhook = b
	}

	if err := normalizeConfig(&out); err != nil {
		return Config{}, err
	}
	return out, nil
}

func SaveConfig(path string, cfg Config) error {
	path, err := expandPath(path)
	if err != nil {
		return err
	}
	if err := normalizeConfig(&cfg); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	payload, err := marshalConfigFile(cfg)
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func resolveStatePath(cfg Config, override string) (string, error) {
	if override != "" {
		return expandPath(override)
	}
	if strings.TrimSpace(cfg.StateFile) != "" {
		return expandPath(cfg.StateFile)
	}
	return defaultStatePath()
}

func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{Offsets: map[string]int64{}}, nil
		}
		return State{}, fmt.Errorf("read state %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return State{Offsets: map[string]int64{}}, nil
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("decode state %s: %w", path, err)
	}
	if state.Offsets == nil {
		state.Offsets = map[string]int64{}
	}
	return state, nil
}

func SaveState(path string, state State) error {
	if state.Offsets == nil {
		state.Offsets = map[string]int64{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	return nil
}

func RedactedConfig(cfg Config) Config {
	out := cfg
	if len(out.Bots) > 0 {
		out.Bots = cloneBots(out.Bots)
		for alias, bot := range out.Bots {
			bot.Token = redactToken(bot.Token)
			out.Bots[alias] = bot
		}
	}
	return out
}

func decodeConfigData(data []byte, path string, out *Config) error {
	var tomlCfg Config
	if err := toml.Unmarshal(data, &tomlCfg); err != nil {
		return fmt.Errorf("decode config %s: %w", path, err)
	}
	*out = tomlCfg
	return nil
}

func writePrettyConfig(w io.Writer, cfg Config) error {
	data, err := marshalConfigData(cfg)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func marshalConfigData(cfg Config) ([]byte, error) {
	data, err := toml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	return data, nil
}

func marshalConfigFile(cfg Config) ([]byte, error) {
	data, err := marshalConfigData(cfg)
	if err != nil {
		return nil, err
	}
	data = append(data, []byte(configTemplateCommentBlock())...)
	return data, nil
}

func configTemplateCommentBlock() string {
	return strings.TrimLeft(`
# default_bot is the alias that gets used when you do not pass --bot.
# default_bot = "sieve"
#
# [bots.sieve]
# token = "123456:ABC..."
# default_chat_id = "123456789"
# description = "my sieve bot"
#
# [bots.other-notifications]
# token = "654321:XYZ..."
# default_chat_id = "@alerts"
# description = "miscellaneous notifications"
`, "\n")
}

func cloneBots(src map[string]BotConfig) map[string]BotConfig {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]BotConfig, len(src))
	for alias, bot := range src {
		dst[alias] = bot
	}
	return dst
}

func validateBotAlias(alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return fmt.Errorf("bot alias is required")
	}
	if strings.IndexFunc(alias, unicode.IsSpace) >= 0 {
		return fmt.Errorf("bot alias %q cannot contain spaces", alias)
	}
	return nil
}

func redactToken(token string) string {
	token = strings.TrimSpace(token)
	if len(token) <= 8 {
		return "********"
	}
	return token[:4] + "..." + token[len(token)-4:]
}
