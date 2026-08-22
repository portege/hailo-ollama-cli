# Hailo Ollama CLI - Makefile
#
# Cross-platform builds for linux/arm64, linux/amd64, darwin/arm64, darwin/amd64.
# Place the resulting binary in ./dist/ (or install to /usr/local/bin).

PROJECT_NAME := hailo-ollama-cli
DIST_DIR     := dist

# Default target architecture for local build
GOOS   ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

# Build flags
LDFLAGS := -s -w

# Supported cross-compilation targets
TARGETS := \
	linux/arm64 \
	linux/amd64 \
	darwin/arm64 \
	darwin/amd64

.PHONY: all
all: build

.PHONY: build
build:
	@mkdir -p $(DIST_DIR)
	@echo "Building $(PROJECT_NAME) for $(GOOS)/$(GOARCH)..."
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/$(PROJECT_NAME)-$(GOOS)-$(GOARCH) .

.PHONY: run
run:
	go run .

.PHONY: test
test:
	go test ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: clean
clean:
	@rm -rf $(DIST_DIR)
	@echo "Cleaned $(DIST_DIR)/"

.PHONY: dist
dist: clean
	@mkdir -p $(DIST_DIR)
	@for target in $(TARGETS); do \
		os=$$(echo $$target | cut -d'/' -f1); \
		arch=$$(echo $$target | cut -d'/' -f2); \
		output="$(DIST_DIR)/$(PROJECT_NAME)-$$os-$$arch"; \
		echo "Building $$os/$$arch..."; \
		GOOS=$$os GOARCH=$$arch go build -ldflags="$(LDFLAGS)" -o $$output .; \
		chmod +x $$output; \
	done
	@echo "All binaries built in $(DIST_DIR)/"

.PHONY: install
install: build
	@echo "Installing $(PROJECT_NAME) to /usr/local/bin/$(PROJECT_NAME)..."
	@cp $(DIST_DIR)/$(PROJECT_NAME)-$(GOOS)-$(GOARCH) /usr/local/bin/$(PROJECT_NAME)
	@chmod +x /usr/local/bin/$(PROJECT_NAME)
	@echo "Installed. Run '$(PROJECT_NAME) --help' to get started."

.PHONY: uninstall
uninstall:
	@echo "Removing /usr/local/bin/$(PROJECT_NAME)..."
	@rm -f /usr/local/bin/$(PROJECT_NAME)
