PREFIX    ?= /usr/local
BINDIR    ?= $(PREFIX)/bin
BINARY    ?= salert
SERVICE   ?= salert.service
GO        ?= /usr/local/go/bin/go

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
	mkdir -p ~/.config/salert
	@if [ ! -f ~/.config/salert/config.yaml ]; then \
		install -m 600 config.yaml.example ~/.config/salert/config.yaml; \
		echo "Installed example config to ~/.config/salert/config.yaml — edit with your credentials"; \
	else \
		echo "Config already exists at ~/.config/salert/config.yaml — not overwriting"; \
	fi
	install -d ~/.config/systemd/user
	install -m 644 $(SERVICE) ~/.config/systemd/user/$(SERVICE)
	systemctl --user daemon-reload
	@echo ""
	@echo "Installed. Next steps:"
	@echo "  1. Edit ~/.config/salert/config.yaml with your Telegram bot_token and chat_id"
	@echo "  2. make enable"

uninstall:
	systemctl --user stop $(BINARY) 2>/dev/null || true
	systemctl --user disable $(BINARY) 2>/dev/null || true
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)
	rm -f ~/.config/systemd/user/$(SERVICE)
	systemctl --user daemon-reload
	@echo "Uninstalled. Config preserved at ~/.config/salert/config.yaml"

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
