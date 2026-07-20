.PHONY: build clean run dev

# Build the skydex-market binary
build:
	@echo "Building skydex-market..."
	@cd /Users/mohammed/Projects/Skycoin/mohammed/skywire && go build -o bin/skydex-market ./cmd/apps/skydex-market

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -f /Users/mohammed/Projects/Skycoin/mohammed/skywire/bin/skydex-market

# Build and run the skydex-market
run: build
	@echo "Running skydex-market..."
	@/Users/mohammed/Projects/Skycoin/mohammed/skywire/bin/skydex-market

# Run in development mode (standalone, without visor)
dev: build
	@echo "Running skydex-market in dev mode..."
	@/Users/mohammed/Projects/Skycoin/mohammed/skywire/bin/skydex-market --db ./dev.db

# Install dependencies
deps:
	@echo "Installing dependencies..."
	@cd /Users/mohammed/Projects/Skycoin/mohammed/skywire && go mod tidy

# Format code
fmt:
	@echo "Formatting code..."
	@cd /Users/mohammed/Projects/Skycoin/mohammed/skywire && gofmt -s -w ./cmd/apps/skydex-market

# Run tests
test:
	@echo "Running tests..."
	@cd /Users/mohammed/Projects/Skycoin/mohammed/skywire && go test ./cmd/apps/skydex-market/...
