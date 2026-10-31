GO ?= go
GOFMT ?= gofmt
STATICCHECK ?= staticcheck
GOVULNCHECK ?= govulncheck

.PHONY: build test lint ci arch integration format-check systemd-verify package-maintenance packaging-test release-stage phase6-test
phase6-test:
	$(GO) test -race -count=1 ./internal/update ./internal/firewall ./internal/backup ./internal/audit ./internal/store ./internal/verbs ./internal/rpc ./internal/agent ./cmd/panel-agent ./internal/exec
	$(GO) test -race -count=1 ./tests/integration -run 'TestPackaging|TestFirewallDispatcherStoreIndependence|TestFirewallStartup|TestFirewallJournalRecovery|TestFirewallTerminalCommitProcess|TestFirewallTerminalMigratedProcess'
build:
	$(GO) build ./...
test:
	$(GO) test ./...
format-check:
	@test -z "$$($(GOFMT) -l cmd internal tools tests)" || { $(GOFMT) -l cmd internal tools tests; exit 1; }
lint: format-check
	$(GO) vet ./...
	$(STATICCHECK) ./...
arch:
	$(GO) test ./internal/rpc -run 'TestArchitecture|TestRegisteredVerbs' -count=1
	python3 tools/arch/backstop.py
release-stage:
	@test -n "$(OUT)" || { printf 'OUT staging directory is required\n'; exit 1; }
	mkdir -p "$(OUT)"
	$(GO) build -o "$(OUT)/panel-agent" ./cmd/panel-agent
	$(GO) build -o "$(OUT)/panel-web" ./cmd/panel-web
	$(GO) build -o "$(OUT)/novapanelctl" ./cmd/novapanelctl
	$(MAKE) package-maintenance OUT="$(OUT)/maintenance.tar"
package-maintenance:
	python3 tools/package_maintenance.py "$(OUT)"
packaging-test:
	$(GO) test ./tests/integration -run 'TestPackagingContracts|TestMaintenancePackage|TestReleaseStageIncludesMaintenance' -count=1
integration:
	NOVAPANEL_INTEGRATION=1 $(GO) test ./tests/integration -count=1 -v
systemd-verify:
	python3 tools/arch/systemd_verify.py
ci: build lint test arch
	$(GOVULNCHECK) ./...
	$(MAKE) systemd-verify
