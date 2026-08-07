# Installs under $HOME so no step needs root, matching dewey on this host.
PREFIX    ?= $(HOME)/.local
BINDIR    ?= $(PREFIX)/bin
BINARY    ?= morse
GO        ?= /usr/local/go/bin/go
CONFIGDIR ?= $(HOME)/.config/morse

.PHONY: all build test vet clean install uninstall

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
	install -d -m 700 $(CONFIGDIR)
	@if [ ! -f $(CONFIGDIR)/config.yaml ]; then \
		install -m 600 config.yaml.example $(CONFIGDIR)/config.yaml; \
		echo "Installed example config to $(CONFIGDIR)/config.yaml — edit with your credentials"; \
	else \
		chmod 600 $(CONFIGDIR)/config.yaml; \
		echo "Config already exists at $(CONFIGDIR)/config.yaml — not overwriting (mode set to 600)"; \
	fi
	install -d $(HOME)/.config/systemd/user
	install -m 644 notify@.service $(HOME)/.config/systemd/user/notify@.service
	systemctl --user daemon-reload
	@echo ""
	@echo "Installed. morse is a CLI; there is no service to start."
	@echo "  morse capabilities    # check it is configured"

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)
	rm -f $(HOME)/.config/systemd/user/notify@.service
	systemctl --user daemon-reload
	@echo "Uninstalled. Config preserved at $(CONFIGDIR)/config.yaml"

