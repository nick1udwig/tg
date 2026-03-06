# tg

`tg` is a Go CLI for Telegram Bot API workflows used by agents.

It currently supports:

- Sending text messages via `sendMessage`
- One-shot polling via `getUpdates`
- Continuous long polling via `watch`
- Webhook clearing/info for switching between webhooks and polling
- Config/state in `~/.tg` by default

## Install

```bash
go build -o tg .
```

## Quick start

```bash
./tg config init
./tg bot add A --token 123456:ABCDEF... --chat-id 123456789 --description "primary bot" --default
./tg bot add B --token 654321:ZYX... --chat-id @alerts --description "backup bot"
./tg send "hello from tg"
./tg --bot B watch --timeout 30
```

## Config

By default, config is loaded from `~/.tg/config.toml`.

```toml
default_bot = "A"
api_base_url = "https://api.telegram.org"
http_timeout_seconds = 30
poll_timeout_seconds = 25
poll_limit = 100
auto_delete_webhook = false
state_file = "~/.tg/state.json"
output = "summary"

[bots.A]
token = "123456:ABCDEF..."
default_chat_id = "123456789"
description = "primary bot"

[bots.B]
token = "654321:ZYX..."
default_chat_id = "@alerts"
description = "backup bot"
```

`tg config init` writes a commented example block into the file so you can add or edit bot entries by hand.
For `send`, chat resolution is `--chat-id` first, then the selected bot's `default_chat_id`.

Environment overrides are also supported:

- `TG_CONFIG`
- `TG_TOKEN`
- `TG_BOT`
- `TG_API_BASE_URL`
- `TG_HTTP_TIMEOUT`
- `TG_POLL_TIMEOUT`
- `TG_POLL_LIMIT`
- `TG_AUTO_DELETE_WEBHOOK`
- `TG_STATE_FILE`
- `TG_OUTPUT`

## Commands

### Send a message

```bash
./tg send "hello"
./tg --bot A send "hello from bot A"
./tg send --chat-id @otherchannel --parse-mode MarkdownV2 "*hi*"
echo "stdin text" | ./tg send --stdin
```

### Poll once

```bash
./tg poll --timeout 20 --limit 50
./tg --bot B poll --json --save-offset
./tg poll --json --save-offset
```

### Watch (long poll loop)

```bash
./tg --bot A watch --timeout 30
./tg watch --json --state --delete-webhook
```

### Webhook helpers

```bash
./tg --bot A webhook info
./tg --bot A webhook clear --drop-pending-updates
```

### Manage bot aliases

```bash
./tg bot add A --token 123456:ABCDEF... --chat-id 123456789 --description "primary bot" --default
./tg bot add B --token 654321:ZYX... --chat-id @alerts
./tg bot list
./tg bot default B
./tg bot rm A
```

## Future ideas

- `tg reply` to reply directly to an incoming update/message id
- `tg send-photo` / `tg send-document` with local file uploads
- `tg run` mode: command router for `/commands` and callback queries
- `tg tail --chat-id ...` to filter incoming updates by chat
- `tg ack` and offset checkpoints with named consumers
- `tg webhook serve` for local webhook debugging
- `tg media get <file_id>` helper around `getFile`
- `tg rate` / throughput controls for bulk sends
