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
# 包根可执行文件名必须等于 manifest.id（宿主按 id 找入口）
EXE := $(BUILD_DIR)/plate_recognition
# verify 的样本帧：骨架只解码 netpbm，jpg 会如实报 sample_decoded=false
VERIFY_SAMPLE ?= $(CURDIR)/sample.jpg

.PHONY: all test verify clean

all: $(EXE) $(DEVTEST)

$(BUILD_DIR)/CMakeCache.txt: CMakeLists.txt
	cmake -S $(CURDIR) -B $(BUILD_DIR) \
		-DCMAKE_BUILD_TYPE=RelWithDebInfo \
		-DCMAKE_PREFIX_PATH=$(SDK_PREFIX)

$(EXE): $(BUILD_DIR)/CMakeCache.txt $(wildcard src/*.cpp src/*.hpp)
	cmake --build $(BUILD_DIR) --target plate_recognition

$(DEVTEST): $(BUILD_DIR)/CMakeCache.txt tests/dev_infer.cpp $(wildcard src/*.cpp src/*.hpp)
	cmake --build $(BUILD_DIR) --target plate_recognition_devtest

# 包根可执行文件的一次性验包（与宿主验包门禁同构：stage token + 载荷 JSON）
verify: $(EXE)
	@cd $(CURDIR) && $(EXE) --verify --sample $(VERIFY_SAMPLE)

test: $(DEVTEST)
	@mkdir -p $(OUT_DIR)
	@# 模型在包根 models/，而二进制在 build/：用包作者也会用的 env 覆盖指明包根。
	@ZHULONG_PLATE_MODEL_DIR=$(CURDIR) \
		$(DEVTEST) --sample $(CURDIR)/sample.jpg --out $(RESULT)
	@test -s $(RESULT) || (echo "错误: 没有生成 $(RESULT)" && exit 1)
	@echo "[✓] $(RESULT)"

clean:
	rm -rf $(BUILD_DIR)
