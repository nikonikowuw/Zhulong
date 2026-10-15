#include "../src/algorithm/runner/gallery_index.hpp"
#include "../src/algorithm/runner/gallery_parser.hpp"
#include <cassert>
#include <iostream>
#include <thread>
#include <chrono>

using namespace zhulong::runner;

void test_gallery_index_basic() {
    GalleryIndex idx;
    assert(idx.size() == 0);

    // 1. Reset
    std::vector<FaceTemplate> initial = {
        {"f1", "p1", "张三", "img1.jpg", {0.1f, 0.2f}},
        {"f2", "p1", "张三", "img2.jpg", {0.3f, 0.4f}},
        {"f3", "p2", "李四", "img3.jpg", {0.5f, 0.6f}},
    };
    idx.reset(initial);
    assert(idx.size() == 3);

    auto snap = idx.snapshot();
    assert(snap->size() == 3);

    // 2. Upsert
    FaceTemplate f4{"f4", "p3", "王五", "img4.jpg", {0.7f, 0.8f}};
    idx.upsert(f4);
    assert(idx.size() == 4);

    // 3. Remove by face_id
    idx.remove("f4", "");
    assert(idx.size() == 3);

    // 4. Remove by person_id (p1 has 2 faces, both should be removed)
    idx.remove("", "p1");
    assert(idx.size() == 1);

    auto snap2 = idx.snapshot();
    assert(snap2->size() == 1);
    assert((*snap2)[0].face_id == "f3");

    std::cout << "[PASS] test_gallery_index_basic" << std::endl;
}

void test_gallery_parser() {
    // 1. Parse Sync Shard
    std::string json_shard = R"({
        "sync_id": "sync_100",
        "shard_index": 0,
        "shard_total": 2,
        "items": [
            {
                "face_id": "face_a",
                "person_id": "P_001",
                "person_name": "Alice",
                "image_path": "a.jpg",
                "features": [0.125, -0.5, 0.875]
            }
        ]
    })";

    ParsedSyncShard shard = parse_sync_shard_payload(
        reinterpret_cast<const uint8_t*>(json_shard.data()), json_shard.size());
    assert(shard.sync_id == "sync_100");
    assert(shard.shard_index == 0);
    assert(shard.shard_total == 2);
    assert(shard.items.size() == 1);
    assert(shard.items[0].face_id == "face_a");
    assert(shard.items[0].person_id == "P_001");
    assert(shard.items[0].person_name == "Alice");
    assert(shard.items[0].image_path == "a.jpg");
    assert(shard.items[0].features.size() == 3);
    assert(shard.items[0].features[0] == 0.125f);
    assert(shard.items[0].features[1] == -0.5f);
    assert(shard.items[0].features[2] == 0.875f);

    // 2. Parse Upsert
    std::string json_upsert = R"({
        "face_id": "face_b",
        "person_id": "P_002",
        "person_name": "Bob",
        "image_path": "b.jpg",
        "features": [1.0, 2.0]
    })";
    FaceTemplate up = parse_upsert_payload(
        reinterpret_cast<const uint8_t*>(json_upsert.data()), json_upsert.size());
    assert(up.face_id == "face_b");
    assert(up.person_id == "P_002");
    assert(up.person_name == "Bob");
    assert(up.image_path == "b.jpg");
    assert(up.features.size() == 2);

    // 3. Parse Delete
    std::string json_del = R"({"face_id":"f_del","person_id":"p_del"})";
    ParsedDelete del = parse_delete_payload(
        reinterpret_cast<const uint8_t*>(json_del.data()), json_del.size());
    assert(del.face_id == "f_del");
    assert(del.person_id == "p_del");

    std::cout << "[PASS] test_gallery_parser" << std::endl;
}

void test_sharded_sync_context() {
    ShardedSyncContext ctx;
    ctx.start_sync("sync_1", 2);
    assert(!ctx.is_complete());

    ctx.received_shards.insert(0);
    assert(!ctx.is_complete());

    ctx.received_shards.insert(1);
    assert(ctx.is_complete());
    assert(!ctx.is_expired());

    std::cout << "[PASS] test_sharded_sync_context" << std::endl;
}

int main() {
    test_gallery_index_basic();
    test_gallery_parser();
    test_sharded_sync_context();
    std::cout << "All runner gallery tests passed successfully!" << std::endl;
    return 0;
}
