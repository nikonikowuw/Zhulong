/**
 * @file scene_filter.hpp
 * @brief `filter_unmatched` 的场景级裁剪（纯数据逻辑，无模型依赖）
 *
 * 抽取成独立头文件的理由与 `face_association.hpp` 相同：这是**纯容器重排**逻辑，
 * 却最容易写出「下标漂移」这类错 —— 裁掉一个人体后必须同步重映射所有
 * `faces[].person_index`，否则宿主会读到别人的框（越界或错位），而单元测试完全
 * 不需要模型就能钉住它。
 *
 * 语义（与 `config.schema.json` 的 `filter_unmatched` 一致）：开关打开后，陌生人
 * 不产生**任何**结果 —— 不只是丢掉未匹配的人脸，还要丢掉「只检到人体、没挂上任何
 * 已匹配人脸」的目标（背身、遮挡、距离太远），否则人员抓拍记录里仍会出现陌生人。
 */

#pragma once

#include "common/match_config.hpp"

#include <zhulong/algo.hpp>

#include <cstddef>
#include <cstdint>
#include <utility>
#include <vector>

namespace zhulong::face {

/**
 * @brief 判断单张比对后的人脸结果是否应向上游输出（由 `build_face_item` 与 `unit-test` 共用）
 *
 * 只看「最终是否命中」一个布尔量：`filter_unmatched` 与 `min_match_margin` 的相互作用
 * （分差不足 → `matched=false`）已经在 `Matcher::match` 内部结算，这里再做一次模板/字段
 * 推导只会把两个模块的判据绑在一起。
 */
[[nodiscard]] inline bool should_emit_face_result(bool matched, const MatchParams& match_params) noexcept {
    return !match_params.filter_unmatched || matched;
}

/**
 * @brief 裁掉没有「已匹配人脸」的人体，并重排场景内的 person_index
 *
 * @param scene 就地修改；孤立人脸（`person_index == -1`）保持原样
 *
 * @note 幂等：对已裁剪过的场景再调用一次不会改变任何东西。
 */
inline void drop_unmatched_persons(zhulong::SceneStructure& scene) {
    if (scene.persons.empty()) {
        return;
    }

    std::vector<bool> keep_person(scene.persons.size(), false);
    for (const auto& face : scene.faces) {
        if (face.matched && face.person_index >= 0 &&
            static_cast<size_t>(face.person_index) < keep_person.size()) {
            keep_person[static_cast<size_t>(face.person_index)] = true;
        }
    }

    std::vector<int32_t> index_map(scene.persons.size(), -1);
    std::vector<zhulong::PersonItem> kept_persons;
    kept_persons.reserve(scene.persons.size());
    for (size_t p_idx = 0; p_idx < scene.persons.size(); ++p_idx) {
        if (!keep_person[p_idx]) {
            continue;
        }
        index_map[p_idx] = static_cast<int32_t>(kept_persons.size());
        kept_persons.push_back(std::move(scene.persons[p_idx]));
    }
    scene.persons = std::move(kept_persons);

    for (auto& face : scene.faces) {
        if (face.person_index >= 0 && static_cast<size_t>(face.person_index) < index_map.size()) {
            face.person_index = index_map[static_cast<size_t>(face.person_index)];
        } else {
            face.person_index = -1;
        }
    }
}

} // namespace zhulong::face
