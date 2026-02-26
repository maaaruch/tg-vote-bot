APP_NAME=bot

.PHONY: help
help: ## show available commands
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: run
run: ## run bot locally (expects env vars or .env)
	@go run ./cmd/bot

.PHONY: build
build: ## build binary to ./bin/bot
	@mkdir -p bin
	@go build -o ./bin/$(APP_NAME) ./cmd/bot

.PHONY: test
test: ## run tests
	@go test ./...

.PHONY: fmt
fmt: ## gofmt all
	@gofmt -w .

.PHONY: lint
lint: ## run golangci-lint (needs to be installed)
	@golangci-lint run

.PHONY: docker-build
docker-build: ## build docker image
	@docker build -t tg-vote-bot:local .

.PHONY: compose-up
compose-up: ## run via docker compose
	@docker compose up --build