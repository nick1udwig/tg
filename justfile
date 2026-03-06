set shell := ["bash", "-euo", "pipefail", "-c"]

# Build the tg binary into the repo root.
build:
	go build -o tg .

# Install the CLI binary to ~/.local/bin.
install: build
	mkdir -p "$HOME/.local/bin"
	install -m 0755 tg "$HOME/.local/bin/tg"
