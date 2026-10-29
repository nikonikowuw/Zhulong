# 人脸识别算法包（rk3568 档位）开发期命令
#
#   make test      读包根 sample.jpg → 跑本 target 产品代码 → build/out/result-*.jpg
#   make verify    包根可执行文件的一次性验包（骨架只解 netpbm，jpg 会报 sample_decoded=false）
#
# ⚠️ 只依赖已安装的 SDK 前缀；把整个档位目录拷到任意位置都能跑：
#     make test SDK_PREFIX=<prefix>
SDK_PREFIX ?= /opt/zhulong-sdk
# 交叉构建用：`TOOLCHAIN=<cmake 工具链文件>`。宿主与板端工程用的是同一份 CMakeLists，
# 差别只在工具链；不传就是本机原生构建。
TOOLCHAIN ?=
# 厂商运行库前缀（交叉构建时用；本机原生构建一般由系统 ldconfig 提供）
RKNN_ROOT ?=
ONNXRUNTIME_ROOT ?=
EXTRA_CMAKE_ARGS ?=

BUILD_DIR ?= $(CURDIR)/build
OUT_DIR := $(BUILD_DIR)/out
DEVTEST := $(BUILD_DIR)/face_recognition_devtest
RESULT := $(OUT_DIR)/result-face_recognition-rk3568.jpg
EXE := $(BUILD_DIR)/face_recognition
VERIFY_SAMPLE ?= $(CURDIR)/sample.jpg

SRC_FILES := $(wildcard src/*.cpp src/*.hpp src/*/*.cpp src/*/*.hpp)

.PHONY: all test verify clean

all: $(EXE) $(DEVTEST)

$(BUILD_DIR)/CMakeCache.txt: CMakeLists.txt
	cmake -S $(CURDIR) -B $(BUILD_DIR) \
		-DCMAKE_BUILD_TYPE=RelWithDebInfo \
		-DCMAKE_PREFIX_PATH=$(SDK_PREFIX) \
		-DZhulongAlgoSDK_DIR=$(SDK_PREFIX)/lib/cmake/ZhulongAlgoSDK \
		$(if $(TOOLCHAIN),-DCMAKE_TOOLCHAIN_FILE=$(TOOLCHAIN),) \
		$(if $(RKNN_ROOT),-DRKNN_ROOT=$(RKNN_ROOT),) \
		$(if $(ONNXRUNTIME_ROOT),-DONNXRUNTIME_ROOT=$(ONNXRUNTIME_ROOT),) \
		$(EXTRA_CMAKE_ARGS)

$(EXE): $(BUILD_DIR)/CMakeCache.txt $(SRC_FILES)
	cmake --build $(BUILD_DIR) --target face_recognition

$(DEVTEST): $(BUILD_DIR)/CMakeCache.txt tests/dev_infer.cpp $(SRC_FILES)
	cmake --build $(BUILD_DIR) --target face_recognition_devtest

verify: $(EXE)
	@cd $(CURDIR) && $(EXE) --verify --sample $(VERIFY_SAMPLE)

test: $(DEVTEST)
	@mkdir -p $(OUT_DIR)
	@# 模型在包根 models/，而二进制在 build/：用包作者也会用的 env 覆盖指明包根。
	@ZHULONG_FACE_MODEL_DIR=$(CURDIR) \
		$(DEVTEST) --sample $(CURDIR)/sample.jpg --out $(RESULT)
	@test -s $(RESULT) || (echo "错误: 没有生成 $(RESULT)" && exit 1)
	@echo "[✓] $(RESULT)"

clean:
	rm -rf $(BUILD_DIR)
