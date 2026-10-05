/**
 * @file engine_abi_test.c
 * @brief 纯 C 环境下的 Zhulong C ABI 兼容性与边界检查测试
 *
 * 验证目标：
 * 1. 验证头文件 Zhulong/engine.h 能够被纯 C 编译器（C11）无缝包含和链接。
 * 2. 验证各个 C ABI 函数对空指针、非法参数的前置防御性校验。
 * 3. 验证未启动状态下的操作拒绝（Zhulong_ERR_NOT_RUNNING）。
 * 4. 验证引擎生命周期（创建 -> 启动 -> 停止 -> 销毁）的基础契约与幂等性。
 */

#include "Zhulong/engine.h"

int main(void) {
    Zhulong_engine_h engine = 0;

    /* 1. 测试空指针创建：必须安全失败并返回 INVALID_ARGUMENT */
    if (Zhulong_engine_create(0) != Zhulong_ERR_INVALID_ARGUMENT) return 1;

    /* 2. 正常创建引擎实例 */
    if (Zhulong_engine_create(&engine) != Zhulong_OK || engine == 0) return 2;

    /* 3. 正常启动与停止 */
    if (Zhulong_engine_start(engine) != Zhulong_OK) return 3;
    if (Zhulong_engine_stop(engine) != Zhulong_OK) return 4;

    /* 4. 边界校验：引擎停止状态下获取流必须返回 NOT_RUNNING */
    Zhulong_probe_result_h result = 0;
    Zhulong_stream_id stream = 999;
    Zhulong_subscription_id subscription = 999;
    Zhulong_video_view video = {0};
    Zhulong_stream_options options = {99, 0, 0};

    if (Zhulong_stream_acquire(engine, "rtsp://localhost/A", 0, 1,
        Zhulong_CONSUMER_PREVIEW, &stream) != Zhulong_ERR_NOT_RUNNING || stream != 0) return 5;

    /* 5. 空指针防御校验 */
    if (Zhulong_engine_probe(0, "rtsp://localhost/A", 0, &result) != Zhulong_ERR_INVALID_ARGUMENT || result != 0) return 6;
    if (Zhulong_probe_result_view(0, &video) != Zhulong_ERR_INVALID_ARGUMENT) return 7;
    if (Zhulong_stream_subscribe(engine, 0, 1, 0, 0, &subscription) != Zhulong_ERR_INVALID_ARGUMENT || subscription != 0) return 8;

    /* 6. 非法选项校验（如未知的 transport 协议值 99） */
    if (Zhulong_stream_acquire(engine, "rtsp://localhost/A", &options, 1,
        Zhulong_CONSUMER_PREVIEW, &stream) != Zhulong_ERR_INVALID_ARGUMENT) return 9;

    /* 7. 非法句柄调用校验 */
    if (Zhulong_stream_release(0, 1, 1) != Zhulong_ERR_INVALID_ARGUMENT) return 10;
    if (Zhulong_stream_unsubscribe(0, 1, 1) != Zhulong_ERR_INVALID_ARGUMENT) return 11;
    if (Zhulong_stream_get_status(0, 1, 0) != Zhulong_ERR_INVALID_ARGUMENT) return 12;

    /* 8. 销毁安全测试：传入 NULL 指针必须保证安全无崩溃 */
    Zhulong_probe_result_destroy(0);
    Zhulong_engine_destroy(engine);
    Zhulong_engine_destroy(0);

    return 0;
}
