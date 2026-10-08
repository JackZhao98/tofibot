.PHONY: ui-install ui-typecheck ui-build go-test go-build build check docker-build compose-config

ui-install:
	npm --prefix ui ci

ui-typecheck:
	npm --prefix ui run typecheck

ui-build:
	npm --prefix ui run build

go-test:
	go test ./...

go-build:
	mkdir -p build
	go build -o build/tofi ./cmd/tofi

build: ui-build go-build

check: ui-typecheck go-test

docker-build:
	docker build -t tofi:local .

compose-config:
	docker compose config

.PHONY: acceptance
acceptance: ui-build
	mkdir -p build
	go build -o build/tofi-acceptance ./cmd/tofi
	python3 scripts/test_acceptance_guard.py
	python3 scripts/acceptance.py

# Self-host installer and release tooling (see deploy/self-host/README.md).
.PHONY: self-host-test self-host-lint self-host-config release-bundle release-manifest
self-host-test:
	python3 -m unittest discover -s deploy/microvm -p 'test_*.py'
	python3 -m unittest discover -s deploy/self-host -p 'test_*.py'
	python3 -m unittest discover -s scripts/release -p 'test_*.py'

self-host-lint:
	bash -n install.sh deploy/self-host/bin/tofi scripts/release/make_bundle.sh
	shellcheck install.sh deploy/self-host/bin/tofi scripts/release/make_bundle.sh

self-host-config:
	docker compose -f deploy/self-host/compose.yaml --env-file deploy/self-host/testdata/tofi.env --profile tls config --quiet

# make release-bundle VERSION=v0.1.0-rc.1
release-bundle:
	bash scripts/release/make_bundle.sh $(VERSION) dist

# make release-manifest VERSION=... APP_IMAGE=... WORKER_IMAGE=... CADDY_IMAGE=... GUEST_RELEASE_JSON=...
release-manifest:
	python3 scripts/release/make_manifest.py --version $(VERSION) \
	  --app-image $(APP_IMAGE) --worker-image $(WORKER_IMAGE) --caddy-image $(CADDY_IMAGE) \
	  --guest-archive dist/tofi-guest-$(VERSION)-x86_64.tar.zst \
	  --guest-release-json $(GUEST_RELEASE_JSON) \
	  --bundle dist/tofi-host-$(VERSION).tar.gz --out dist/manifest.json
