PREFIX     ?= /usr/local
BINDIR     ?= $(PREFIX)/bin
CONFIGDIR  ?= /etc/salert
SERVICEDIR ?= /etc/systemd/system
BINARY     ?= salert
SERVICE    ?= salert.service
GO         ?= go

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

## Installation (requires root)

install: build
	install -d $(DESTDIR)$(BINDIR)
	install -m 755 $(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)
	install -d $(DESTDIR)$(CONFIGDIR)
	@if [ ! -f $(DESTDIR)$(CONFIGDIR)/config.yaml ]; then \
		install -m 640 config.yaml $(DESTDIR)$(CONFIGDIR)/config.yaml; \
		echo "Installed example config to $(CONFIGDIR)/config.yaml — edit with your credentials"; \
	else \
		echo "Config already exists at $(CONFIGDIR)/config.yaml — not overwriting"; \
	fi
	install -m 644 $(SERVICE) $(DESTDIR)$(SERVICEDIR)/$(SERVICE)
	@id -u salert >/dev/null 2>&1 || useradd -r -s /usr/sbin/nologin salert
	chown root:salert $(DESTDIR)$(CONFIGDIR)/config.yaml
	systemctl daemon-reload
	@echo ""
	@echo "Installed. Next steps:"
	@echo "  1. Edit $(CONFIGDIR)/config.yaml with your Telegram bot_token and chat_id"
	@echo "  2. sudo make enable"

uninstall:
	systemctl stop $(BINARY) 2>/dev/null || true
	systemctl disable $(BINARY) 2>/dev/null || true
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)
	rm -f $(DESTDIR)$(SERVICEDIR)/$(SERVICE)
	systemctl daemon-reload
	@echo "Uninstalled. Config preserved at $(CONFIGDIR)/config.yaml"

## Service management

enable:
	systemctl enable --now $(BINARY)

disable:
	systemctl disable --now $(BINARY)

restart:
	systemctl restart $(BINARY)

status:
	systemctl status $(BINARY)

logs:
	journalctl -u $(BINARY) -f
