# 第三方 OpenAPI：人员与底库人脸同步对接指南

> 适用版本：Zhulong Edge Host v1.0+
> 适用场景：考勤、门禁、HR 等第三方业务系统通过机器间（M2M）通道自动化下发人员基本信息与底库人脸图片。

---

## 1. 架构与安全前置说明

1. **运行网络假设**：
   本系统为本地优先的边缘运维宿主，OpenAPI 接口**专供企业可信内部专网或安全受控局域网（LAN）**内的系统间通信使用。宿主默认采用明文 HTTP 协议提供本地服务，严禁直接将本接口暴露至公网。若跨广域网调用，必须前置 Nginx/Envoy 反向代理并配置 TLS 1.3 及网络访问控制列表（ACL）。

2. **认证方式**：
   所有 OpenAPI 端点强制采用 HTTP Bearer Token 认证：
   ```http
   Authorization: Bearer sk-<64位十六进制字符>
   ```
   - 令牌由系统管理员在 WebUI 控制台（「系统设置」->「API 访问凭据」）中生成并分发；
   - 系统中最多同时存在 10 个激活状态的凭据；
   - 缺失、伪造或已被吊销的凭据将收到 `401 Unauthorized` 响应，并附带响应头 `WWW-Authenticate: Bearer`。

3. **外部业务键锚点（`personId`）**：
   - 业务方与边缘宿主之间的唯一对齐标识是外部人员编号 `personId`（例如工号、学号、门禁卡号）；
   - `personId` 是**路径参数**，位于 `/persons/{personId}` 中；长度 1~64 字符，支持英文字符、数字、中划线和下划线，严禁包含路径与 URI 保留字符（`/ \ ? # %` 及控制字符）。含这些字符的 `personId` 必须按 RFC 3986 做百分号编码后放入路径，服务端按**解码后的值**精确匹配，不做大小写折叠、不做 Unicode 归一化；
   - `personId` 创建后永久不可变。本 API 不提供修改 `personId` 的端点——迁移业务键的正确做法是删除旧记录后重新创建；
   - 边缘宿主内部主键（Crockford Base32 编码的 ULID）对外严格隔离，绝不在 OpenAPI 契约中暴露。

4. **审计追踪**：
   第三方系统通过 Bearer Token 触发的所有创建、更新、注销和人脸录入操作，均会以 `api_token:<凭据备注名>` 作为操作人（Actor）完整记入安全审计日志。

---

## 2. 接口端点规范

所有 OpenAPI 接口统一挂载在 `/api/v1/openapi` 路由组下。统一响应遵循以下 JSON 信封：

```json
{
  "code": "OK",
  "message": "success",
  "data": { ... }
}
```

> 错误响应的 `data` 恒为 `null`；仅 HTTP 422 字段校验错误会额外携带 `details` 数组，逐字段给出 `field` / `code` / `message`。

### 2.1 录入/更新人员基本信息 (Upsert)

向边缘宿主同步人员信息。若 `personId` 不存在则自动创建；若已存在则更新姓名。该端点是**幂等**的：重复提交同名请求不会改写 `updatedAt`。

- **请求方法**：`POST`
- **请求路径**：`/api/v1/openapi/persons/{personId}`
- **Content-Type**：`application/json`（请求体上限 1 MiB）
- **请求体**：
  ```json
  {
    "name": "周星星"
  }
  ```
  `name` 必填，trim 后 1~64 个字符（按 Unicode 码点计）。
- **响应状态码**：
  - 新建成功：`201 Created`
  - 更新（含姓名未变化）：`200 OK`
- **响应体示例**：
  ```json
  {
    "code": "OK",
    "message": "Success",
    "data": {
      "personId": "EMP-9527",
      "name": "周星星",
      "primaryFaceId": null,
      "faces": [],
      "createdAt": "2026-11-04T12:00:00.123456Z",
      "updatedAt": "2026-11-04T12:00:00.123456Z"
    }
  }
  ```
  无论 201 还是 200，`data` 都是同一份完整投影：`faces` 恒为数组（无底库人脸时为 `[]`，不是 `null`）。

---

### 2.2 查询人员及底库人脸详情

- **请求方法**：`GET`
- **请求路径**：`/api/v1/openapi/persons/{personId}`
- **响应体示例**：
  ```json
  {
    "code": "OK",
    "message": "Success",
    "data": {
      "personId": "EMP-9527",
      "name": "周星星",
      "primaryFaceId": "01JBMX1234567890ABCDEFGHJK",
      "faces": [
        {
          "faceId": "01JBMX1234567890ABCDEFGHJK",
          "isPrimary": true,
          "createdAt": "2026-11-04T12:01:00.654321Z"
        }
      ],
      "createdAt": "2026-11-04T12:00:00.123456Z",
      "updatedAt": "2026-11-04T12:01:00.654321Z"
    }
  }
  ```
  所有时间字段为 UTC RFC3339Nano，恒以 `Z` 结尾。

---

### 2.3 上传/替换底库人脸图片

为指定人员录入或替换底库面档。支持追加或原子全量替换。

- **请求方法**：`POST`
- **请求路径**：`/api/v1/openapi/persons/{personId}/faces`
- **Query 参数**：
  - `replace`（可选，布尔）：原子替换模式。接受 `true`/`false`/`1`/`0`/`TRUE` 等 `strconv.ParseBool` 可识别的字面量（大小写不敏感）。**传入无法解析的值返回 `422 REPLACE_INVALID`**——不会静默退化成追加。
    - `replace=true`：本次录入成功后将**清空此人此前所有的旧底库人脸**并将此图置为主面档；若提取或落库失败，**旧人脸完整保留**，绝不会导致底库清空。
    - 缺省或 `replace=false`：追加一张新人脸。
- **Content-Type**：`multipart/form-data`（请求体上限 11 MiB）
- **表单字段**：
  - `image`：二进制图片文件（支持 JPEG、PNG、WebP，单张最大 10 MiB）。
  - *注：系统仅基于文件头魔数识别图片真实格式，无需客户端显式传递特定文件名或后缀扩展名（即 `curl -F "image=@photo"` 格式同样支持）。*
- **响应状态码**：`201 Created`
- **响应体示例**：
  ```json
  {
    "code": "OK",
    "message": "Success",
    "data": {
      "faceId": "01JBMX1234567890ABCDEFGHJK",
      "replaced": false,
      "isPrimary": true,
      "createdAt": "2026-11-04T12:01:00.654321Z"
    }
  }
  ```
  `replaced` 表示本次是替换（`true`）还是追加（`false`）；`isPrimary` 表示该人脸当前是否为主面档。

---

### 2.4 设置主面档

指定某张人脸为主面档图片（用于识别比对打卡展示）。该端点是幂等的。

- **请求方法**：`PUT`
- **请求路径**：`/api/v1/openapi/persons/{personId}/faces/{faceId}/primary`
- **响应状态码**：`200 OK`
- **响应体**：与 `GET /persons/{personId}` 的 `data` 完全一致（含完整 `faces` 列表）。

---

### 2.5 删除单张底库人脸

删除指定人员名下的单张人脸。若被删除的人脸恰好为主面档，边缘宿主将自动将此人剩余人脸中最早录入的一张提升为主面档。

- **请求方法**：`DELETE`
- **请求路径**：`/api/v1/openapi/persons/{personId}/faces/{faceId}`
- **响应状态码**：`200 OK`
- **响应体示例**：
  ```json
  {
    "code": "OK",
    "message": "Success",
    "data": {
      "deleted": true
    }
  }
  ```

---

### 2.6 清空人员所有底库人脸

清空指定人员名下的全部底库人脸，并立即向底层 AI 原生引擎同步清空该人员的特征底库。

- **请求方法**：`DELETE`
- **请求路径**：`/api/v1/openapi/persons/{personId}/faces`
- **响应状态码**：`200 OK`
- **响应体示例**：
  ```json
  {
    "code": "OK",
    "message": "Success",
    "data": {
      "cleared": 2
    }
  }
  ```
  `cleared` 为实际删除的人脸张数（幂等：已无脸时返回 `0`）。

---

### 2.7 物理删除人员档案

删除人员及其全部底库人脸，并从 AI 底库中注销。

- **请求方法**：`DELETE`
- **请求路径**：`/api/v1/openapi/persons/{personId}`
- **响应状态码**：`200 OK`
- **响应体示例**：
  ```json
  {
    "code": "OK",
    "message": "Success",
    "data": {
      "deleted": true
    }
  }
  ```
  重复删除返回 `404 PERSON_NOT_FOUND`（本 API 固定此语义）。

---

### 2.8 端点与幂等性总览

| 方法 | 路径 | 成功码 | 幂等 |
| :--- | :--- | :--- | :--- |
| `POST` | `/openapi/persons/{personId}` | 201 / 200 | 是 |
| `GET` | `/openapi/persons/{personId}` | 200 | 是 |
| `DELETE` | `/openapi/persons/{personId}` | 200 | 否（重复删除 404） |
| `POST` | `/openapi/persons/{personId}/faces` | 201 | 否（`?replace=true` 时收敛为是） |
| `DELETE` | `/openapi/persons/{personId}/faces` | 200 | 是（重复清空 `cleared: 0`） |
| `DELETE` | `/openapi/persons/{personId}/faces/{faceId}` | 200 | 否（重复删除 404） |
| `PUT` | `/openapi/persons/{personId}/faces/{faceId}/primary` | 200 | 是 |

---

## 3. 并发控制与限流防护 (Rate Limiting)

边缘设备（如 RK3568、Atlas 200I 等）由于板端 NPU 与 CPU 算力有限，且底层人脸特征抽取通道为严格的单协程串行模型。为保障边缘主机流媒体解码与实时 AI 分析的平稳运行，OpenAPI 对人脸录入端点实施了两级非阻塞限流：

1. **限流范围**：限流中间件**只修饰 `POST /persons/{personId}/faces`**。人员查询、改名、删除、清空人脸与单张删除等端点**不受限流影响**，保证在录入通道打满时逃生通道始终可用。
2. **限流额度**：
   - **全局并发上限**：`3`（整个边缘主机同时最多允许 3 路人脸图片上传与特征提取）；
   - **单 Token 并发上限**：`1`（单个调用方同时仅允许 1 路人脸上传提取进行中）。
3. **限流响应**：
   当并发超出额度时，接口会立即返回 `HTTP 429 Too Many Requests`，并且：
   - 附带响应头：`Retry-After: 1`
   - 响应信封：`code: "FACE_UPLOAD_CONCURRENCY_LIMITED"`
4. **调用方退避建议**：
   第三方系统在调用人脸上传接口时，**应在客户端实现单线程队列消费或使用连接池限制为 1 个并发连接**。遇到 429 响应时，应解析 `Retry-After` 头，结合**指数退避 + 随机抖动（Jitter）**机制延时重试，避免瞬间高频重试压垮边缘网关。

---

## 4. 完整错误代码与 HTTP 映射表

| HTTP 状态码 | 错误代码 (`code`) | 说明与处置建议 |
| :--- | :--- | :--- |
| **400** | `UNSUPPORTED_IMAGE_FORMAT` | 图片文件头魔数无法识别，或尺寸超过 4096px。仅支持标准 JPEG、PNG、WebP。 |
| **400** | `NO_FACE_DETECTED` | 算法未能在图片中检测到有效人脸，请提供清晰正面人脸图像。 |
| **401** | `UNAUTHORIZED` | 凭据缺失、格式错误、未通过校验或已被管理员吊销。响应附带 `WWW-Authenticate: Bearer`。 |
| **404** | `PERSON_NOT_FOUND` | 指定的 `personId` 不存在。请先调用 `POST /persons/{personId}` 录入人员。 |
| **404** | `FACE_NOT_FOUND` | 指定的人脸 `faceId` 不存在或不属于该人员。 |
| **409** | `FACE_LIMIT_EXCEEDED` | 该人员底库人脸已达最大上限（单人最多 10 张）。需先删除无用人脸或使用 `?replace=true`。 |
| **413** | `FILE_TOO_LARGE` | 上传的人脸图片单文件超过 10 MiB，或请求体超过 11 MiB 组限额。 |
| **413** | `PAYLOAD_TOO_LARGE` | JSON 端点请求体超过 1 MiB。 |
| **422** | `VALIDATION_FAILED` | 请求体字段级校验失败（如 `name` 为空或超长）。响应 `details` 逐字段给出原因。 |
| **422** | `REPLACE_INVALID` | `?replace=` 的取值无法解析为布尔值。 |
| **422** | `PERSON_ID_REQUIRED` | `personId` 为空或仅含空白字符。 |
| **422** | `PERSON_ID_TOO_LONG` | `personId` 超过 64 字符。 |
| **422** | `PERSON_ID_INVALID_CHARS` | `personId` 含有控制字符或 URI 保留字符（`/ \ ? # %`）。 |
| **422** | `FILE_REQUIRED` | 表单缺失 `image` 字段。 |
| **422** | `INVALID_FILE_TYPE` | 依据扩展名判定的格式不受支持（仅限携带可用扩展名的上传路径）。 |
| **422** | `MULTIPLE_FACES_DETECTED` | 图片中检测到多张人脸，底库人脸仅支持单人特写照。 |
| **500** | `DATABASE_ERROR` | 底库读写异常。请联系管理员检查边缘设备日志。 |
| **500** | `STORAGE_ERROR` | 人脸图片落盘失败（磁盘满或目录不可写）。 |
| **500** | `INTERNAL_ERROR` | 其他未分类的内部错误。请联系管理员检查边缘设备日志。 |

> 说明：`404` 与 `413` 的判定顺序是先校验请求体、再解析路径资源，因此「不存在的 personId + 超大图片」会先得到 `413`。

---

## 5. 快速调用示例 (cURL)

### 5.1 创建人员档案
```bash
curl -X POST http://192.168.1.100:8000/api/v1/openapi/persons/EMP-8080 \
  -H "Authorization: Bearer sk-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "张工"
  }'
```

### 5.2 上传底库人脸（普通追加）
```bash
curl -X POST http://192.168.1.100:8000/api/v1/openapi/persons/EMP-8080/faces \
  -H "Authorization: Bearer sk-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" \
  -F "image=@/path/to/zhang.jpg"
```

### 5.3 上传底库人脸（原子替换模式）
```bash
curl -X POST "http://192.168.1.100:8000/api/v1/openapi/persons/EMP-8080/faces?replace=true" \
  -H "Authorization: Bearer sk-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" \
  -F "image=@/path/to/new_zhang.png"
```

### 5.4 查询人员底库状态
```bash
curl -X GET http://192.168.1.100:8000/api/v1/openapi/persons/EMP-8080 \
  -H "Authorization: Bearer sk-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
```

### 5.5 设置主面档
```bash
curl -X PUT http://192.168.1.100:8000/api/v1/openapi/persons/EMP-8080/faces/01JBMX1234567890ABCDEFGHJK/primary \
  -H "Authorization: Bearer sk-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
```

### 5.6 清空全部底库人脸
```bash
curl -X DELETE http://192.168.1.100:8000/api/v1/openapi/persons/EMP-8080/faces \
  -H "Authorization: Bearer sk-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
```

### 5.7 删除人员档案
```bash
curl -X DELETE http://192.168.1.100:8000/api/v1/openapi/persons/EMP-8080 \
  -H "Authorization: Bearer sk-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
```

---

## 6. 文档与实现的单一真实源

本文档由 `internal/openapi` 的 Swagger 注解生成物（`internal/apidocs/swagger.yaml`）与端点实现共同约束，仓库内有一致性测试（`internal/openapi/contract_test.go`）在 CI 中比对以下三项，任一处漂移即失败：

1. `RegisterRoutes` 注册的路径与 HTTP 方法；
2. 各端点的成功状态码与响应 `data` 类型名；
3. `docs/openapi/person-face.md` 中出现的路径字面量。

因此本文档不接受手工添加未被实现覆盖的端点或字段。Swagger UI 挂载在 `/swagger/index.html`，可实时对照调试。
