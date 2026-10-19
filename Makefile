GO ?= go
# Frontend package manager: the repository ships only pnpm-lock.yaml, so npm
# cannot install it (npm ci requires package-lock.json).
PNPM ?= pnpm
CMAKE ?= cmake
CTEST ?= ctest
BUILD_DIR ?= build
# 推理后端覆盖：cpu | rknn | onnx。留空时 Host 用 cpu、交叉编译沿用 profile JSON 内的
# inference_backend（不要无条件注入，否则会覆盖 rk3568-rknn.json 的 rknn 选择）。
# ⚠️ 必须走这个变量注入：每次 build.py 调用都会重新 configure CMake，而 CMake 缓存里的
#    ZHULONG_INFERENCE_BACKEND 会被无参 configure 重置回 cpu。直接执行一次
#    `build.py --inference-backend onnx build` 之后，任何 `make dev` / `make build` /
#    `make check` 都会把它悄悄改回 cpu 并重链出桩后端。
# ⚠️ Air（.air.toml 的 build cmd）也会独立调用 build.py，所以还要把选择导出成
#    ZHULONG_INFERENCE_BACKEND 让它继承；环境变量是该开关的统一入口。
INFERENCE_BACKEND ?= $(ZHULONG_INFERENCE_BACKEND)
ifneq ($(INFERENCE_BACKEND),)
export ZHULONG_INFERENCE_BACKEND := $(INFERENCE_BACKEND)
endif
NATIVE_BUILD := python3 native/scripts/build.py $(if $(INFERENCE_BACKEND),--inference-backend $(INFERENCE_BACKEND),)
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
	@command -v $(PNPM) > /dev/null 2>&1 || (echo "$(PNPM) is not installed. Run: corepack enable pnpm" && exit 1)
	$(PNPM) --dir web install --frozen-lockfile

frontend-build: frontend-install
	$(PNPM) --dir web run build

# 前端门禁：lint + 类型检查（含在 build 的 tsc -b 中）+ 真实无头浏览器测试 + 生产构建。
# 说明：`type-check` 与 `audit` 不是 web/package.json 里的 script。
#   - type-check → 由 build 承担（其脚本为 `tsc -b && vite build`，类型检查已包含在内）；
#   - audit      → 本目标不执行。生产依赖存在既有安全债（pnpm audit --prod 报告 52 项，
#                  含 axios 的 2 项 critical），作为独立议题跟踪，避免阻断全部前端检查。
frontend-check: frontend-install
	$(PNPM) --dir web run lint
	$(PNPM) --dir web test
	$(PNPM) --dir web run build

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
	$(GO) run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION) init --dir cmd/Zhulong,internal/app,internal/httputil,internal/auth,internal/audit,internal/camera,internal/network,internal/systemtime,internal/storage,internal/algorithm,internal/person,internal/mediafile,internal/record,internal/task,internal/aitypes --generalInfo main.go --output internal/apidocs --parseInternal

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
	$(PNPM) --dir web run dev

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
		$(PNPM) --dir web run dev & P2=$$!; \
		while kill -0 $$P1 2>/dev/null && kill -0 $$P2 2>/dev/null; do \
			sleep 1 & wait $$!; \
		done'


