/**
 * @file stub_algo_consumer.cpp
 * @brief 测试夹具：一个**以包形态存在**的最小算法可执行文件
 *
 * 用途：跨进程 FrameBroker 集成测试需要一个独立子进程来消费帧。以前那个子进程是
 * 通用 `zhulong_algo_runner` + `dlopen(plugin.so)`；现在交付形态是「包 = 自带算法类
 * 的可执行文件」，所以夹具也必须是同样的形态——否则测试验证的就不是生产路径。
 *
 * 与各算法包的 `src/main.cpp` 的唯一区别：插件类是本文件里的桩（不加载模型、输出与
 * 图像内容无关），因此不需要任何厂商 SDK 或权重，板端/宿主都能跑。
 */

#include <zhulong/algo.hpp>
#include <zhulong/algo_main.hpp>

#include <vector>

namespace {

/**
 * @brief 桩检测插件：每次推理都返回同一个框
 *
 * 刻意不调用 `Model::load`：夹具过去用一个假模型路径，在桩后端下侥幸成功、在真实
 * 后端下必然 model_create 失败，导致同一套回归换后端就假红/假绿。
 */
class StubConsumerPlugin {
public:
    zhulong_status_t init(const std::string& config_json) {
        (void)config_json;
        return ZHULONG_PLUGIN_OK;
    }

    zhulong::Session create_session(uint32_t worker_id) {
        (void)worker_id;
        return zhulong::Session();
    }

    std::vector<zhulong::Detection> detect(zhulong::Session& session, const zhulong::Image& image,
                                          zhulong::TaskContext& task_context) {
        (void)session;
        (void)image;
        (void)task_context;

        zhulong::Detection det;
        det.box[0] = 0.10f;
        det.box[1] = 0.10f;
        det.box[2] = 0.40f;
        det.box[3] = 0.40f;
        det.confidence = 0.9f;
        det.class_id = 1;
        det.label = "person";
        return {det};
    }
};

} // namespace

ZHULONG_ALGO_MAIN(StubConsumerPlugin, zhulong::sdk::DetectionResultAdapter, "StubAlgoConsumer", "1.0.0")
