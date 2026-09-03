.DEFAULT_GOAL := help
.PHONY: help tidy format lint vet test cover check install-linters demo

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

tidy: ## Tidy dependencies
	go mod tidy -v

format: tidy ## Format the code. Needs goimports (make install-linters)
	goimports -w -local github.com/0magnet/realorigin ./

lint: ## Run golangci-lint. Needs it installed (make install-linters)
	golangci-lint run ./...

vet: ## Run go vet
	go vet ./...

test: ## Run the tests
	go test ./... -race -count=1

cover: ## Report test coverage per package
	go test ./... -cover -count=1

check: lint vet test ## Run linters, vet and tests

install-linters: ## Install the linters
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	go install golang.org/x/tools/cmd/goimports@latest

demo: ## Run the demo on localhost:7999
	go run ./cmd/realorigin-demo
