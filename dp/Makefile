.PHONY: dev-backend dev-frontend build test lint docker-up docker-down docker-logs package

dev-backend:
	go run ./cmd/dp

dev-frontend:
	cd web && corepack pnpm dev

build:
	cd web && corepack pnpm build
	mkdir -p bin
	go build -o bin/dp ./cmd/dp

test:
	go test ./...
	cd web && corepack pnpm test -- --run

lint:
	go vet ./...
	cd web && corepack pnpm typecheck

docker-up:
	./scripts/dp.sh start

docker-down:
	./scripts/dp.sh down

docker-logs:
	./scripts/dp.sh logs

package:
	./scripts/build-package.sh
