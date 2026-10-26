# 算法包 target 的开发期命令（阶段 3.0e / 3.3）
#
#   make test      读包根 sample.jpg → 跑本 target 产品代码 → build/out/result-*.jpg
#   make clean
#
# ⚠️ 这里的 test 只是开发期自测（画一张图 + 打明细表），不是交付物；正式打包用
#    packages/<id>/scripts/build_package.py。SDK 安装（3.2b）落地后，下面的默认路径
#    会收敛为单一的 SDK_PREFIX。
REPO_ROOT ?= $(abspath $(CURDIR)/../../..)
SDK_INCLUDE ?= $(REPO_ROOT)/native/sdk/include
STB_DIR ?= $(REPO_ROOT)/native/sdk/tests/third_party/stb
ALGO_RUNTIME ?= $(REPO_ROOT)/build/native/host/libZhulongAlgoRuntime.a
ONNX_ROOT ?= $(REPO_ROOT)/build/deps/onnxruntime

BUILD_DIR ?= $(CURDIR)/build
OUT_DIR := $(BUILD_DIR)/out
DEVTEST := $(BUILD_DIR)/plate_recognition_devtest
RESULT := $(OUT_DIR)/result-plate_recognition-onnx.jpg

.PHONY: all test clean

all: $(DEVTEST)

$(DEVTEST): CMakeLists.txt tests/dev_infer.cpp $(wildcard src/*.cpp src/*.hpp)
	cmake -S $(CURDIR) -B $(BUILD_DIR) \
		-DCMAKE_BUILD_TYPE=RelWithDebInfo \
		-DZHULONG_SDK_INCLUDE=$(SDK_INCLUDE) \
		-DZHULONG_STB_DIR=$(STB_DIR) \
		-DZHULONG_ALGO_RUNTIME=$(ALGO_RUNTIME) \
		-DZHULONG_ONNX_ROOT=$(ONNX_ROOT)
	cmake --build $(BUILD_DIR) --target plate_recognition_devtest

test: $(DEVTEST)
	@mkdir -p $(OUT_DIR)
	@# 模型在包根 models/，而二进制在 build/：用包作者也会用的 env 覆盖指明包根。
	@ZHULONG_PLATE_MODEL_DIR=$(CURDIR) \
		$(DEVTEST) --sample $(CURDIR)/sample.jpg --out $(RESULT)
	@test -s $(RESULT) || (echo "错误: 没有生成 $(RESULT)" && exit 1)
	@echo "[✓] $(RESULT)"

clean:
	rm -rf $(BUILD_DIR)
