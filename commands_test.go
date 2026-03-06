package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveBotTokenPrecedence(t *testing.T) {
	t.Setenv("TG_TOKEN", "")
	t.Setenv("TG_BOT", "")

	cfg := Config{
		DefaultBot: "B",
		Bots: map[string]BotConfig{
			"A": {Token: "token-a"},
			"B": {Token: "token-b"},
		},
	}

	tests := []struct {
		name      string
		selection tokenSelection
		envToken  string
		envAlias  string
		want      string
	}{
		{
			name: "explicit token wins",
			selection: tokenSelection{
				explicitToken: "cli-token",
				tokenChanged:  true,
				explicitAlias: "A",
				aliasChanged:  true,
			},
			envToken: "env-token",
			envAlias: "B",
			want:     "cli-token",
		},
		{
			name: "explicit alias beats environment",
			selection: tokenSelection{
				explicitAlias: "A",
				aliasChanged:  true,
			},
			envToken: "env-token",
			envAlias: "B",
			want:     "token-a",
		},
		{
			name:     "environment token beats environment alias",
			envToken: "env-token",
			envAlias: "B",
			want:     "env-token",
		},
		{
			name:     "environment alias beats configured default",
			envAlias: "A",
			want:     "token-a",
		},
		{
			name: "configured default is used when nothing else is set",
			want: "token-b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TG_TOKEN", tt.envToken)
			t.Setenv("TG_BOT", tt.envAlias)

			got, err := resolveBotToken(cfg, tt.selection)
			if err != nil {
				t.Fatalf("resolveBotToken returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveBotToken = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveBotTokenRequiresSelectionWhenNoDefault(t *testing.T) {
	t.Setenv("TG_TOKEN", "")
	t.Setenv("TG_BOT", "")

	cfg := Config{
		Bots: map[string]BotConfig{
			"A": {Token: "token-a"},
		},
	}

	_, err := resolveBotToken(cfg, tokenSelection{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no bot selected") {
		t.Fatalf("expected missing-selection hint, got %v", err)
	}
}

func TestBotCommandsLifecycle(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")

	if _, _, err := executeCLI("--config", configPath, "bot", "add", "A", "--token", "111:AAA", "--chat-id", "123456789", "--description", "primary", "--default"); err != nil {
		t.Fatalf("add A failed: %v", err)
	}
	if _, _, err := executeCLI("--config", configPath, "bot", "add", "B", "--token", "222:BBB", "--chat-id", "@backup", "--description", "backup"); err != nil {
		t.Fatalf("add B failed: %v", err)
	}

	cfg, _, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.DefaultBot != "A" {
		t.Fatalf("default bot = %q, want %q", cfg.DefaultBot, "A")
	}
	if cfg.Bots["A"].DefaultChatID != "123456789" {
		t.Fatalf("A default chat id = %q, want %q", cfg.Bots["A"].DefaultChatID, "123456789")
	}
	if cfg.Bots["A"].Description != "primary" {
		t.Fatalf("A description = %q, want %q", cfg.Bots["A"].Description, "primary")
	}

	stdout, _, err := executeCLI("--config", configPath, "bot", "list", "--json")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}

	var entries []botListEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("list output is not valid json: %v\n%s", err, stdout)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 bot entries, got %d", len(entries))
	}
	if entries[0].DefaultChatID == "" && entries[1].DefaultChatID == "" {
		t.Fatalf("expected bot list to include default chat ids, got %+v", entries)
	}
	if entries[0].Token == "111:AAA" || entries[1].Token == "222:BBB" {
		t.Fatalf("expected redacted tokens, got %+v", entries)
	}

	if _, _, err := executeCLI("--config", configPath, "bot", "default", "B"); err != nil {
		t.Fatalf("bot default failed: %v", err)
	}
	if _, _, err := executeCLI("--config", configPath, "bot", "rm", "A"); err != nil {
		t.Fatalf("bot rm failed: %v", err)
	}

	cfg, _, err = LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig after updates failed: %v", err)
	}
	if cfg.DefaultBot != "B" {
		t.Fatalf("default bot after update = %q, want %q", cfg.DefaultBot, "B")
	}
	if _, ok := cfg.Bots["A"]; ok {
		t.Fatal("bot alias A should have been removed")
	}
}

func TestRootTokenFlagAppliesToSubcommands(t *testing.T) {
	t.Setenv("TG_CHAT_ID", "")
	configPath := filepath.Join(t.TempDir(), "config.toml")
	_, _, err := executeCLI("--config", configPath, "--token", "111:AAA", "send")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "chat id is required") {
		t.Fatalf("expected chat id error from resolved token, got %v", err)
	}
}

func TestRootBotFlagAppliesToSubcommands(t *testing.T) {
	t.Setenv("TG_CHAT_ID", "")
	configPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := Config{
		Bots: map[string]BotConfig{
			"A": {Token: "111:AAA", DefaultChatID: "123456789"},
		},
	}
	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	_, _, err := executeCLI("--config", configPath, "--bot", "A", "send")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "message text is required") {
		t.Fatalf("expected message text error from resolved bot alias, got %v", err)
	}
}

func TestConfigShowRedactsBotTokens(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")

	cfg := Config{
		DefaultBot: "A",
		Bots: map[string]BotConfig{
			"A": {Token: "111:AAA", DefaultChatID: "123456789", Description: "primary"},
		},
	}
	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	stdout, _, err := executeCLI("--config", configPath, "config", "show")
	if err != nil {
		t.Fatalf("config show failed: %v", err)
	}
	if strings.Contains(stdout, "111:AAA") {
		t.Fatalf("config show leaked raw token:\n%s", stdout)
	}
	if !strings.Contains(stdout, "default_bot =") {
		t.Fatalf("config show did not emit toml:\n%s", stdout)
	}
	if !strings.Contains(stdout, "[bots.A]") {
		t.Fatalf("config show missing bot table:\n%s", stdout)
	}
	if !strings.Contains(stdout, "default_chat_id =") {
		t.Fatalf("config show missing bot chat id:\n%s", stdout)
	}
	if strings.Contains(stdout, "# Edit by hand:") {
		t.Fatalf("config show should not include file template comments:\n%s", stdout)
	}
}

func TestSaveConfigWritesTOMLTemplateComments(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")

	cfg := Config{
		DefaultBot: "A",
		Bots: map[string]BotConfig{
			"A": {Token: "111:AAA", DefaultChatID: "123456789", Description: "primary"},
		},
	}
	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	text := string(data)
	if strings.HasPrefix(strings.TrimSpace(text), "{") {
		t.Fatalf("expected toml, got json-like payload:\n%s", text)
	}
	if !strings.Contains(text, "default_bot =") {
		t.Fatalf("missing default_bot in toml:\n%s", text)
	}
	if !strings.Contains(text, "[bots.A]") {
		t.Fatalf("missing bot table in toml:\n%s", text)
	}
	if !strings.Contains(text, "default_chat_id =") {
		t.Fatalf("missing bot chat id in toml:\n%s", text)
	}
	if !strings.Contains(text, "# Edit by hand:") {
		t.Fatalf("missing commented template block:\n%s", text)
	}
	if !strings.Contains(text, "# default_bot = \"A\"") {
		t.Fatalf("missing default_bot example in template block:\n%s", text)
	}
}

func TestConfigInitWritesHandEditTemplate(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")

	if _, _, err := executeCLI("--config", configPath, "config", "init"); err != nil {
		t.Fatalf("config init failed: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "# [bots.A]") {
		t.Fatalf("missing commented bot example:\n%s", text)
	}
	if !strings.Contains(text, "# default_chat_id = \"123456789\"") {
		t.Fatalf("missing commented bot chat example:\n%s", text)
	}
	if !strings.Contains(text, "# Set default_bot to the alias you want tg to use when you do not pass --bot.") {
		t.Fatalf("missing hand-edit guidance:\n%s", text)
	}
}

func executeCLI(args ...string) (string, string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runE(context.Background(), args, &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}
