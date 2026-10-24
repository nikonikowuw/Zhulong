#pragma once

#include <zhulong/algo.hpp>
#include <array>
#include <cstdint>
#include <string>

namespace zhulong::plate {

/**
 * @brief 识别模型输入尺寸（该权重导出固定 168×48）
 *
 * 单层车牌直接整块缩放；双层车牌按「上半 5/12 + 下半 2/3 各取一半宽度横向拼接」重排成一行。
 */
constexpr int kRecognitionWidth = 168;
constexpr int kRecognitionHeight = 48;

/**
 * @brief 车牌像素格式 / 层数语义（对应检测模型的 class 索引）
 */
enum class PlateLineLayout : int32_t {
    SingleLine = 0, /**< 单层车牌 */
    DoubleLine = 1, /**< 双层车牌 */
    NewEnergy = 2,  /**< 新能源车牌（仍按单层采样，只是字符位更多） */
};

/**
 * @brief 一个模型输入像素对应的原图采样坐标（像素中心语义）
 */
struct PlateSamplePoint {
    float fx{0.0f};
    float fy{0.0f};
};

/**
 * @brief 计算识别模型输入像素 (x, y) 在原始帧上的采样坐标
 *
 * 抽成纯函数的目的：双层切块重拼是全包最绕的一段坐标映射，必须能被单测直接钉住，
 * 而不是靠「跑一遍模型看结果像不像」来验证。
 *
 * 双层车牌的重拼规则（与参考实现 scripts/test_infer.py 一致）：
 *   把车牌框的上 5/12 与下 2/3 各缩放成满宽，再左右拼接成一行 168 宽。
 *   因此模型输入左半 (x < 84) 对应原图上段，右半对应原图下段。
 *
 * @param x 模型输入横坐标 [0, target_w)
 * @param y 模型输入纵坐标 [0, target_h)
 * @param target_w 模型输入宽度
 * @param target_h 模型输入高度
 * @param crop_x 车牌框左上角 x（原图像素，可为小数）
 * @param crop_y 车牌框左上角 y（原图像素，可为小数）
 * @param crop_w 车牌框宽度（原图像素）
 * @param crop_h 车牌框高度（原图像素）
 * @param plate_type 车牌层数语义（0=单层, 1=双层, 其他=按单层采样）
 */
PlateSamplePoint plate_sample_point(int x, int y, int target_w, int target_h,
                                    float crop_x, float crop_y, float crop_w, float crop_h,
                                    int32_t plate_type);

/**
 * @brief 单块车牌的识别结果
 */
struct RecognizedPlate {
    std::string plate_number;
    std::string plate_color;
    float score{0.0f};
    int32_t plate_type{0}; /**< 0=单层, 1=双层, 2=新能源... */
};

/**
 * @brief 车牌字符 + 颜色识别器（无状态，可被多个 Worker 线程并发调用）
 */
class PlateRecognizer {
public:
    PlateRecognizer() = default;

    RecognizedPlate recognize(
        zhulong::Session& session,
        const zhulong_frame_view& raw,
        const std::array<float, 4>& box,
        int32_t class_id
    );
};

} // namespace zhulong::plate
