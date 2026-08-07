# Installs under $HOME so no step needs root, matching dewey on this host.
PREFIX    ?= $(HOME)/.local
BINDIR    ?= $(PREFIX)/bin
BINARY    ?= morse
GO        ?= /usr/local/go/bin/go
CONFIGDIR ?= $(HOME)/.config/morse

# Stamped into the binary so an installed copy can name its release. A build
# outside a checkout — from a release tarball, as a package manager does — has
# no tag to find, and morse falls back to Go's own build info.
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null)
LDFLAGS   ?= -s -w -X main.version=$(VERSION)

.PHONY: all build test vet clean install uninstall

all: build

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) .

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
	@echo ""
	@echo "Installed."
	@echo "  morse capabilities    # check it is configured"

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)
	@echo "Uninstalled. Config preserved at $(CONFIGDIR)/config.yaml"

