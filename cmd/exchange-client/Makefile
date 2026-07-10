.PHONY: dev build clean

# Install dependencies
install:
	npm install

# Run development server
dev:
	npm run dev

# Build for production
build:
	npm run build

# Clean build artifacts
clean:
	rm -rf dist node_modules

# Build and run Go server with embedded UI
run:
	@echo "Building UI..."
	@npm run build
	@echo "Starting Go server..."
	@go run main.go

# Full build (install + build)
full-build: install build
