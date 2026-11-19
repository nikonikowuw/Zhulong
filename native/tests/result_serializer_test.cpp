#include <cassert>
#include <cmath>
#include <cstring>
#include <iostream>
#include <vector>

#include "internal/result_serializer.hpp"
#include <zhulong/plugin.h>

using namespace zhulong::sdk::detail;

static void test_base64_encode_basic()
{
    const uint8_t data[] = "Hello World!";
    std::string b64 = base64_encode(data, sizeof(data) - 1);
    assert(b64 == "SGVsbG8gV29ybGQh");

    const uint8_t one[] = "a";
    assert(base64_encode(one, 1) == "YQ==");

    const uint8_t two[] = "ab";
    assert(base64_encode(two, 2) == "YWI=");

    const uint8_t three[] = "abc";
    assert(base64_encode(three, 3) == "YWJj");
}

static void test_encode_features_b64()
{
    // 512 floats
    std::vector<float> feats(512, 0.0f);
    feats[0] = 1.0f;
    feats[1] = -0.5f;

    std::string b64 = encode_features_b64(feats.data(), 512);
    assert(!b64.empty());
    // 512 * 4 = 2048 bytes -> ceil(2048 / 3) * 4 = 683 * 4 = 2732 bytes
    assert(b64.size() == 2732);

    // Test rejection of non-finite floats
    feats[10] = std::numeric_limits<float>::quiet_NaN();
    std::string b64_nan = encode_features_b64(feats.data(), 512);
    assert(b64_nan.empty());

    feats[10] = std::numeric_limits<float>::infinity();
    std::string b64_inf = encode_features_b64(feats.data(), 512);
    assert(b64_inf.empty());

    // Test rejection of count > ZHULONG_FACE_FEATURE_MAX_DIM
    assert(encode_features_b64(feats.data(), 513).empty());
}

static void test_serializer_budget_protection()
{
    // Test case 1: Single face with 512 floats -> total payload well below 64KB
    {
        std::vector<float> feat_pool(512, 0.01f);
        zhulong_face_structure_item face{};
        face.box[0] = 0.1f; face.box[1] = 0.1f; face.box[2] = 0.5f; face.box[3] = 0.5f;
        face.quality = 0.95f;
        face.feature_offset = 0;
        face.feature_dim = 512;

        zhulong_scene_structure scene{};
        scene.face_count = 1;
        scene.faces = &face;
        scene.face_features = feat_pool.data();

        zhulong_inference_result res{};
        res.result_type = ZHULONG_RESULT_TYPE_SCENE_STRUCTURE;
        res.count = 1;
        res.data = &scene;

        std::string json = serialize_inference_result(1, 100, 200, 10, &res, "face_algo");
        assert(json.size() <= zhulong::ipc::MAX_IPC_PAYLOAD_SIZE);
        assert(json.find("\"feature_b64\":") != std::string::npos);
        assert(json.find("\"faces\":[{\"box\":[0.1,0.1,0.5,0.5]") != std::string::npos);
    }

    // Test case 2: 30 faces with 512-dim features
    // 30 * 2732 bytes features = ~82 KB, which exceeds 64 KB limit!
    // The serializer must protect IPC payload: total length <= 65536,
    // all 30 faces are preserved, high-quality faces get feature_b64, low-quality ones drop it.
    {
        constexpr uint32_t kNumFaces = 30;
        std::vector<float> feat_pool(kNumFaces * 512, 0.02f);
        std::vector<zhulong_face_structure_item> faces(kNumFaces);

        for (uint32_t i = 0; i < kNumFaces; ++i)
        {
            faces[i].box[0] = 0.1f;
            faces[i].box[1] = 0.1f;
            faces[i].box[2] = 0.2f;
            faces[i].box[3] = 0.2f;
            // Face 0 has quality 0.99, Face 29 has quality 0.70
            faces[i].quality = 0.99f - static_cast<float>(i) * 0.01f;
            faces[i].person_index = static_cast<int32_t>(i);
            faces[i].feature_offset = i * 512;
            faces[i].feature_dim = 512;
        }

        zhulong_scene_structure scene{};
        scene.face_count = kNumFaces;
        scene.faces = faces.data();
        scene.face_features = feat_pool.data();

        zhulong_inference_result res{};
        res.result_type = ZHULONG_RESULT_TYPE_SCENE_STRUCTURE;
        res.count = 1;
        res.data = &scene;

        std::string json = serialize_inference_result(1, 100, 200, 10, &res, "face_algo");
        assert(json.size() <= zhulong::ipc::MAX_IPC_PAYLOAD_SIZE);

        // Count how many "feature_b64" instances were preserved
        size_t feat_count = 0;
        size_t pos = 0;
        while ((pos = json.find("\"feature_b64\":", pos)) != std::string::npos)
        {
            ++feat_count;
            pos += 14;
        }

        // Expected: fits ~22 faces, so 20 <= feat_count < 30
        std::cout << "30 faces budget test: total json size = " << json.size()
                  << " bytes, preserved features for " << feat_count << " faces\n";
        assert(feat_count > 15 && feat_count < 30);

        // Verify that highest quality face (face 0, quality 0.99) has feature_b64
        // and lowest quality face (face 29, quality 0.70) does NOT have feature_b64
        size_t face0_pos = json.find("\"person_index\":0,");
        assert(face0_pos != std::string::npos);
        size_t face0_end = json.find("}", face0_pos);
        size_t face0_feat = json.find("\"feature_b64\":", face0_pos);
        assert(face0_feat != std::string::npos && face0_feat < face0_end);

        size_t face29_pos = json.find("\"person_index\":29,");
        assert(face29_pos != std::string::npos);
        size_t face29_end = json.find("}", face29_pos);
        size_t face29_feat = json.find("\"feature_b64\":", face29_pos);
        assert(face29_feat == std::string::npos || face29_feat > face29_end);
    }
}

static void test_person_attributes_serialization()
{
    zhulong_person_attribute attrs[2]{};
    std::strncpy(attrs[0].name, "Female", sizeof(attrs[0].name) - 1);
    attrs[0].score = 0.93f;
    std::strncpy(attrs[1].name, "LongSleeve", sizeof(attrs[1].name) - 1);
    attrs[1].score = 0.87f;

    zhulong_person_item persons[2]{};
    persons[0].track_id = 0;
    persons[0].body_box[0] = 0.1f;
    persons[0].body_box[1] = 0.1f;
    persons[0].body_box[2] = 0.5f;
    persons[0].body_box[3] = 0.9f;
    persons[0].body_score = 0.9f;
    persons[0].attribute_count = 2;
    persons[0].attributes = attrs;

    persons[1].body_box[0] = 0.6f;
    persons[1].body_box[1] = 0.1f;
    persons[1].body_box[2] = 0.9f;
    persons[1].body_box[3] = 0.9f;
    persons[1].body_score = 0.8f;

    zhulong_scene_structure scene{};
    scene.person_count = 2;
    scene.persons = persons;

    zhulong_inference_result res{};
    res.result_type = ZHULONG_RESULT_TYPE_SCENE_STRUCTURE;
    res.count = 2;
    res.data = &scene;

    const std::string json = serialize_inference_result(1, 100, 200, 10, &res, "face_algo");
    assert(json.find("\"attributes\":{\"Female\":0.93,\"LongSleeve\":0.87}") != std::string::npos);

    // 没有属性的人不得输出空 attributes 对象（否则 Go 侧 map 会得到 {} 而不是缺失）
    size_t attr_pos = json.find("\"attributes\":");
    assert(attr_pos != std::string::npos);
    assert(json.find("\"attributes\":", attr_pos + 1) == std::string::npos);

    // count 非零但指针为空：视为无属性，不能崩、也不能输出半个对象
    persons[0].attributes = nullptr;
    const std::string null_json = serialize_inference_result(1, 100, 200, 10, &res, "face_algo");
    assert(null_json.find("\"attributes\":") == std::string::npos);

    // 非有限分数必须被跳过（NaN 进 JSON 会让宿主整条结果解析失败）
    persons[0].attributes = attrs;
    attrs[1].score = std::numeric_limits<float>::quiet_NaN();
    const std::string nan_json = serialize_inference_result(1, 100, 200, 10, &res, "face_algo");
    assert(nan_json.find("\"LongSleeve\"") == std::string::npos);
    assert(nan_json.find("\"Female\":0.93") != std::string::npos);

    // 全部分数非法时不能输出空对象：{} 会让下游误判成「跑过模型但没有达标项」
    attrs[0].score = std::numeric_limits<float>::quiet_NaN();
    const std::string all_nan_json = serialize_inference_result(1, 100, 200, 10, &res, "face_algo");
    assert(all_nan_json.find("\"attributes\":") == std::string::npos);
}

// 人脸属性必须挂在人脸对象上，且与人体属性互不串味：
// 同一个 label 名在两个通道上出现时，不得互相覆盖或合并。
static void test_face_attributes_serialization()
{
    zhulong_person_attribute face_attrs[2]{};
    std::strncpy(face_attrs[0].name, "Female", sizeof(face_attrs[0].name) - 1);
    face_attrs[0].score = 0.12f;
    std::strncpy(face_attrs[1].name, "Age18-30", sizeof(face_attrs[1].name) - 1);
    face_attrs[1].score = 0.66f;

    zhulong_person_attribute person_attrs[1]{};
    std::strncpy(person_attrs[0].name, "Female", sizeof(person_attrs[0].name) - 1);
    person_attrs[0].score = 0.93f;

    zhulong_face_structure_item faces[2]{};
    faces[0].box[2] = 0.5f;
    faces[0].box[3] = 0.5f;
    faces[0].quality = 0.9f;
    faces[0].person_index = 0;
    faces[0].attribute_count = 2;
    faces[0].attribute_offset = 0;

    faces[1].box[0] = 0.6f;
    faces[1].box[2] = 0.9f;
    faces[1].box[3] = 0.5f;
    faces[1].quality = 0.8f;
    faces[1].person_index = -1;
    faces[1].attribute_count = 0;
    faces[1].attribute_offset = 0;

    zhulong_person_item person{};
    person.body_box[2] = 0.5f;
    person.body_box[3] = 0.9f;
    person.body_score = 0.9f;
    person.attribute_count = 1;
    person.attributes = person_attrs;

    zhulong_scene_structure scene{};
    scene.person_count = 1;
    scene.persons = &person;
    scene.face_count = 2;
    scene.faces = faces;
    scene.face_attributes = face_attrs;
    scene.face_attribute_total = 2;

    zhulong_inference_result res{};
    res.result_type = ZHULONG_RESULT_TYPE_SCENE_STRUCTURE;
    res.count = 3;
    res.data = &scene;

    const std::string json = serialize_inference_result(1, 100, 200, 10, &res, "face_algo");

    // 人体属性照旧挂在人体上
    assert(json.find("\"body_score\":0.9,\"attributes\":{\"Female\":0.93}") != std::string::npos);
    // 人脸属性挂在人脸对象末尾，顺序与张量内的标签顺序一致
    assert(json.find("\"person_index\":0,\"person_id\":\"\",\"person_name\":\"\"") != std::string::npos);
    assert(json.find("\"attributes\":{\"Female\":0.12,\"Age18-30\":0.66}") != std::string::npos);
    // 两张人脸 + 一个人体，共 3 个 attributes 对象（人脸 1 个、人体 1 个、第二张脸没有）
    size_t first = json.find("\"attributes\":");
    size_t second = json.find("\"attributes\":", first + 1);
    assert(first != std::string::npos && second != std::string::npos);
    assert(json.find("\"attributes\":", second + 1) == std::string::npos);

    // attribute_offset 越界时必须整体放弃该人脸属性，不能读到池外内存
    faces[0].attribute_offset = 1;
    const std::string oob_json = serialize_inference_result(1, 100, 200, 10, &res, "face_algo");
    assert(oob_json.find("\"Age18-30\"") == std::string::npos);
    assert(oob_json.find("\"Female\":0.12") == std::string::npos);
    // 人体属性不受影响
    assert(oob_json.find("\"Female\":0.93") != std::string::npos);

    // 池指针为空时不得解引用
    faces[0].attribute_offset = 0;
    scene.face_attributes = nullptr;
    const std::string null_json = serialize_inference_result(1, 100, 200, 10, &res, "face_algo");
    assert(null_json.find("\"Age18-30\"") == std::string::npos);
    assert(null_json.find("\"Female\":0.12") == std::string::npos);

    // PERSON_STRUCTURE 布局下的人脸属性（人脸内嵌指针）
    scene.face_attributes = face_attrs;
    zhulong_face_item person_face{};
    person_face.box[2] = 0.5f;
    person_face.box[3] = 0.5f;
    person_face.quality = 0.9f;
    person_face.attribute_count = 2;
    person_face.attributes = face_attrs;

    zhulong_person_item carrier{};
    carrier.body_box[2] = 0.5f;
    carrier.body_box[3] = 0.9f;
    carrier.body_score = 0.9f;
    carrier.face_count = 1;
    carrier.faces = &person_face;

    zhulong_inference_result person_res{};
    person_res.result_type = ZHULONG_RESULT_TYPE_PERSON_STRUCTURE;
    person_res.count = 1;
    person_res.data = &carrier;

    const std::string person_json = serialize_inference_result(1, 100, 200, 10, &person_res, "face_algo");
    assert(person_json.find("\"attributes\":{\"Female\":0.12,\"Age18-30\":0.66}") != std::string::npos);
    // 没有人体属性时不得凭空造一个
    assert(person_json.find("\"body_score\":0.9,\"attributes\"") == std::string::npos);
}

int main()
{
    test_base64_encode_basic();
    test_encode_features_b64();
    test_serializer_budget_protection();
    test_person_attributes_serialization();
    test_face_attributes_serialization();
    std::cout << "All result_serializer tests passed!\n";
    return 0;
}
