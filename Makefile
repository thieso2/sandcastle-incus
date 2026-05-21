GO ?= go
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
DESTDIR ?=
BIN_DIR ?= bin

SANDCASTLE_BIN := $(BIN_DIR)/sandcastle
SC_ALIAS := $(BIN_DIR)/sc
SANDCASTLE_ADMIN_BIN := $(BIN_DIR)/sandcastle-admin
SC_ADM_ALIAS := $(BIN_DIR)/sc-adm

.PHONY: build install test e2e-safe clean

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(SANDCASTLE_BIN) ./cmd/sandcastle
	ln -sf sandcastle $(SC_ALIAS)
	$(GO) build -o $(SANDCASTLE_ADMIN_BIN) ./cmd/sandcastle-admin
	ln -sf sandcastle-admin $(SC_ADM_ALIAS)

install: build
	install -d $(DESTDIR)$(BINDIR)
	install -m 0755 $(SANDCASTLE_BIN) $(DESTDIR)$(BINDIR)/sandcastle
	ln -sf sandcastle $(DESTDIR)$(BINDIR)/sc
	install -m 0755 $(SANDCASTLE_ADMIN_BIN) $(DESTDIR)$(BINDIR)/sandcastle-admin
	ln -sf sandcastle-admin $(DESTDIR)$(BINDIR)/sc-adm

test:
	$(GO) test ./...

e2e-safe:
	scripts/e2e.sh unit
	scripts/e2e.sh gated
	scripts/e2e.sh local

clean:
	rm -rf $(BIN_DIR)
