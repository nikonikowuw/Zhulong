# 开发期构建（**不是**交付构建）
#
# 这里只产出 `tests/dev_infer.cpp` 的自测可执行文件：读包根 sample.jpg → 调本 target
# 产品代码 → 画框 → 写 result.jpg。它 EXCLUDE_FROM_ALL、无安装规则，物理上进不了
# 算法包归档。正式包构建（可执行文件 + SDK）见阶段 3.3。
cmake_minimum_required(VERSION 3.20)
project(plate_recognition_onnx_dev LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# 输入路径一律由 Makefile 传入绝对路径；缺一个就在 configure 阶段失败（fail closed）。
foreach(required_var ZHULONG_SDK_INCLUDE ZHULONG_STB_DIR ZHULONG_ALGO_RUNTIME ZHULONG_ONNX_ROOT)
    if(NOT DEFINED ${required_var})
        message(FATAL_ERROR "缺少 ${required_var}：请用 `make test` 驱动，或显式传 -D${required_var}=<path>")
    endif()
endforeach()

if(NOT EXISTS "${ZHULONG_ALGO_RUNTIME}")
    message(FATAL_ERROR "找不到静态运行时 ${ZHULONG_ALGO_RUNTIME}：先跑 `make native-build`")
endif()

# vendored stb 的完整性校验：头文件被改过就必须显式更新 SHA256SUMS（见同目录 README）。
set(ZHULONG_STB_SUMS "${ZHULONG_STB_DIR}/SHA256SUMS")
if(NOT EXISTS "${ZHULONG_STB_SUMS}")
    message(FATAL_ERROR "缺少 ${ZHULONG_STB_SUMS}")
endif()
file(STRINGS "${ZHULONG_STB_SUMS}" stb_sum_lines)
foreach(header stb_image.h stb_image_write.h)
    if(NOT EXISTS "${ZHULONG_STB_DIR}/${header}")
        message(FATAL_ERROR "缺少 vendored 头 ${ZHULONG_STB_DIR}/${header}")
    endif()
    file(SHA256 "${ZHULONG_STB_DIR}/${header}" actual_sha)
    set(pinned_sha "")
    foreach(line IN LISTS stb_sum_lines)
        if(line MATCHES "^([0-9a-f]+) +${header}$")
            set(pinned_sha "${CMAKE_MATCH_1}")
        endif()
    endforeach()
    if(NOT pinned_sha)
        message(FATAL_ERROR "SHA256SUMS 中没有 ${header} 的记录")
    endif()
    if(NOT actual_sha STREQUAL pinned_sha)
        message(FATAL_ERROR "${header} 与 SHA256SUMS 不一致（实际 ${actual_sha}，登记 ${pinned_sha}）")
    endif()
endforeach()

find_package(Threads REQUIRED)

add_executable(plate_recognition_devtest EXCLUDE_FROM_ALL
    tests/dev_infer.cpp
    src/plate_recognition.cpp
    src/plate_detector.cpp
    src/plate_recognizer.cpp
)
# 与运行时一致的编译期后端标识（algo.hpp 的张量/资产约定依赖它）
target_compile_definitions(plate_recognition_devtest PRIVATE ZHULONG_PLUGIN_BUILD ZHULONG_INFERENCE_ONNX=1)
target_include_directories(plate_recognition_devtest PRIVATE
    ${ZHULONG_SDK_INCLUDE}
    ${ZHULONG_STB_DIR}
    ${CMAKE_CURRENT_SOURCE_DIR}/src
)
target_link_libraries(plate_recognition_devtest PRIVATE
    ${ZHULONG_ALGO_RUNTIME}
    ${ZHULONG_ONNX_ROOT}/lib/libonnxruntime.so
    Threads::Threads
    ${CMAKE_DL_LIBS}
)
