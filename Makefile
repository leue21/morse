# Installs under $HOME so no step needs root, matching dewey on this host.
PREFIX    ?= $(HOME)/.local
BINDIR    ?= $(PREFIX)/bin
BINARY    ?= morse
SERVICE   ?= morse.service
GO        ?= /usr/local/go/bin/go
CONFIGDIR ?= $(HOME)/.config/morse

.PHONY: all build test vet clean install uninstall enable disable restart status logs

all: build

build:
	$(GO) build -o $(BINARY) .

test:
	$(GO) test ./... -v -count=1

vet:
	$(GO) vet ./...

clean:
	rm -f $(BINARY) coverage.out

## Installation

install: build
	install -d $(DESTDIR)$(BINDIR)
	install -m 755 $(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)
	mkdir -p $(CONFIGDIR)
	@if [ ! -f $(CONFIGDIR)/config.yaml ]; then \
		install -m 600 config.yaml.example $(CONFIGDIR)/config.yaml; \
		echo "Installed example config to $(CONFIGDIR)/config.yaml — edit with your credentials"; \
	else \
		echo "Config already exists at $(CONFIGDIR)/config.yaml — not overwriting"; \
	fi
	install -d $(HOME)/.config/systemd/user
	install -m 644 $(SERVICE) $(HOME)/.config/systemd/user/$(SERVICE)
	systemctl --user daemon-reload
	@echo ""
	@echo "Installed. Next steps:"
	@echo "  1. Edit $(CONFIGDIR)/config.yaml with your Telegram bot_token and chat_id"
	@echo "  2. make enable"

uninstall:
	systemctl --user stop $(BINARY) 2>/dev/null || true
	systemctl --user disable $(BINARY) 2>/dev/null || true
	sudo rm -f $(DESTDIR)$(BINDIR)/$(BINARY)
	rm -f $(HOME)/.config/systemd/user/$(SERVICE)
	systemctl --user daemon-reload
	@echo "Uninstalled. Config preserved at $(CONFIGDIR)/config.yaml"

## Service management

enable:
	systemctl --user enable --now $(BINARY)

disable:
	systemctl --user disable --now $(BINARY)

restart:
	systemctl --user restart $(BINARY)

status:
	systemctl --user status $(BINARY)

logs:
	journalctl --user -u $(BINARY) -f
