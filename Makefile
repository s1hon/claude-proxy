.PHONY: build run test vet tidy clean dist release

BIN        := bin/claude-proxy
DIST       := dist
LDFLAGS    := -s -w
TARGETS    := darwin-arm64 darwin-amd64
REPO       := s1hon/claude-proxy

# Split a target like "darwin-arm64" into GOOS / GOARCH.
goos    = $(word 1,$(subst -, ,$1))
goarch  = $(word 2,$(subst -, ,$1))

build:
	@mkdir -p bin
	go build -o $(BIN) ./cmd/claude-proxy

run: build
	./$(BIN)

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf bin $(DIST) state.json

# dist: cross-compile every target into $(DIST)/ with stripped symbols
# and generate SHA256SUMS for release integrity.
dist:
	@mkdir -p $(DIST)
	@rm -f $(DIST)/claude-proxy-* $(DIST)/SHA256SUMS
	@for t in $(TARGETS); do \
	  goos=$${t%-*}; goarch=$${t##*-}; \
	  out="$(DIST)/claude-proxy-$$t"; \
	  echo "  building $$out"; \
	  GOOS=$$goos GOARCH=$$goarch CGO_ENABLED=0 \
	    go build -ldflags="$(LDFLAGS)" -o "$$out" ./cmd/claude-proxy || exit 1; \
	done
	@cd $(DIST) && shasum -a 256 claude-proxy-* > SHA256SUMS
	@echo "--- dist ---"
	@ls -lh $(DIST)
	@cat $(DIST)/SHA256SUMS

# release: cut a new GitHub release.
# Usage: make release VERSION=v0.1.1
# Requires: clean working tree, tests passing, gh authenticated.
release: test dist
	@if [ -z "$(VERSION)" ]; then \
	  echo "ERROR: VERSION is required, e.g. make release VERSION=v0.1.1"; exit 1; \
	fi
	@if [ -n "$$(git status --porcelain)" ]; then \
	  echo "ERROR: working tree has uncommitted changes"; git status --short; exit 1; \
	fi
	@if git rev-parse "$(VERSION)" >/dev/null 2>&1; then \
	  echo "ERROR: tag $(VERSION) already exists"; exit 1; \
	fi
	@echo "Tagging $(VERSION)..."
	@git tag "$(VERSION)"
	@git push origin "$(VERSION)"
	@echo "Creating GitHub release $(VERSION)..."
	@gh release create "$(VERSION)" \
	  --repo $(REPO) \
	  --title "$(VERSION)" \
	  --generate-notes \
	  $(DIST)/claude-proxy-darwin-arm64 \
	  $(DIST)/claude-proxy-darwin-amd64 \
	  $(DIST)/SHA256SUMS
	@echo "Released: https://github.com/$(REPO)/releases/tag/$(VERSION)"
