#include <zhulong/plugin.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <assert.h>

static zhulong_status_t mock_model_create(const char* config_json, zhulong_model_group_h* out_model) {
    (void)config_json;
    if (!out_model) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    *out_model = (zhulong_model_group_h)(uintptr_t)0x1000;
    return ZHULONG_PLUGIN_OK;
}

static void mock_model_destroy(zhulong_model_group_h model) {
    (void)model;
}

static zhulong_status_t mock_worker_create(zhulong_model_group_h model, uint32_t worker_id, zhulong_worker_ctx_h* out_worker) {
    (void)model;
    (void)worker_id;
    if (!out_worker) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    *out_worker = (zhulong_worker_ctx_h)(uintptr_t)0x2000;
    return ZHULONG_PLUGIN_OK;
}

static void mock_worker_destroy(zhulong_worker_ctx_h worker) {
    (void)worker;
}

static zhulong_status_t mock_task_open(zhulong_worker_ctx_h worker, uint64_t task_id, zhulong_task_ctx_h* out_task) {
    (void)worker;
    (void)task_id;
    if (!out_task) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    *out_task = (zhulong_task_ctx_h)(uintptr_t)0x3000;
    return ZHULONG_PLUGIN_OK;
}

static void mock_task_close(zhulong_task_ctx_h task) {
    (void)task;
}

static zhulong_status_t mock_process(zhulong_task_ctx_h task, const zhulong_frame_view* frame, zhulong_inference_result** out_result) {
    (void)task;
    (void)frame;
    if (!out_result) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;
    *out_result = NULL;
    return ZHULONG_PLUGIN_OK;
}

static void mock_result_release(zhulong_inference_result* result) {
    (void)result;
}

static zhulong_status_t mock_get_api(uint32_t requested_version, void* out_api_table, size_t table_size) {
    if (requested_version != 1) return ZHULONG_PLUGIN_ERR_NOT_SUPPORTED;
    if (!out_api_table || table_size < sizeof(zhulong_plugin_api_v1)) return ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT;

    zhulong_plugin_api_v1* api = (zhulong_plugin_api_v1*)out_api_table;
    memset(api, 0, sizeof(*api));
    api->struct_size = (uint32_t)sizeof(zhulong_plugin_api_v1);
    api->api_version = 1;
    api->plugin_name = "MockPlugin";
    api->plugin_version = "1.0.0";
    api->model_create = mock_model_create;
    api->model_destroy = mock_model_destroy;
    api->worker_create = mock_worker_create;
    api->worker_destroy = mock_worker_destroy;
    api->task_open = mock_task_open;
    api->task_close = mock_task_close;
    api->process = mock_process;
    api->result_release = mock_result_release;

    return ZHULONG_PLUGIN_OK;
}

int main(void) {
    /* 1. 验证基础枚举与状态码 */
    assert(ZHULONG_PLUGIN_OK == 0);
    assert(ZHULONG_PLUGIN_ERR_INVALID_ARGUMENT == -1);
    assert(ZHULONG_PLUGIN_ERR_OUT_OF_MEMORY == -2);
    assert(ZHULONG_PLUGIN_ERR_NOT_SUPPORTED == -3);
    assert(ZHULONG_PLUGIN_ERR_INTERNAL == -99);

    /* 2. 验证数据结构与对齐 */
    _Static_assert(sizeof(zhulong_bbox) == 88, "zhulong_bbox must be 88 bytes");
    _Static_assert(sizeof(zhulong_bbox) % 8 == 0, "zhulong_bbox must be 8-byte aligned");
    _Static_assert(sizeof(zhulong_frame_view) == 88, "zhulong_frame_view must be 88 bytes");
    _Static_assert(sizeof(zhulong_frame_view) % 8 == 0, "zhulong_frame_view must be 8-byte aligned");
    _Static_assert(offsetof(zhulong_frame_view, dma_buf_fd) == 28, "dma_buf_fd offset mismatch");
    _Static_assert(offsetof(zhulong_frame_view, planes) == 32, "planes offset mismatch");
    _Static_assert(offsetof(zhulong_frame_view, strides) == 64, "strides offset mismatch");
    _Static_assert(offsetof(zhulong_frame_view, timestamp_us) == 80, "timestamp_us offset mismatch");
    assert(sizeof(zhulong_inference_result) > 0);
    assert(sizeof(zhulong_plugin_api_v1) > 0);

    /* 验证结构化结果常量与对齐 */
    assert(ZHULONG_PERSON_ID_MAX_LEN == 64);
    assert(ZHULONG_PERSON_NAME_MAX_LEN == 64);
    assert(ZHULONG_FACE_ID_MAX_LEN == 64);
    assert(ZHULONG_FACE_LANDMARK_COUNT == 5);
    assert(ZHULONG_RESULT_TYPE_PERSON_STRUCTURE == 3);

    _Static_assert(sizeof(zhulong_face_candidate) == 200, "zhulong_face_candidate must be 200 bytes");
    _Static_assert(sizeof(zhulong_face_candidate) % 8 == 0, "zhulong_face_candidate must be 8-byte aligned");

    _Static_assert(sizeof(zhulong_face_item) == 280, "zhulong_face_item must be 280 bytes");
    _Static_assert(sizeof(zhulong_face_item) % 8 == 0, "zhulong_face_item must be 8-byte aligned");

    _Static_assert(sizeof(zhulong_person_item) == 40, "zhulong_person_item must be 40 bytes");
    _Static_assert(sizeof(zhulong_person_item) % 8 == 0, "zhulong_person_item must be 8-byte aligned");

    /* 3. 验证 API 表填写与调用流程 */
    zhulong_plugin_api_v1 api;
    zhulong_status_t rc = mock_get_api(1, &api, sizeof(api));
    assert(rc == ZHULONG_PLUGIN_OK);
    assert(api.api_version == 1);
    assert(strcmp(api.plugin_name, "MockPlugin") == 0);

    /* 测试非法版本请求 */
    rc = mock_get_api(2, &api, sizeof(api));
    assert(rc == ZHULONG_PLUGIN_ERR_NOT_SUPPORTED);

    /* 测试模型与上下文生命周期模拟 */
    zhulong_model_group_h model = NULL;
    rc = api.model_create("{}", &model);
    assert(rc == ZHULONG_PLUGIN_OK);
    assert(model != NULL);

    zhulong_worker_ctx_h worker = NULL;
    rc = api.worker_create(model, 0, &worker);
    assert(rc == ZHULONG_PLUGIN_OK);
    assert(worker != NULL);

    zhulong_task_ctx_h task = NULL;
    rc = api.task_open(worker, 1001, &task);
    assert(rc == ZHULONG_PLUGIN_OK);
    assert(task != NULL);

    zhulong_frame_view frame;
    memset(&frame, 0, sizeof(frame));
    frame.frame_id = 1;
    frame.width = 1920;
    frame.height = 1080;
    frame.format = ZHULONG_PIXEL_FORMAT_BGR24;

    zhulong_inference_result* result = NULL;
    rc = api.process(task, &frame, &result);
    assert(rc == ZHULONG_PLUGIN_OK);
    assert(result == NULL);

    api.task_close(task);
    api.worker_destroy(worker);
    api.model_destroy(model);

    /* 4. 验证结构化结果在纯 C 边界下的单块 Arena 解构 */
    {
        /* 模拟分配 1 个人体 + 1 张人脸 + 1 个候选人的 Arena 连续内存块 */
        size_t arena_size = sizeof(zhulong_person_item) + sizeof(zhulong_face_item) + sizeof(zhulong_face_candidate);
        uint8_t* arena = (uint8_t*)malloc(arena_size);
        assert(arena != NULL);
        memset(arena, 0, arena_size);

        zhulong_person_item* person = (zhulong_person_item*)arena;
        zhulong_face_item* face = (zhulong_face_item*)(arena + sizeof(zhulong_person_item));
        zhulong_face_candidate* candidate = (zhulong_face_candidate*)(arena + sizeof(zhulong_person_item) + sizeof(zhulong_face_item));

        /* 填充候选人 */
        strncpy(candidate->person_id, "user_001", sizeof(candidate->person_id) - 1);
        strncpy(candidate->person_name, "Alice", sizeof(candidate->person_name) - 1);
        candidate->similarity = 0.92f;

        /* 填充人脸 */
        face->box[0] = 0.1f;
        face->box[1] = 0.2f;
        face->box[2] = 0.4f;
        face->box[3] = 0.5f;
        face->quality = 0.88f;
        face->landmarks[0][0] = 0.15f; face->landmarks[0][1] = 0.25f;
        face->landmarks[1][0] = 0.35f; face->landmarks[1][1] = 0.25f;
        face->landmarks[2][0] = 0.25f; face->landmarks[2][1] = 0.35f;
        face->landmarks[3][0] = 0.18f; face->landmarks[3][1] = 0.45f;
        face->landmarks[4][0] = 0.32f; face->landmarks[4][1] = 0.45f;
        face->matched = 1;
        face->similarity = 0.92f;
        strncpy(face->person_id, "user_001", sizeof(face->person_id) - 1);
        strncpy(face->person_name, "Alice", sizeof(face->person_name) - 1);
        face->candidate_count = 1;
        face->candidates = candidate;

        /* 填充人体 (纯人脸退化模式) */
        person->track_id = 0;
        person->body_box[0] = face->box[0];
        person->body_box[1] = face->box[1];
        person->body_box[2] = face->box[2];
        person->body_box[3] = face->box[3];
        person->body_score = 1.0f;
        person->face_count = 1;
        person->faces = face;

        /* 构造 zhulong_inference_result */
        zhulong_inference_result res;
        res.frame_id = 100;
        res.task_id = 200;
        res.result_type = ZHULONG_RESULT_TYPE_PERSON_STRUCTURE;
        res.count = 1;
        res.data = arena;
        res.internal_token = arena;

        /* 断言验证纯 C 读取 */
        assert(res.result_type == ZHULONG_RESULT_TYPE_PERSON_STRUCTURE);
        assert(res.count == 1);
        const zhulong_person_item* p = (const zhulong_person_item*)res.data;
        assert(p->face_count == 1);
        assert(p->faces != NULL);
        assert(p->faces[0].matched == 1);
        assert(strcmp(p->faces[0].person_id, "user_001") == 0);
        assert(strcmp(p->faces[0].person_name, "Alice") == 0);
        assert(strcmp(p->faces[0].matched_face_id, "face_001") == 0);
        assert(p->faces[0].candidate_count == 1);
        assert(strcmp(p->faces[0].candidates[0].person_name, "Alice") == 0);
        assert(strcmp(p->faces[0].candidates[0].face_id, "face_001") == 0);

        /* 单次 free 释放整个 Arena 树 */
        free(res.internal_token);
    }

    printf("Plugin C ABI tests passed successfully!\n");
    return 0;
}
