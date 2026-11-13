# 人脸识别算法包（rk3568 档位）开发期命令
#
#   make test      包内自测（不需要模型）+ 读包根 sample.jpg 跑产品代码 → build/out/result-*.jpg
#   make unit-test 只跑不需要模型的自测（几何对齐 / 1:N 比对）
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
# RGA 前缀（视觉前处理走 rga 档位的 SDK 前缀才需要；否则留空）
RGA_ROOT ?=
ONNXRUNTIME_ROOT ?=
EXTRA_CMAKE_ARGS ?=

BUILD_DIR ?= $(CURDIR)/build
OUT_DIR := $(BUILD_DIR)/out
DEVTEST := $(BUILD_DIR)/face_recognition_devtest
RESULT := $(OUT_DIR)/result-face_recognition-rk3568.jpg
EXE := $(BUILD_DIR)/face_recognition
UNITTEST := $(BUILD_DIR)/face_recognition_unittest
VERIFY_SAMPLE ?= $(CURDIR)/sample.jpg

SRC_FILES := $(wildcard src/*.cpp src/*.hpp src/*/*.cpp src/*/*.hpp)

# 配置参数指纹：切档位/SDK 前缀/厂商前缀时必须重跑 cmake。
# ⚠️ 只依赖 CMakeLists.txt 的 mtime，会让上一次的 -DCMAKE_TOOLCHAIN_FILE / -DRGA_ROOT 静默沿用；
#    症状是「换了档位编出来的包还是旧后端」，rga 前缀时直接报找不到 librga.so.2。
CONFIGURE_ARGS := SDK_PREFIX=$(SDK_PREFIX)|TOOLCHAIN=$(TOOLCHAIN)|RKNN_ROOT=$(RKNN_ROOT)|RGA_ROOT=$(RGA_ROOT)|ONNXRUNTIME_ROOT=$(ONNXRUNTIME_ROOT)|EXTRA=$(EXTRA_CMAKE_ARGS)
CONFIGURE_STAMP := $(BUILD_DIR)/.configure-args

# ⚠️ 规则必须放在 `all:` **之后**：Make 的默认目标是文件里第一个普通目标，
#    把 FORCE 写在前面会让 `make` 什么都不做（只打印 Nothing to be done for 'FORCE'）。
.PHONY: all test unit-test verify clean FORCE

all: $(EXE) $(DEVTEST) $(UNITTEST)

FORCE:

# 只在参数真的变了时重写 stamp，避免无谓的全量重配。
$(CONFIGURE_STAMP): FORCE
	@mkdir -p $(BUILD_DIR)
	@printf '%s\n' '$(CONFIGURE_ARGS)' | cmp -s - $@ \
		|| printf '%s\n' '$(CONFIGURE_ARGS)' > $@

$(BUILD_DIR)/CMakeCache.txt: $(CONFIGURE_STAMP) CMakeLists.txt
	cmake -S $(CURDIR) -B $(BUILD_DIR) \
		-DCMAKE_BUILD_TYPE=RelWithDebInfo \
		-DCMAKE_PREFIX_PATH=$(SDK_PREFIX) \
		-DZhulongAlgoSDK_DIR=$(SDK_PREFIX)/lib/cmake/ZhulongAlgoSDK \
		$(if $(TOOLCHAIN),-DCMAKE_TOOLCHAIN_FILE=$(TOOLCHAIN),) \
		$(if $(RKNN_ROOT),-DRKNN_ROOT=$(RKNN_ROOT),) \
		$(if $(RGA_ROOT),-DRGA_ROOT=$(RGA_ROOT),) \
		$(if $(ONNXRUNTIME_ROOT),-DONNXRUNTIME_ROOT=$(ONNXRUNTIME_ROOT),) \
		$(EXTRA_CMAKE_ARGS)

# ⚠️ 三个构建目标都挂 FORCE：`$(SRC_FILES)` 只覆盖包自己，**看不到 SDK 前缀里的头文件**。
#    `algo.hpp` / `plugin.h` 升级后 make 会认为二进制是最新的而静默跳过重建，
#    `make verify` 于是验的是上一个 ABI 的老二进制（表现为新字段全空却 status=passed）。
#    头文件依赖交给 CMake 自己的 depfile 判定，这里只保证每次都真的问一次 CMake。
$(EXE): $(BUILD_DIR)/CMakeCache.txt $(SRC_FILES) FORCE
	cmake --build $(BUILD_DIR) --target face_recognition

$(DEVTEST): $(BUILD_DIR)/CMakeCache.txt tests/dev_infer.cpp $(SRC_FILES) FORCE
	cmake --build $(BUILD_DIR) --target face_recognition_devtest

$(UNITTEST): $(BUILD_DIR)/CMakeCache.txt tests/aligner_matcher_test.cpp $(SRC_FILES) FORCE
	cmake --build $(BUILD_DIR) --target face_recognition_unittest

verify: $(EXE)
	@cd $(CURDIR) && $(EXE) --verify --sample $(VERIFY_SAMPLE)

# 不需要模型的那部分先跑：它失败时不该等到 devtest 出图才发现。
unit-test: $(UNITTEST)
	@$(UNITTEST)

test: unit-test $(DEVTEST)
	@mkdir -p $(OUT_DIR)
	@# 模型在包根 models/，而二进制在 build/：用包作者也会用的 env 覆盖指明包根。
	@ZHULONG_FACE_MODEL_DIR=$(CURDIR) \
		$(DEVTEST) --sample $(CURDIR)/sample.jpg --out $(RESULT) --check
	@test -s $(RESULT) || (echo "错误: 没有生成 $(RESULT)" && exit 1)
	@echo "[✓] $(RESULT)"

clean:
	rm -rf $(BUILD_DIR)
