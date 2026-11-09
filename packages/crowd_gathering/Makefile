# 人员非法聚集检测算法包工程 Makefile
# 支持在算法包目录内独立编译、运行自测及组装打包

TARGET ?= onnx
SDK_PREFIX ?=
TOOLCHAIN ?=
RKNN_ROOT ?=
ONNXRUNTIME_ROOT ?=
EXTRA_CMAKE_ARGS ?=

MAKE_ARGS := $(if $(SDK_PREFIX),SDK_PREFIX=$(SDK_PREFIX),) \
             $(if $(TOOLCHAIN),TOOLCHAIN=$(TOOLCHAIN),) \
             $(if $(RKNN_ROOT),RKNN_ROOT=$(RKNN_ROOT),) \
             $(if $(ONNXRUNTIME_ROOT),ONNXRUNTIME_ROOT=$(ONNXRUNTIME_ROOT),) \
             $(if $(EXTRA_CMAKE_ARGS),EXTRA_CMAKE_ARGS=$(EXTRA_CMAKE_ARGS),)

.PHONY: all build test unit-test verify package pkg clean onnx pkg-onnx

all: build

build:
	$(MAKE) -C $(TARGET) $(MAKE_ARGS)

test:
	$(MAKE) -C $(TARGET) test $(MAKE_ARGS)

unit-test:
	$(MAKE) -C $(TARGET) unit-test $(MAKE_ARGS)

verify:
	$(MAKE) -C $(TARGET) verify $(MAKE_ARGS)

package:
	python3 scripts/build_package.py --target $(TARGET) \
		$(if $(SDK_PREFIX),--sdk-prefix "$(SDK_PREFIX)",) \
		$(if $(TOOLCHAIN),--toolchain "$(TOOLCHAIN)",) \
		$(if $(RKNN_ROOT),--rknn-root "$(RKNN_ROOT)",) \
		$(if $(ONNXRUNTIME_ROOT),--onnxruntime-root "$(ONNXRUNTIME_ROOT)",)

pkg: package

onnx:
	$(MAKE) -C onnx $(MAKE_ARGS)

pkg-onnx:
	$(MAKE) package TARGET=onnx

clean:
	rm -rf onnx/build build
