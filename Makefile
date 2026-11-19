.PHONY: dev web build test docker

web:
	cd web && npm ci && npm run build

build: web
	CGO_ENABLED=0 go build -trimpath -o easycloud ./cmd/easycloud

test:
	go vet ./...
	go test -race ./...

# Backend on :8080 with a local data dir; run `npm run dev` in web/ for the UI with hot reload.
dev:
	mkdir -p .dev/data .dev/app
	EASYCLOUD_DATA_ROOT=.dev/data EASYCLOUD_APP_DATA=.dev/app EASYCLOUD_HTTP_LISTEN=127.0.0.1:8080 go run ./cmd/easycloud

docker:
	docker build -f deploy/docker/Dockerfile -t easycloud:dev .
