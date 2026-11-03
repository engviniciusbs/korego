.PHONY: test build web

test:
	go test ./...

build:
	CGO_ENABLED=0 go build -o bin/korego ./cmd/korego

web:
	cd web && pnpm install && pnpm build
