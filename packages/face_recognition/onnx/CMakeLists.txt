# 人脸识别算法包（onnx 档位）—— 开发期构建
#
# 产出 devtest（读包根 sample.jpg → 调本 target 产品代码 → 写 result.jpg）与
# 包根可执行文件 `face_recognition`（名字必须等于 manifest.id）。
# 依赖只有 SDK 前缀：`find_package(ZhulongAlgoSDK)`，本文件不得出现仓库路径。
cmake_minimum_required(VERSION 3.20)
project(face_recognition_onnx LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

find_package(ZhulongAlgoSDK 1.3 REQUIRED)
find_package(Threads REQUIRED)

# 包版本只认 manifest.json —— 唯一事实源。
#
# `main.cpp` 的 `ZHULONG_ALGO_MAIN(..., "FaceRecognition", <version>)` 会把包版本回显给
# `--verify` 与宿主握手；版本号手写两份的结局是「清单升了、二进制还报旧版本」，而且没有
# 任何门禁比对（Go 侧只校验 manifest 自己的 token）。所以这里在配置期把 version 抠出来
# 编成宏，清单改了重新构建即可，不需要人记得改第二处。
file(READ "${CMAKE_CURRENT_SOURCE_DIR}/manifest.json" ZHULONG_FACE_MANIFEST_JSON)
string(REGEX MATCH "\"version\"[ \t\r\n]*:[ \t\r\n]*\"([^\"]*)\"" _version_match "${ZHULONG_FACE_MANIFEST_JSON}")
set(ZHULONG_FACE_PLUGIN_VERSION "${CMAKE_MATCH_1}")
if(NOT ZHULONG_FACE_PLUGIN_VERSION)
    message(WARNING "manifest.json 中未解析出 version：包版本回退为 0.0.0")
    set(ZHULONG_FACE_PLUGIN_VERSION "0.0.0")
endif()
message(STATUS "face_recognition 包版本（manifest.json）: ${ZHULONG_FACE_PLUGIN_VERSION}")

# 产品代码（算法类本体）：可执行文件与 devtest 共用，避免两份实现。
add_library(face_recognition_core OBJECT
    src/detection/detector.cpp
    src/alignment/aligner.cpp
    src/quality/quality_assessor.cpp
    src/feature/extractor.cpp
    src/matching/matcher.cpp
    src/pipeline/face_recognition.cpp
)
target_compile_definitions(face_recognition_core PRIVATE ZHULONG_PLUGIN_BUILD)
target_include_directories(face_recognition_core PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(face_recognition_core PRIVATE Zhulong::AlgoRuntime)

# 交付物：包根可执行文件名必须等于 manifest.id
add_executable(face_recognition
    src/main.cpp
    $<TARGET_OBJECTS:face_recognition_core>
)
target_include_directories(face_recognition PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_compile_definitions(face_recognition PRIVATE
    ZHULONG_FACE_PLUGIN_VERSION="${ZHULONG_FACE_PLUGIN_VERSION}")
# --exclude-libs 是 GNU ld 专属（Apple ld 报 unknown options）；macOS 开发机跳过即可。
if(NOT APPLE)
    target_link_options(face_recognition PRIVATE "LINKER:--exclude-libs,ALL")
endif()
target_link_libraries(face_recognition PRIVATE Zhulong::AlgoSDK Zhulong::AlgoRuntime)

# 开发期自测（EXCLUDE_FROM_ALL、无安装规则 → 物理上进不了归档）
add_executable(face_recognition_devtest EXCLUDE_FROM_ALL
    tests/dev_infer.cpp
    $<TARGET_OBJECTS:face_recognition_core>
)
target_compile_definitions(face_recognition_devtest PRIVATE ZHULONG_PLUGIN_BUILD)
target_include_directories(face_recognition_devtest PRIVATE
    ${ZhulongAlgoSDK_DEV_DIR}/stb
    ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_link_libraries(face_recognition_devtest PRIVATE Zhulong::AlgoRuntime)

# 包内自测：纯几何 / 比对数学，不加载模型、不需要会话。
# 只依赖 SDK 前缀 + 包自身源码 → 第三方与 sdk-clean-check 的干净容器都能构建运行。
add_executable(face_recognition_unittest EXCLUDE_FROM_ALL
    tests/aligner_matcher_test.cpp
    src/alignment/aligner.cpp
    src/matching/matcher.cpp
)
target_include_directories(face_recognition_unittest PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(face_recognition_unittest PRIVATE Zhulong::AlgoRuntime)
