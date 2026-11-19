# PPE 安全帽穿戴检测算法包工程 Makefile
# 支持在包目录内独立编译、自测，以及组装两档 .tar.gz

TARGET ?= onnx
SDK_PREFIX ?=
TOOLCHAIN ?=
RKNN_ROOT ?=
RGA_ROOT ?=
EXTRA_CMAKE_ARGS ?=

MAKE_ARGS := $(if $(SDK_PREFIX),SDK_PREFIX=$(SDK_PREFIX),) \
             $(if $(TOOLCHAIN),TOOLCHAIN=$(TOOLCHAIN),) \
             $(if $(RKNN_ROOT),RKNN_ROOT=$(RKNN_ROOT),) \
             $(if $(RGA_ROOT),RGA_ROOT=$(RGA_ROOT),) \
             $(if $(EXTRA_CMAKE_ARGS),EXTRA_CMAKE_ARGS=$(EXTRA_CMAKE_ARGS),)

PACKAGE_ARGS := --target $(TARGET) \
	$(if $(SDK_PREFIX),--sdk-prefix "$(SDK_PREFIX)",) \
	$(if $(TOOLCHAIN),--toolchain "$(TOOLCHAIN)",) \
	$(if $(RKNN_ROOT),--rknn-root "$(RKNN_ROOT)",) \
	$(if $(RGA_ROOT),--rga-root "$(RGA_ROOT)",)

.PHONY: all build test unit-test verify package pkg clean onnx rk3568 pkg-onnx pkg-rk3568

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
	python3 scripts/build_package.py $(PACKAGE_ARGS)

pkg: package

onnx:
	$(MAKE) -C onnx $(MAKE_ARGS)

rk3568:
	$(MAKE) -C rk3568 $(MAKE_ARGS)

pkg-onnx:
	$(MAKE) package TARGET=onnx

pkg-rk3568:
	$(MAKE) package TARGET=rk3568

clean:
	rm -rf onnx/build rk3568/build build
