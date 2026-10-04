GO ?= go
NPM ?= npm
CMAKE ?= cmake
CTEST ?= ctest
BUILD_DIR ?= build
NATIVE_BUILD_DIR := $(BUILD_DIR)/native
BINARY := $(BUILD_DIR)/Zhulong
SWAG_VERSION := v1.16.6

.PHONY: all build check frontend-install frontend-build frontend-check native-configure native-build native-test api-docs go-check smoke run web-dev

all: build

build: go-build

go-build: frontend-build native-build api-docs
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 $(GO) build -o $(BINARY) ./cmd/Zhulong

frontend-install:
	$(NPM) ci --prefix web

frontend-build: frontend-install
	$(NPM) run build --prefix web

frontend-check: frontend-install
	$(NPM) run lint --prefix web
	$(NPM) run type-check --prefix web
	$(NPM) test --prefix web
	$(NPM) audit --prefix web
	$(NPM) run build --prefix web

native-configure:
	$(CMAKE) -S native -B $(NATIVE_BUILD_DIR) -DCMAKE_BUILD_TYPE=RelWithDebInfo -DBUILD_TESTING=ON

native-build: native-configure
	$(CMAKE) --build $(NATIVE_BUILD_DIR) --parallel

native-test: native-build
	$(CTEST) --test-dir $(NATIVE_BUILD_DIR) --output-on-failure

api-docs:
	$(GO) run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION) init --dir cmd/Zhulong,internal/app,internal/httputil --generalInfo main.go --output internal/apidocs --parseInternal

go-check: native-build
	test -z "$$(gofmt -l cmd internal)"
	$(GO) vet ./cmd/... ./internal/...
	CGO_ENABLED=1 $(GO) test -race ./cmd/... ./internal/...

check:
	$(MAKE) frontend-check
	$(MAKE) native-test
	$(MAKE) api-docs
	$(MAKE) go-check

smoke: build
	bash scripts/smoke.sh $(BINARY)

run: build
	./$(BINARY)

web-dev:
	$(NPM) run dev --prefix web
