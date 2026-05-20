GO ?= go
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
DESTDIR ?=
BIN_DIR ?= bin

SANDCASTLE_BIN := $(BIN_DIR)/sandcastle
SC_ALIAS := $(BIN_DIR)/sc

.PHONY: build install test e2e-safe clean

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(SANDCASTLE_BIN) ./cmd/sandcastle
	ln -sf sandcastle $(SC_ALIAS)

install: build
	install -d $(DESTDIR)$(BINDIR)
	install -m 0755 $(SANDCASTLE_BIN) $(DESTDIR)$(BINDIR)/sandcastle
	ln -sf sandcastle $(DESTDIR)$(BINDIR)/sc

test:
	$(GO) test ./...

e2e-safe:
	scripts/e2e.sh unit
	scripts/e2e.sh gated
	scripts/e2e.sh local

clean:
	rm -rf $(BIN_DIR)
