# 算法包 target 的开发期命令
#
#   make unit-test 只跑不需要模型的自测（配置解析 / 聚类几何 / 时序防抖）
#   make test      读包根 sample.jpg → 跑本 target 产品代码 → build/out/result-*.jpg
#   make clean
#
# 未装前缀时 make 会先给出可行动的报错，而不是把 find_package 的原始输出甩给你：
#   make sdk-install                       # 在 Zhulong 仓库根目录生成前缀
#   make test SDK_PREFIX=<prefix>          # 或用已有前缀
#
# ⚠️ 本工程只依赖已安装的 SDK 前缀，不引用 Zhulong 仓库里的任何路径。
SDK_PREFIX ?= /opt/zhulong-sdk
TOOLCHAIN ?=
RKNN_ROOT ?=
ONNXRUNTIME_ROOT ?=
EXTRA_CMAKE_ARGS ?=

BUILD_DIR ?= $(CURDIR)/build
# 配置成功后才落这个戳。⚠️ 不要拿 CMakeCache.txt 当完成标记：configure 失败也会留下一个
# 半截 CMakeCache.txt，此后每次 make 都把它当「已配置」，于是跳过 cmake 直接
# `cmake --build`，报的是「No rule to make target 'Makefile'」—— 和真实原因毫无关系。
CONFIGURE_STAMP := $(BUILD_DIR)/.configured
OUT_DIR := $(BUILD_DIR)/out
DEVTEST := $(BUILD_DIR)/crowd_gathering_devtest
UNITTEST := $(BUILD_DIR)/crowd_gathering_unittest
RESULT := $(OUT_DIR)/result-crowd_gathering-onnx.jpg
# 包根可执行文件名必须等于 manifest.id（宿主按 id 找入口）
EXE := $(BUILD_DIR)/crowd_gathering
# verify 的样本帧：骨架只解码 netpbm，jpg 会如实报 sample_decoded=false
VERIFY_SAMPLE ?= $(CURDIR)/sample.jpg

SRC_FILES := $(wildcard src/*.cpp src/*.hpp src/*/*.cpp src/*/*.hpp)

.PHONY: all test unit-test verify clean

all: $(EXE) $(DEVTEST) $(UNITTEST)

$(CONFIGURE_STAMP): CMakeLists.txt
	@test -f "$(SDK_PREFIX)/lib/cmake/ZhulongAlgoSDK/ZhulongAlgoSDKConfig.cmake" || { \
		echo "错误: SDK 前缀不可用: $(SDK_PREFIX)"; \
		echo "  缺少 $(SDK_PREFIX)/lib/cmake/ZhulongAlgoSDK/ZhulongAlgoSDKConfig.cmake"; \
		echo "  在 Zhulong 仓库根目录跑 make sdk-install（前缀默认产出到 build/sdk/prefix），"; \
		echo "  或用 make <target> SDK_PREFIX=<prefix> 指定已有前缀。"; \
		exit 2; \
	}
	@cmake -S $(CURDIR) -B $(BUILD_DIR) \
		-DCMAKE_BUILD_TYPE=RelWithDebInfo \
		-DCMAKE_PREFIX_PATH=$(SDK_PREFIX) \
		-DZhulongAlgoSDK_DIR=$(SDK_PREFIX)/lib/cmake/ZhulongAlgoSDK \
		$(if $(TOOLCHAIN),-DCMAKE_TOOLCHAIN_FILE=$(TOOLCHAIN),) \
		$(if $(RKNN_ROOT),-DRKNN_ROOT=$(RKNN_ROOT),) \
		$(if $(ONNXRUNTIME_ROOT),-DONNXRUNTIME_ROOT=$(ONNXRUNTIME_ROOT),) \
		$(EXTRA_CMAKE_ARGS)
	@touch $(CONFIGURE_STAMP)

$(EXE): $(CONFIGURE_STAMP) $(SRC_FILES)
	cmake --build $(BUILD_DIR) --target crowd_gathering

$(DEVTEST): $(CONFIGURE_STAMP) tests/dev_infer.cpp $(SRC_FILES)
	cmake --build $(BUILD_DIR) --target crowd_gathering_devtest

# 纯逻辑自测：失败即非零退出码（不依赖 assert，也不依赖模型）
$(UNITTEST): $(CONFIGURE_STAMP) tests/unit_test.cpp $(SRC_FILES)
	cmake --build $(BUILD_DIR) --target crowd_gathering_unittest

unit-test: $(UNITTEST)
	@$(UNITTEST)

# 包根可执行文件的一次性验包
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
