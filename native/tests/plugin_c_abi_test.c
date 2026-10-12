#include <zhulong/plugin.h>
#include <stdio.h>
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
    assert(sizeof(zhulong_bbox) >= 64 + 5 * sizeof(float) + sizeof(int32_t));
    assert(sizeof(zhulong_frame_view) > 0);
    assert(sizeof(zhulong_inference_result) > 0);
    assert(sizeof(zhulong_plugin_api_v1) > 0);

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

    printf("Plugin C ABI tests passed successfully!\n");
    return 0;
}
