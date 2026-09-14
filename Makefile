.PHONY: build web test run

web:
	cd web && npm ci && npm run build

build: web
	mkdir -p bin
	go build -trimpath -o bin/notifier ./cmd/notifier

test:
	go test -race ./...
	go vet ./...
	cd web && npm run build

run:
	go run ./cmd/notifier
