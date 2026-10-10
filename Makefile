GO ?= go
NPM ?= npm
CMAKE ?= cmake
CTEST ?= ctest
BUILD_DIR ?= build
NATIVE_BUILD := python3 native/scripts/build.py
BINARY := $(BUILD_DIR)/Zhulong
SWAG_VERSION := v1.16.6
AIR ?= $(shell command -v air 2>/dev/null || (test -x "$$(go env GOPATH 2>/dev/null)/bin/air" && echo "$$(go env GOPATH)/bin/air") || echo air)

.PHONY: all build go-build check frontend-install frontend-build frontend-check native-deps native-configure native-build native-test native-cross-build api-docs go-check smoke run web-dev dev-backend dev-frontend dev

all: build

build: go-build

go-build: frontend-build native-build api-docs
	mkdir -p $(BUILD_DIR)
	$(NATIVE_BUILD) go build -o $(BINARY) ./cmd/Zhulong

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

# 准备 Native 外部依赖源码（唯一允许发起外网下载的构建目标）
native-deps:
	$(NATIVE_BUILD) prepare --download

# 生成与校验 Native CMake 构建环境（离线）
native-configure:
	$(NATIVE_BUILD) configure

# 编译 Native C++ 引擎静态库与测试目标
native-build:
	$(NATIVE_BUILD) build

# 执行 Native CTest 单元测试与集成测试套件
native-test:
	$(NATIVE_BUILD) test

# 交叉编译目标二进制（严格要求指定目标板 Profile 与输出路径，防 Host/Cross 产物混淆）
native-cross-build:
	test -n "$(CROSS_PROFILE)" && test -n "$(CROSS_OUTPUT)"
	$(NATIVE_BUILD) --profile "$(CROSS_PROFILE)" go build -ldflags=-linkmode=external -o "$(CROSS_OUTPUT)" ./cmd/Zhulong

api-docs:
	$(GO) run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION) init --dir cmd/Zhulong,internal/app,internal/httputil,internal/auth,internal/audit,internal/camera,internal/network,internal/systemtime,internal/storage --generalInfo main.go --output internal/apidocs --parseInternal

go-check: native-build
	test -z "$$(gofmt -l cmd internal)"
	$(NATIVE_BUILD) go vet ./cmd/... ./internal/...
	$(NATIVE_BUILD) go test -race ./cmd/... ./internal/...
	python3 native/tests/run_go_bridge_tests.py

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

dev-frontend: web-dev

dev-backend: native-build
	@which $(AIR) > /dev/null 2>&1 || (echo "air is not installed. Run: go install github.com/air-verse/air@latest" && exit 1)
	$(AIR)

dev: native-build
	@which $(AIR) > /dev/null 2>&1 || (echo "air is not installed. Run: go install github.com/air-verse/air@latest" && exit 1)
	@echo "Starting Zhulong full-stack dev (Air + Vite)..."
	@bash -c '\
		cleanup() { \
			kill $$P1 $$P2 2>/dev/null || true; \
			pkill -P $$P2 2>/dev/null || true; \
			pkill -P $$P1 2>/dev/null || true; \
		}; \
		trap cleanup INT TERM EXIT; \
		$(AIR) & P1=$$!; \
		$(NPM) run dev --prefix web & P2=$$!; \
		while kill -0 $$P1 2>/dev/null && kill -0 $$P2 2>/dev/null; do \
			sleep 1 & wait $$!; \
		done'


