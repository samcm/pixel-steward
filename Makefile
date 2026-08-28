# The Go binary embeds internal/api/dist, so the frontend must be built before
# any go build that is expected to serve the operator UI.
NPM ?= npm

.PHONY: all ui ui-install ui-test ui-typecheck build test vet clean

all: build

ui-install:
	$(NPM) --prefix web ci

ui: ui-install
	rm -rf internal/api/dist/assets internal/api/dist/index.html
	$(NPM) --prefix web run build

ui-typecheck:
	$(NPM) --prefix web run typecheck

ui-test:
	$(NPM) --prefix web test

build: ui
	CGO_ENABLED=0 go build -trimpath -o pixel-steward ./cmd/pixel-steward

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf internal/api/dist/assets internal/api/dist/index.html web/node_modules pixel-steward
