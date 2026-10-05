#!/bin/sh
# Zhulong 封闭依赖隔离脚本（针对自包含的静态 FFmpeg 编译）
#
# 设计目的：
#   FFmpeg 的 configure 脚本在禁用自动探测 (--disable-autodetect) 的情况下，
#   仍会尝试调用 pkg-config --version 探测包管理工具。
#   为防止构建脚本意外回落并查询宿主机系统的 pkg-config 数据库（导致混入宿主机未审计的动态库），
#   此处提供一个封闭空的解析器：仅响应 --version 校验，其余所有外部包查询均以非 0 状态直接失败退出（Fail-closed）。
if [ "$#" -eq 1 ] && [ "$1" = "--version" ]; then
    printf '%s\n' 'Zhulong empty package resolver 1'
    exit 0
fi
exit 1
