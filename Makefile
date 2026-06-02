.PHONY: all build test test-unit test-integration lint clean cross-windows package help

GO        ?= go
GOFLAGS   ?= -trimpath
LDFLAGS   ?= -s -w
GOOS_WIN  := windows
GOARCH    := amd64

CMDS      := launcher onboarding-server agentctl open-folder
DIST      := dist

all: build

help:
	@echo "make build              - build native binaries to dist/<os>/"
	@echo "make cross-windows      - cross-compile windows/amd64 to dist/windows/"
	@echo "make test               - go test -race ./..."
	@echo "make test-unit          - unit tests only (-short)"
	@echo "make test-integration   - integration tests (test/integration)"
	@echo "make lint               - go vet + staticcheck"
	@echo "make ext-build          - build VS Code extension .vsix"
	@echo "make package            - build Windows .exe installer (requires Inno Setup)"
	@echo "make clean              - rm dist/ and out/"

build:
	@mkdir -p $(DIST)/$(shell go env GOOS)
	@for cmd in $(CMDS); do \
		echo "==> building $$cmd"; \
		$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" \
		  -o $(DIST)/$(shell go env GOOS)/$$cmd ./cmd/$$cmd ; \
	done

cross-windows:
	@mkdir -p $(DIST)/windows
	@for cmd in $(CMDS); do \
		echo "==> cross-building $$cmd (windows/amd64)"; \
		GOOS=$(GOOS_WIN) GOARCH=$(GOARCH) \
		  $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" \
		  -o $(DIST)/windows/$$cmd.exe ./cmd/$$cmd ; \
	done

test:
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
	cd extensions/agentserver-vscode && npm ci && npm run package

package: cross-windows ext-build
	bash scripts/package-windows.sh

clean:
	rm -rf $(DIST) out coverage.out
