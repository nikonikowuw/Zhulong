# 人员非法聚集检测算法包（rk3568 档位）开发期命令
#
#   make            编译包根可执行文件 + devtest + 单测（交叉构建时只编译）
#   make unit-test  纯逻辑自测（配置解析 / 聚类几何 / 时序防抖），不需要模型
#   make test       读包根 sample.jpg → 跑产品代码 → build/out/result-*.jpg（须在 aarch64 上）
#   make clean
#
# 本档位的权重是 RKNN 静态形状，可在 x86 宿主交叉编译，但**跑不了**：单测与 devtest 都是
# aarch64 二进制。交叉构建入口见包根 Makefile 的 `make pkg TARGET=rk3568`（会调
# scripts/build_package.py 找好 SDK 前缀与工具链），或手动：
#
#   make TOOLCHAIN=$PWD/../../../build/native/cross-rk3568-rknn-rga/toolchain.cmake \
#        SDK_PREFIX=$PWD/../../../build/sdk/rk3568 \
#        RKNN_ROOT=$PWD/../../../native/vendor/rknpu2 RGA_ROOT=$PWD/../../../native/vendor/rga
SDK_PREFIX ?= /opt/zhulong-sdk
# 交叉构建用：`TOOLCHAIN=<cmake 工具链文件>`。宿主与板端工程用的是同一份 CMakeLists，
# 差别只在工具链；不传就是本机原生构建（只能在 aarch64 上跑）。
TOOLCHAIN ?=
# 厂商运行库前缀（交叉构建时用；本机原生构建一般由系统 ldconfig 提供）
RKNN_ROOT ?=
# RGA 前缀：本仓库的 rk3568 SDK 前缀（backend=rknn vision=rga）会把 librga.so.2 传给消费方，
# 交叉链接必须能解析到它（native/vendor/rga）。vision=cpu 的前缀不需要。
RGA_ROOT ?=
EXTRA_CMAKE_ARGS ?=

BUILD_DIR ?= $(CURDIR)/build
OUT_DIR := $(BUILD_DIR)/out
DEVTEST := $(BUILD_DIR)/crowd_gathering_devtest
UNITTEST := $(BUILD_DIR)/crowd_gathering_unittest
RESULT := $(OUT_DIR)/result-crowd_gathering-rk3568.jpg
# 包根可执行文件名必须等于 manifest.id（宿主按 id 找入口）
EXE := $(BUILD_DIR)/crowd_gathering
# verify 的样本帧：骨架只解码 netpbm，jpg 会如实报 sample_decoded=false
VERIFY_SAMPLE ?= $(CURDIR)/sample.jpg

SRC_FILES := $(wildcard src/*.cpp src/*.hpp src/*/*.cpp src/*/*.hpp)

# 配置参数指纹：切 SDK 前缀 / 工具链 / 厂商前缀时必须重跑 cmake。
# ⚠️ 只依赖 CMakeLists.txt 的 mtime，会让上一次的 -DCMAKE_TOOLCHAIN_FILE / -DRKNN_ROOT 静默
#    沿用；本档位「宿主构建」与「交叉构建」共用同一个 build/ 目录，这个坑几乎必然踩到，
#    症状是换了工具链编出来的还是 x86 二进制（或反之），而 make 一直说 up to date。
CONFIGURE_ARGS := SDK_PREFIX=$(SDK_PREFIX)|TOOLCHAIN=$(TOOLCHAIN)|RKNN_ROOT=$(RKNN_ROOT)|RGA_ROOT=$(RGA_ROOT)|EXTRA=$(EXTRA_CMAKE_ARGS)
CONFIGURE_STAMP := $(BUILD_DIR)/.configure-args

HOST_ARCH := $(shell uname -m)

# ⚠️ 规则必须放在 `all:` **之后**：Make 的默认目标是文件里第一个普通目标，
#    把 FORCE 写在前面会让 `make` 什么都不做。
.PHONY: all test unit-test verify clean check-native FORCE

all: $(EXE) $(DEVTEST) $(UNITTEST)

FORCE:

# 只在参数真的变了时重写 stamp，避免无谓的全量重配。
$(CONFIGURE_STAMP): FORCE
	@mkdir -p $(BUILD_DIR)
	@printf '%s\n' '$(CONFIGURE_ARGS)' | cmp -s - $@ \
		|| printf '%s\n' '$(CONFIGURE_ARGS)' > $@

$(BUILD_DIR)/.configured: $(CONFIGURE_STAMP) CMakeLists.txt
	@test -f "$(SDK_PREFIX)/lib/cmake/ZhulongAlgoSDK/ZhulongAlgoSDKConfig.cmake" || { \
		echo "错误: SDK 前缀不可用: $(SDK_PREFIX)"; \
		echo "  缺少 $(SDK_PREFIX)/lib/cmake/ZhulongAlgoSDK/ZhulongAlgoSDKConfig.cmake"; \
		echo "  在 Zhulong 仓库根目录跑 make sdk-cross-install（产出 build/sdk/rk3568），"; \
		echo "  或用 make <target> SDK_PREFIX=<prefix> 指定已有前缀。"; \
		exit 2; \
	}
	@rm -f $(BUILD_DIR)/.configured
	cmake -S $(CURDIR) -B $(BUILD_DIR) \
		-DCMAKE_BUILD_TYPE=RelWithDebInfo \
		-DCMAKE_PREFIX_PATH=$(SDK_PREFIX) \
		-DZhulongAlgoSDK_DIR=$(SDK_PREFIX)/lib/cmake/ZhulongAlgoSDK \
		$(if $(TOOLCHAIN),-DCMAKE_TOOLCHAIN_FILE=$(TOOLCHAIN),) \
		$(if $(RKNN_ROOT),-DRKNN_ROOT=$(RKNN_ROOT),) \
		$(if $(RGA_ROOT),-DRGA_ROOT=$(RGA_ROOT),) \
		$(EXTRA_CMAKE_ARGS)
	@touch $(BUILD_DIR)/.configured

# ⚠️ 三个构建目标都挂 FORCE：`$(SRC_FILES)` 只覆盖包自己，**看不到 SDK 前缀里的头文件**。
#    `algo.hpp` / `plugin.h` 升级后 make 会认为二进制是最新的而静默跳过重建。
#    头文件依赖交给 CMake 自己的 depfile 判定，这里只保证每次都真的问一次 CMake。
$(EXE): $(BUILD_DIR)/.configured $(SRC_FILES) FORCE
	cmake --build $(BUILD_DIR) --target crowd_gathering

$(DEVTEST): $(BUILD_DIR)/.configured tests/dev_infer.cpp $(SRC_FILES) FORCE
	cmake --build $(BUILD_DIR) --target crowd_gathering_devtest

$(UNITTEST): $(BUILD_DIR)/.configured tests/unit_test.cpp $(SRC_FILES) FORCE
	cmake --build $(BUILD_DIR) --target crowd_gathering_unittest

# 单测与 devtest 都是 aarch64 二进制：在 x86 宿主上只能编译，不能在宿主跑。
# 与其让用户看到 `Exec format error`，不如直接说清楚该在哪跑。
check-native:
	@case "$(HOST_ARCH)" in \
		aarch64|arm64) ;; \
		*) echo "错误: 本档位二进制是 aarch64（RK3568），当前宿主是 $(HOST_ARCH)"; \
		   echo "  交叉构建用：make（只编译）+ 包根 make pkg TARGET=rk3568"; \
		   echo "  要在设备上跑：把包拷到板端，或 ssh 到 RK3568（/opt/zhulong）后执行。"; \
		   exit 2 ;; \
	esac

unit-test: check-native $(UNITTEST)
	@$(UNITTEST)

verify: $(EXE)
	@cd $(CURDIR) && $(EXE) --verify --sample $(VERIFY_SAMPLE)

test: unit-test $(DEVTEST)
	@mkdir -p $(OUT_DIR)
	@ZHULONG_CROWD_MODEL_DIR=$(CURDIR) \
		$(DEVTEST) --sample $(CURDIR)/sample.jpg --out $(RESULT)
	@test -s $(RESULT) || (echo "错误: 没有生成 $(RESULT)" && exit 1)
	@echo "[✓] $(RESULT)"

clean:
	rm -rf $(BUILD_DIR)
