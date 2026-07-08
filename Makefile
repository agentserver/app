.PHONY: all build test test-unit test-integration lint clean cross-windows cross-linux package package-linux npm-audit help ui-build ui-test

GO        ?= go
GOFLAGS   ?= -trimpath
LDFLAGS   ?= -s -w
NPM       ?= npm
NPM_CI_FLAGS ?= --audit=false --fund=false --loglevel=error
NPM_AUDIT_FLAGS ?= --audit-level=moderate
GOOS_WIN  := windows
GOARCH    := amd64

CMDS      := launcher onboarding-server agentctl codex-debug-wrapper open-folder uninstall token-refresher
DIST      := dist

all: build

help:
	@echo "make build              - build native binaries to dist/<os>/"
	@echo "make cross-windows      - cross-compile windows/amd64 to dist/windows/ (depends on ui-build)"
	@echo "make cross-linux        - cross-compile linux amd64/arm64 agentserver"
	@echo "make test               - go test -race ./..."
	@echo "make test-unit          - unit tests only (-short)"
	@echo "make test-integration   - integration tests (test/integration)"
	@echo "make lint               - go vet + staticcheck"
	@echo "make ext-build          - build VS Code extension .vsix"
	@echo "make ui-build           - build onboarding Vue front-end into internal/ui/assets/dist/"
	@echo "make ui-test            - run frontend unit tests"
	@echo "make npm-audit          - audit frontend and VS Code extension dependencies"
	@echo "make package            - build Windows .exe installer (requires Inno Setup; depends on ui-build + ext-build)"
	@echo "make package-linux      - build Linux headless tarballs"
	@echo "make clean              - rm dist/ and out/"

build: ui-build
	@mkdir -p $(DIST)/$(shell go env GOOS)
	@for cmd in $(CMDS); do \
		echo "==> building $$cmd"; \
		$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" \
		  -o $(DIST)/$(shell go env GOOS)/$$cmd ./cmd/$$cmd ; \
	done

cross-windows: ui-build
	@mkdir -p $(DIST)/windows
	@for cmd in $(CMDS); do \
		echo "==> cross-building $$cmd (windows/amd64)"; \
		ldflags="$(LDFLAGS)"; \
		case "$$cmd" in launcher|onboarding-server|open-folder|token-refresher) ldflags="$(LDFLAGS) -H=windowsgui" ;; esac; \
		GOOS=$(GOOS_WIN) GOARCH=$(GOARCH) \
		  $(GO) build $(GOFLAGS) -ldflags="$$ldflags" \
		  -o $(DIST)/windows/$$cmd.exe ./cmd/$$cmd ; \
	done

cross-linux:
	@mkdir -p $(DIST)/linux/amd64 $(DIST)/linux/arm64
	@for arch in amd64 arm64; do \
		echo "==> cross-building agentserver (linux/$$arch)"; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch \
		  $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" \
		  -o $(DIST)/linux/$$arch/agentserver ./cmd/agentserver ; \
	done

test: ui-build
	$(GO) test -race -count=1 ./...

test-unit:
	$(GO) test -race -short -count=1 ./...

test-integration:
	$(GO) test -race -count=1 -tags=integration ./test/integration/...

lint:
	$(GO) vet ./...
	@which staticcheck >/dev/null 2>&1 || $(GO) install honnef.co/go/tools/cmd/staticcheck@latest
	staticcheck ./...

ext-build:
	cd extensions/agentserver-app && $(NPM) ci $(NPM_CI_FLAGS) && $(NPM) run compile && $(NPM) run package

ui-build:
	cd internal/ui/web && $(NPM) ci $(NPM_CI_FLAGS) && $(NPM) run build

ui-test:
	cd internal/ui/web && $(NPM) ci $(NPM_CI_FLAGS) && $(NPM) test

npm-audit:
	cd internal/ui/web && $(NPM) audit $(NPM_AUDIT_FLAGS)
	cd extensions/agentserver-app && $(NPM) audit $(NPM_AUDIT_FLAGS)

package: npm-audit cross-windows ext-build
	bash scripts/package-windows.sh

package-linux: cross-linux
	OUT="$(DIST)" bash scripts/package-linux.sh

clean:
	rm -rf $(DIST) out coverage.out internal/ui/assets/dist
