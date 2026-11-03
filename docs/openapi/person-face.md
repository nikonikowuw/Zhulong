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
   - `personId` 长度 1~64 字符，支持英文字符、数字、中划线和下划线，严禁包含路径与 URI 保留字符（`/ \ ? # %` 及控制字符）；
   - **`personId` 创建后永久不可变**。任何尝试修改 `personId` 的请求将被拒绝并返回 `409 PERSON_ID_IMMUTABLE`；
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

### 2.1 录入/更新人员基本信息 (Upsert)

向边缘宿主同步人员信息。若 `personId` 不存在则自动创建；若已存在则更新姓名。

- **请求方法**：`POST`
- **请求路径**：`/api/v1/openapi/persons`
- **Content-Type**：`application/json`（体量限制 1 MiB）
- **请求体**：
  ```json
  {
    "personId": "EMP-9527",
    "name": "周星星"
  }
  ```
- **响应状态码**：
  - 新建成功：`201 Created`
  - 更新成功：`200 OK`
- **响应体示例**：
  ```json
  {
    "code": "OK",
    "message": "success",
    "data": {
      "personId": "EMP-9527",
      "name": "周星星",
      "source": "API",
      "sourceName": "DoorAccess-System",
      "createdAt": "2026-11-04T12:00:00Z",
      "updatedAt": "2026-11-04T12:00:00Z",
      "faceCount": 0,
      "faces": []
    }
  }
  ```

---

### 2.2 查询人员及底库人脸详情

查询指定外部编号的人员档案及其底库人脸图片列表。

- **请求方法**：`GET`
- **请求路径**：`/api/v1/openapi/persons/:personId`
- **响应体示例**：
  ```json
  {
    "code": "OK",
    "message": "success",
    "data": {
      "personId": "EMP-9527",
      "name": "周星星",
      "source": "API",
      "sourceName": "DoorAccess-System",
      "createdAt": "2026-11-04T12:00:00Z",
      "updatedAt": "2026-11-04T12:00:00Z",
      "faceCount": 1,
      "faces": [
        {
          "faceId": "01JBMX1234567890ABCDEFGHJK",
          "isPrimary": true,
          "createdAt": "2026-11-04T12:01:00Z"
        }
      ]
    }
  }
  ```

---

### 2.3 上传/替换底库人脸图片

为指定人员录入或替换底库面档。支持追加或原子全量替换。

- **请求方法**：`POST`
- **请求路径**：`/api/v1/openapi/persons/:personId/faces`
- **Query 参数**：
  - `replace=true`（可选）：原子替换模式。若设置，本次录入成功后将**清空此人此前所有的旧底库人脸**并将此图置为主面档；若提取或落库失败，**旧人脸完整保留**，绝不会导致底库清空。
- **Content-Type**：`multipart/form-data`（上传上限 11 MiB）
- **表单字段**：
  - `image`：二进制图片文件（支持 JPEG、PNG、WebP，单张最大支持 10 MiB）。
  - *注：系统仅基于文件头魔数识别图片真实格式，无需客户端显式传递特定文件名或后缀扩展名（即 `curl -F "image=@photo"` 格式同样支持）。*
- **响应状态码**：`201 Created`
- **响应体示例**：
  ```json
  {
    "code": "OK",
    "message": "success",
    "data": {
      "faceId": "01JBMX1234567890ABCDEFGHJK",
      "isPrimary": true,
      "replacedOldFaces": 0
    }
  }
  ```

---

### 2.4 设置主面档

指定某张人脸为主面档图片（用于识别比对打卡展示）。

- **请求方法**：`POST`
- **请求路径**：`/api/v1/openapi/persons/:personId/faces/:faceId/primary`
- **响应状态码**：`200 OK`

---

### 2.5 删除单张底库人脸

删除指定人员名下的单张人脸。若被删除的人脸恰好为主面档，边缘宿主将自动将此人剩余人脸中最早录入的一张提升为主面档。

- **请求方法**：`DELETE`
- **请求路径**：`/api/v1/openapi/persons/:personId/faces/:faceId`
- **响应状态码**：`200 OK`
- **响应体示例**：
  ```json
  {
    "code": "OK",
    "message": "success",
    "data": {
      "deleted": true
    }
  }
  ```

---

### 2.6 清空人员所有底库人脸

清空指定人员名下的全部底库人脸，并立即向底层 AI 原生引擎同步清空该人员的特征底库。

- **请求方法**：`DELETE`
- **请求路径**：`/api/v1/openapi/persons/:personId/faces`
- **响应状态码**：`200 OK`
- **响应体示例**：
  ```json
  {
    "code": "OK",
    "message": "success",
    "data": {
      "clearedCount": 2
    }
  }
  ```

---

### 2.7 物理删除人员档案

删除人员及其全部底库人脸，并从 AI 底库中注销。

- **请求方法**：`DELETE`
- **请求路径**：`/api/v1/openapi/persons/:personId`
- **响应状态码**：`200 OK`
- **响应体示例**：
  ```json
  {
    "code": "OK",
    "message": "success",
    "data": {
      "deleted": true
    }
  }
  ```

---

## 3. 并发控制与限流防护 (Rate Limiting)

边缘设备（如 RK3568、Atlas 200I 等）由于板端 NPU 与 CPU 算力有限，且底层人脸特征抽取通道为严格的单协程串行模型。为保障边缘主机流媒体解码与实时 AI 分析的平稳运行，OpenAPI 实施了两级非阻塞限流：

1. **限流额度**：
   - **全局并发上限**：`3`（整个边缘主机同时最多允许 3 路人脸图片上传与特征提取）；
   - **单 Token 并发上限**：`1`（单个调用方同时仅允许 1 路人脸上传提取进行中）。
2. **限流响应**：
   当并发超出额度时，接口会立即返回 `HTTP 429 Too Many Requests`，并且：
   - 附带响应头：`Retry-After: 1`
   - 响应信封：`code: "TOO_MANY_REQUESTS"`
3. **调用方退避建议**：
   第三方系统在调用人脸上传接口时，**应在客户端实现单线程队列消费或使用连接池限制为 1 个并发连接**。遇到 429 响应时，应解析 `Retry-After` 头，结合**指数退避 + 随机抖动（Jitter）**机制延时重试，避免瞬间高频重试压垮边缘网关。

---

## 4. 完整错误代码与 HTTP 映射表

| HTTP 状态码 | 错误代码 (`code`) | 说明与处置建议 |
| :--- | :--- | :--- |
| **400** | `INVALID_ARGUMENT` | 请求参数缺失或格式错误（例如 `personId` 包含非法字符、姓名为空或超长）。 |
| **401** | `UNAUTHORIZED` | 凭据缺失、格式错误、未通过校验或已被管理员吊销。响应附带 `WWW-Authenticate: Bearer`。 |
| **404** | `PERSON_NOT_FOUND` | 指定的 `personId` 不存在。请先调用 `POST /persons` 录入人员。 |
| **404** | `FACE_NOT_FOUND` | 指定的人脸 `faceId` 不存在或不属于该人员。 |
| **409** | `PERSON_ID_IMMUTABLE` | 禁止修改外部人员标识 `personId`。 |
| **409** | `FACE_LIMIT_EXCEEDED` | 该人员底库人脸已达最大上限（单人最多 10 张）。需先删除无用人脸或使用 `?replace=true`。 |
| **413** | `FILE_TOO_LARGE` | 上传的人脸图片单文件超过 10 MiB（请求体超过 11 MiB 阈值）。 |
| **413** | `PAYLOAD_TOO_LARGE` | JSON 接口请求体超过 1 MiB。 |
| **422** | `INVALID_FILE_TYPE` | 上传的文件不是支持的图片类型。 |
| **422** | `UNSUPPORTED_IMAGE_FORMAT` | 图片文件头魔数解析失败或损坏。仅支持标准 JPEG、PNG、WebP 格式。 |
| **422** | `NO_FACE_DETECTED` | 算法未能在图片中检测到有效人脸，请提供清晰正面人脸图像。 |
| **422** | `MULTIPLE_FACES_DETECTED` | 图片中检测到多张人脸，底库人脸仅支持单人特写照。 |
| **429** | `TOO_MANY_REQUESTS` | 触发并发阈值（全局 3 或单 Token 1）。请依据 `Retry-After: 1` 进行退避重试。 |
| **500** | `INTERNAL_SERVER_ERROR` | 边缘宿主内部错误（如持久化异常）。请联系管理员检查边缘设备日志。 |

---

## 5. 快速调用示例 (cURL)

### 5.1 创建人员档案
```bash
curl -X POST http://192.168.1.100:8000/api/v1/openapi/persons \
  -H "Authorization: Bearer sk-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" \
  -H "Content-Type: application/json" \
  -d '{
    "personId": "EMP-8080",
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
