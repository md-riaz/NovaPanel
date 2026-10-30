GO ?= go
GOFMT ?= gofmt
STATICCHECK ?= staticcheck
GOVULNCHECK ?= govulncheck

.PHONY: build test lint ci arch integration format-check systemd-verify
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
integration:
	NOVAPANEL_INTEGRATION=1 $(GO) test ./tests/integration -count=1 -v
systemd-verify:
	python3 tools/arch/systemd_verify.py
ci: build lint test arch
	$(GOVULNCHECK) ./...
	$(MAKE) systemd-verify
