.PHONY: build clean run dev

# Build the exchange-market binary
build:
	@echo "Building exchange-market..."
	@cd /Users/mohammed/Projects/Skycoin/mohammed/skywire && go build -o bin/exchange-market ./cmd/exchange-market

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -f /Users/mohammed/Projects/Skycoin/mohammed/skywire/bin/exchange-market

# Build and run the exchange-market
run: build
	@echo "Running exchange-market..."
	@/Users/mohammed/Projects/Skycoin/mohammed/skywire/bin/exchange-market

# Run in development mode (standalone, without visor)
dev: build
	@echo "Running exchange-market in dev mode..."
	@/Users/mohammed/Projects/Skycoin/mohammed/skywire/bin/exchange-market --db ./dev.db

# Install dependencies
deps:
	@echo "Installing dependencies..."
	@cd /Users/mohammed/Projects/Skycoin/mohammed/skywire && go mod tidy

# Format code
fmt:
	@echo "Formatting code..."
	@cd /Users/mohammed/Projects/Skycoin/mohammed/skywire && gofmt -s -w ./cmd/exchange-market

# Run tests
test:
	@echo "Running tests..."
	@cd /Users/mohammed/Projects/Skycoin/mohammed/skywire && go test ./cmd/exchange-market/...
