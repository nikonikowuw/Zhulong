# 算法包 target 的开发期命令
#
#   make test      读包根 sample.jpg → 跑本 target 产品代码 → build/out/result-*.jpg
#   make clean
#
# ⚠️ 本工程**只依赖已安装的 SDK 前缀**，不引用 Zhulong 仓库里的任何路径：
#   - SDK 前缀由仓库侧 `make sdk-install` 产出（默认 build/sdk/prefix）；
#   - 把整个档位目录拷到任意位置都能 `make test SDK_PREFIX=<prefix>` 跑通。
SDK_PREFIX ?= /opt/zhulong-sdk

BUILD_DIR ?= $(CURDIR)/build
OUT_DIR := $(BUILD_DIR)/out
DEVTEST := $(BUILD_DIR)/plate_recognition_devtest
RESULT := $(OUT_DIR)/result-plate_recognition-onnx.jpg

.PHONY: all test clean

all: $(DEVTEST)

$(DEVTEST): CMakeLists.txt tests/dev_infer.cpp $(wildcard src/*.cpp src/*.hpp)
	cmake -S $(CURDIR) -B $(BUILD_DIR) \
		-DCMAKE_BUILD_TYPE=RelWithDebInfo \
		-DCMAKE_PREFIX_PATH=$(SDK_PREFIX)
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
