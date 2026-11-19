# PPE 安全帽穿戴检测算法包（rk3568 档位）开发期命令
#
#   make test      包内自测（不需要模型）+ 读包根 sample.jpg 跑产品代码 → build/out/result-*.jpg
#   make unit-test 只跑不需要模型的自测（解码 / 关联 / 时序）
#   make verify    包根可执行文件的一次性验包（骨架只解 netpbm，jpg 会报 sample_decoded=false）
#
# ⚠️ 只依赖已安装的 SDK 前缀；把整个档位目录拷到任意位置都能跑：
#     make test SDK_PREFIX=<prefix>
SDK_PREFIX ?= /opt/zhulong-sdk
TOOLCHAIN ?=
RKNN_ROOT ?=
RGA_ROOT ?=
ONNXRUNTIME_ROOT ?=
EXTRA_CMAKE_ARGS ?=

BUILD_DIR ?= $(CURDIR)/build
OUT_DIR := $(BUILD_DIR)/out
EXE := $(BUILD_DIR)/ppe_helmet
DEVTEST := $(BUILD_DIR)/ppe_helmet_devtest
UNITTEST := $(BUILD_DIR)/ppe_helmet_unittest
VERIFY_SAMPLE ?= $(CURDIR)/sample.jpg
RESULT := $(OUT_DIR)/result-ppe_helmet-rk3568.jpg

CMAKE ?= cmake

SRC_FILES := $(wildcard src/*.cpp src/*.hpp src/*/*.cpp src/*/*.hpp)

# 配置参数指纹：切档位/SDK 前缀/厂商前缀时必须重跑 cmake。
# 只依赖 CMakeLists.txt 的 mtime，会让上一次的 -DCMAKE_TOOLCHAIN_FILE / -DRGA_ROOT 静默沿用。
CONFIGURE_ARGS := SDK_PREFIX=$(SDK_PREFIX)|TOOLCHAIN=$(TOOLCHAIN)|RKNN_ROOT=$(RKNN_ROOT)|RGA_ROOT=$(RGA_ROOT)|ONNXRUNTIME_ROOT=$(ONNXRUNTIME_ROOT)|EXTRA=$(EXTRA_CMAKE_ARGS)
CONFIGURE_STAMP := $(BUILD_DIR)/.configure-args

.PHONY: all test unit-test verify clean FORCE

all: $(EXE)

FORCE:

# 只在参数真的变了时重写 stamp，避免无谓的全量重配。
$(CONFIGURE_STAMP): FORCE
	@mkdir -p $(BUILD_DIR)
	@printf '%s\n' '$(CONFIGURE_ARGS)' | cmp -s - $@ \
		|| printf '%s\n' '$(CONFIGURE_ARGS)' > $@

$(BUILD_DIR)/CMakeCache.txt: $(CONFIGURE_STAMP) CMakeLists.txt
	@if [ ! -f "$(SDK_PREFIX)/lib/cmake/ZhulongAlgoSDK/ZhulongAlgoSDKConfig.cmake" ]; then \
		echo "[错误] 未找到 ZhulongAlgoSDK: $(SDK_PREFIX)"; \
		echo "       先执行 make sdk-cross-install，或用 SDK_PREFIX=<前缀> 指定"; \
		exit 2; \
	fi
	$(CMAKE) -S $(CURDIR) -B $(BUILD_DIR) \
		-DCMAKE_BUILD_TYPE=RelWithDebInfo \
		-DCMAKE_PREFIX_PATH=$(SDK_PREFIX) \
		-DZhulongAlgoSDK_DIR=$(SDK_PREFIX)/lib/cmake/ZhulongAlgoSDK \
		$(if $(TOOLCHAIN),-DCMAKE_TOOLCHAIN_FILE=$(TOOLCHAIN),) \
		$(if $(RKNN_ROOT),-DRKNN_ROOT=$(RKNN_ROOT),) \
		$(if $(RGA_ROOT),-DRGA_ROOT=$(RGA_ROOT),) \
		$(if $(ONNXRUNTIME_ROOT),-DONNXRUNTIME_ROOT=$(ONNXRUNTIME_ROOT),) \
		$(EXTRA_CMAKE_ARGS)

# 三个构建目标都挂 FORCE：SRC_FILES 看不到 SDK 前缀里的头文件，头文件升级后
# make 会认为二进制是最新的而静默跳过重建；依赖判定交给 CMake 自己的 depfile。
$(EXE): $(BUILD_DIR)/CMakeCache.txt $(SRC_FILES) FORCE
	$(CMAKE) --build $(BUILD_DIR) --target ppe_helmet -j

$(UNITTEST): $(BUILD_DIR)/CMakeCache.txt tests/unit_test.cpp $(SRC_FILES) FORCE
	$(CMAKE) --build $(BUILD_DIR) --target ppe_helmet_unittest -j

$(DEVTEST): $(BUILD_DIR)/CMakeCache.txt tests/dev_infer.cpp $(SRC_FILES) FORCE
	$(CMAKE) --build $(BUILD_DIR) --target ppe_helmet_devtest -j

unit-test: $(UNITTEST)
	@$(UNITTEST)

test: unit-test $(DEVTEST)
	@mkdir -p $(OUT_DIR)
	@ZHULONG_PPE_MODEL_DIR=$(CURDIR) \
		$(DEVTEST) --sample $(CURDIR)/sample.jpg --out $(RESULT)
	@test -s $(RESULT) || (echo "错误: 没有生成 $(RESULT)" && exit 1)
	@echo "[✓] $(RESULT)"

verify: $(EXE)
	@cd $(CURDIR) && $(EXE) --verify --sample $(VERIFY_SAMPLE)

clean:
	rm -rf $(BUILD_DIR)
