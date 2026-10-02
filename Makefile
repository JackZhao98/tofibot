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
