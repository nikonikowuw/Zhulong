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

int main()
{
    test_base64_encode_basic();
    test_encode_features_b64();
    test_serializer_budget_protection();
    std::cout << "All result_serializer tests passed!\n";
    return 0;
}
