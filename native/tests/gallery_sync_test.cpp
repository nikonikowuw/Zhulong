#include "internal/gallery_index.hpp"
#include "internal/gallery_parser.hpp"
#include <cassert>
#include <cmath>
#include <iostream>
#include <thread>
#include <atomic>
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

// =============================================================================
// REQ-5: MSG_GALLERY_SYNC 分片聚合的幂等性与索引范围门禁
// =============================================================================

/** @brief 构造一个只含指定 face_id 的分片。 */
static ParsedSyncShard make_shard(const std::string& sync_id, int index, int total,
                                  const std::string& face_id) {
    ParsedSyncShard s;
    s.sync_id = sync_id;
    s.shard_index = index;
    s.shard_total = total;
    s.items.push_back(FaceTemplate{face_id, "p_" + face_id, "N", "i.jpg", {1.0f, 2.0f}});
    return s;
}

void test_gallery_sync_shard_idempotent() {
    ShardedSyncContext ctx;

    // 正常收齐 2 片
    assert(accumulate_sync_shard(ctx, make_shard("s1", 0, 2, "f0")));
    assert(accumulate_sync_shard(ctx, make_shard("s1", 1, 2, "f1")));
    assert(ctx.is_complete());
    assert(ctx.collected_items.size() == 2);

    // ⚠️ 核心断言：重传已收过的 shard 0 必须被拒且不重复累加 items。
    //    若退化为只靠 insert 判重（旧实现），这里会变成 3 而 is_complete() 仍为 true，
    //    最终经 gallery_index.reset() 把重复条目写进底库。
    ShardedSyncContext dup_ctx;
    assert(accumulate_sync_shard(dup_ctx, make_shard("s2", 0, 2, "f0")));
    assert(accumulate_sync_shard(dup_ctx, make_shard("s2", 0, 2, "f0")) == false); // 重复
    assert(dup_ctx.collected_items.size() == 1);
    assert(!dup_ctx.is_complete());

    // 补齐第 1 片后完成，条目数仍是 2（不是 3）
    assert(accumulate_sync_shard(dup_ctx, make_shard("s2", 1, 2, "f1")));
    assert(dup_ctx.is_complete());
    assert(dup_ctx.collected_items.size() == 2);

    std::cout << "[PASS] test_gallery_sync_shard_idempotent" << std::endl;
}

void test_gallery_sync_shard_index_out_of_range() {
    ShardedSyncContext ctx;

    // 越界（>= shard_total）
    assert(accumulate_sync_shard(ctx, make_shard("s3", 5, 2, "bad")) == false);
    assert(ctx.collected_items.empty());
    assert(ctx.received_shards.empty());
    assert(!ctx.is_complete());

    // 负值
    assert(accumulate_sync_shard(ctx, make_shard("s3", -1, 2, "bad")) == false);
    assert(ctx.collected_items.empty());
    assert(ctx.received_shards.empty());

    // shard_total <= 0
    assert(accumulate_sync_shard(ctx, make_shard("s3", 0, 0, "bad")) == false);
    assert(ctx.collected_items.empty());

    // ⚠️ 关键：越界分片不得污染完成判定 —— 收齐合法两片后仍能正确完成
    assert(accumulate_sync_shard(ctx, make_shard("s3", 0, 2, "f0")));
    assert(accumulate_sync_shard(ctx, make_shard("s3", 1, 2, "f1")));
    assert(ctx.is_complete());
    assert(ctx.collected_items.size() == 2);

    std::cout << "[PASS] test_gallery_sync_shard_index_out_of_range" << std::endl;
}

/**
 * @brief 回归：收齐后的重复分片不得触发二次 reset。
 *
 * 背景：`is_complete()` 只看 `received_shards.size()`，收齐后**恒为真**。
 * 若调用方对「重复分片」也执行 `gallery_index.reset(std::move(collected_items))`，
 * 则 collected_items 已被 move 空 → **底库被清空**（比重复累加更严重）。
 *
 * 本用例模拟 runner.cpp 的调用契约：只有 `accumulate_sync_shard` 返回 true
 * 且 `is_complete()` 为真时才允许应用底库。
 */
void test_gallery_sync_duplicate_after_complete_does_not_reapply() {
    ShardedSyncContext ctx;
    int apply_count = 0;
    size_t last_applied_count = 0;

    auto on_shard = [&](const ParsedSyncShard& s) {
        const bool accepted = (s.shard_total > 0) && accumulate_sync_shard(ctx, s);
        if (accepted && ctx.is_complete()) {
            ++apply_count;
            last_applied_count = ctx.collected_items.size();
            ctx.collected_items.clear(); // 模拟 std::move 进 gallery_index.reset
        }
    };

    on_shard(make_shard("s4", 0, 2, "f0"));
    on_shard(make_shard("s4", 1, 2, "f1"));
    assert(apply_count == 1);
    assert(last_applied_count == 2);

    // 重复分片：accepted == false，因此**不得**触发第二次应用
    on_shard(make_shard("s4", 0, 2, "f0"));
    on_shard(make_shard("s4", 1, 2, "f1"));
    assert(apply_count == 1);           // 未被二次应用
    assert(last_applied_count == 2);    // 上次应用的内容未被空数据覆盖

    std::cout << "[PASS] test_gallery_sync_duplicate_after_complete_does_not_reapply" << std::endl;
}

void test_gallery_registry_concurrency() {
    auto& reg = zhulong::runtime::GalleryRegistry::instance();
    reg.clear();
    assert(reg.size() == 0);

    std::atomic<bool> stop{false};
    std::atomic<int> read_count{0};

    // 启动 4 个并发读线程（模拟推理任务调用 snapshot）
    std::vector<std::thread> readers;
    for (int i = 0; i < 4; ++i) {
        readers.emplace_back([&]() {
            while (!stop.load()) {
                auto snap = reg.snapshot();
                assert(snap != nullptr);
                for (const auto& item : *snap) {
                    assert(!item.face_id.empty());
                }
                read_count.fetch_add(1);
            }
        });
    }

    // 主线程执行并发写入（reset / upsert / remove）
    for (int i = 1; i <= 200; ++i) {
        FaceTemplate item{
            "f_" + std::to_string(i),
            "p_" + std::to_string(i % 10),
            "Name_" + std::to_string(i),
            "path.jpg",
            {static_cast<float>(i), 1.0f}
        };
        reg.upsert(item);
        if (i % 50 == 0) {
            reg.remove("f_" + std::to_string(i - 1), "");
        }
    }

    stop.store(true);
    for (auto& t : readers) {
        t.join();
    }

    assert(read_count.load() > 0);
    assert(reg.size() > 0);

    // 清空验证
    reg.clear();
    assert(reg.size() == 0);
    assert(reg.snapshot()->empty());

    std::cout << "[PASS] test_gallery_registry_concurrency (reads: " << read_count.load() << ")" << std::endl;
}

void test_gallery_parser_unicode_escapes() {
    // 1. Go json.Marshal 默认转义的 HTML 字符（大小写 hex 均需解码）
    {
        std::string payload = R"({
            "face_id": "f_html",
            "person_id": "P\u00261",
            "person_name": "A\u0026B \u003cC\u003e \u003CD\u003E",
            "image_path": "img.jpg",
            "features": [0.5]
        })";
        FaceTemplate item = parse_upsert_payload(
            reinterpret_cast<const uint8_t*>(payload.data()), payload.size());
        assert(item.face_id == "f_html");
        assert(item.person_id == "P&1");
        assert(item.person_name == "A&B <C> <D>");
    }

    // 2. 2 字节拉丁扩展、3 字节中文 BMP、4 字节 UTF-16 代理对
    {
        std::string payload = R"({
            "face_id": "f_utf8",
            "person_id": "p_utf8",
            "person_name": "caf\u00e9-\u5f20\u4e09-\ud83d\ude00",
            "features": [1.0]
        })";
        FaceTemplate item = parse_upsert_payload(
            reinterpret_cast<const uint8_t*>(payload.data()), payload.size());
        assert(item.person_name == std::string("caf\xC3\xA9-\xE5\xBC\xA0\xE4\xB8\x89-\xF0\x9F\x98\x80"));
    }

    // 3. 孤立高/低代理项输出 U+FFFD，不破坏后续字符；畸形/截断 \u 不越界不崩溃
    {
        size_t pos = 0;
        const std::string s1 = R"("\ud83dX\ude00Y\ud83d\u0041")";
        assert(parse_json_string(s1, pos) == std::string("\xEF\xBF\xBD" "X" "\xEF\xBF\xBD" "Y" "\xEF\xBF\xBD" "A"));

        // 非 NUL 结尾的截断转义
        const std::string backing = R"("abc\u12ZZZZ")";
        size_t p2 = 0;
        std::string_view truncated(backing.data(), 8); // `"abc\u12`
        assert(parse_json_string(truncated, p2) == "abc12");

        size_t p3 = 0;
        const std::string bad_hex = R"("ok\uZZZZ!")";
        assert(parse_json_string(bad_hex, p3) == "okZZZZ!");
    }

    std::cout << "[PASS] test_gallery_parser_unicode_escapes" << std::endl;
}

void test_gallery_parser_non_nul_truncated_floats() {
    // 1. 截断数字紧跟非 NUL 尾随数字：不得越过 string_view 边界吞掉后继数字
    {
        const std::string backing = R"({"face_id":"f1","features":[0.5999999)";
        const size_t prefix_len = backing.size() - 6; // 截断在 "0.5" 处，紧邻字节是 "999999"
        FaceTemplate item = parse_upsert_payload(
            reinterpret_cast<const uint8_t*>(backing.data()), prefix_len);
        assert(item.face_id == "f1");
        assert(item.features.size() == 1);
        assert(std::fabs(item.features[0] - 0.5f) < 1e-6f);
    }

    // 2. 截断在指数符号 'e' 处，紧邻字节是 '5'：必须解析为 1.0 而非 1e5
    {
        const std::string backing = R"({"face_id":"f2","features":[1e5)";
        const size_t prefix_len = backing.size() - 1; // 截断在 "1e"
        FaceTemplate item = parse_upsert_payload(
            reinterpret_cast<const uint8_t*>(backing.data()), prefix_len);
        assert(item.features.size() == 1);
        assert(std::fabs(item.features[0] - 1.0f) < 1e-6f);
    }

    // 3. 截断在数组内尾随空白处，紧邻字节是数字：不得越界读入尾随数字
    {
        const std::string backing = R"({"face_id":"f3","features":[0.25   99)";
        const size_t prefix_len = backing.size() - 2; // 截断在 "0.25   "
        FaceTemplate item = parse_upsert_payload(
            reinterpret_cast<const uint8_t*>(backing.data()), prefix_len);
        assert(item.features.size() == 1);
        assert(std::fabs(item.features[0] - 0.25f) < 1e-6f);
    }

    // 4. 超长浮点字面量 (> 64 字符) 与畸形 token 不死循环、正确提取合法值
    {
        const std::string long_decimal = "0.25" + std::string(80, '0');
        const std::string payload = R"({"face_id":"f4","features":[)" + long_decimal + R"(, "bad", +, -, ., e, -1.5e-1]})";
        FaceTemplate item = parse_upsert_payload(
            reinterpret_cast<const uint8_t*>(payload.data()), payload.size());
        assert(item.features.size() == 2);
        assert(std::fabs(item.features[0] - 0.25f) < 1e-6f);
        assert(std::fabs(item.features[1] - (-0.15f)) < 1e-6f);
    }

    std::cout << "[PASS] test_gallery_parser_non_nul_truncated_floats" << std::endl;
}

int main() {
    test_gallery_index_basic();
    test_gallery_parser();
    test_gallery_parser_unicode_escapes();
    test_gallery_parser_non_nul_truncated_floats();
    test_sharded_sync_context();
    test_gallery_sync_shard_idempotent();
    test_gallery_sync_shard_index_out_of_range();
    test_gallery_sync_duplicate_after_complete_does_not_reapply();
    test_gallery_registry_concurrency();
    std::cout << "All runner gallery tests passed successfully!" << std::endl;
    return 0;
}
