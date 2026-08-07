# Installs under $HOME so no step needs root, matching dewey on this host.
PREFIX    ?= $(HOME)/.local
BINDIR    ?= $(PREFIX)/bin
BINARY    ?= morse
GO        ?= /usr/local/go/bin/go
CONFIGDIR ?= $(HOME)/.config/morse

.PHONY: all build test vet clean install install-diskguard uninstall

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
	install -m 644 notify@.service $(HOME)/.config/systemd/user/notify@.service
	systemctl --user daemon-reload
	@echo ""
	@echo "Installed. morse is a CLI; there is no service to start."
	@echo "  morse capabilities    # check it is configured"
	@echo "  make install-diskguard   # optional: the disk-space job"

# diskguard is a consumer of morse, installed separately because it is not part
# of it.
install-diskguard:
	install -m 755 contrib/diskguard/diskguard $(DESTDIR)$(BINDIR)/diskguard
	install -d $(HOME)/.config/systemd/user
	install -m 644 contrib/diskguard/diskguard.service $(HOME)/.config/systemd/user/diskguard.service
	install -m 644 contrib/diskguard/diskguard.timer $(HOME)/.config/systemd/user/diskguard.timer
	systemctl --user daemon-reload
	systemctl --user enable --now diskguard.timer
	@echo "diskguard installed; runs every 15 minutes"

uninstall:
	systemctl --user disable --now diskguard.timer 2>/dev/null || true
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY) $(DESTDIR)$(BINDIR)/diskguard
	rm -f $(HOME)/.config/systemd/user/notify@.service
	rm -f $(HOME)/.config/systemd/user/diskguard.service $(HOME)/.config/systemd/user/diskguard.timer
	systemctl --user daemon-reload
	@echo "Uninstalled. Config preserved at $(CONFIGDIR)/config.yaml"

