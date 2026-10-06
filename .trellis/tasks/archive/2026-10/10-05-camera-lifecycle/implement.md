# 实施计划

## 0. 规划门禁

- [x] 用户批准 Native 媒体证据/失败流重启纳入本子任务。
- [x] 完成 Native/Go 源码规划复核，N1–N8 修正写入 design.md 第 18 节（不是运行验收）。
- [x] 整体审阅新增 ABI/代次合同及必要的旧 wrapper 存活期修正（第 13/16/18 节）。
- [x] 用户批准 icholy/digest v1.2.0 低层 API + 项目协议边界适配路线：保留空 opaque、参数规范化，摘要算法由依赖承担；不使用 HTTP Transport，不引入 libcurl。
- [x] 完成所选依赖模块来源/校验链与最终 pin；源码及许可证研究已记录，网络行为需实施阶段以 RTSP 桩验证。
- [x] 用户确认健康/会话状态拆分、主子流整体验证、未知 FPS 策略、密钥恢复与活跃流检测默认行为；已同步父 PRD。
- [x] 规划全部收敛后获取用户实现批准，再 task.py start。

## 1. 持久化与凭据

- [x] 公共源引用、协议配置与 RTSP adapter 分离；假 adapter 验证无 URL 的源可表达，生产拒绝未实现协议。
- [x] 健康管理允许主动检查和事件更新，不将 DESCRIBE、RTSP URL 或固定轮询写进通用业务合同；按 design.md 第 14 节验收。
- [x] 复核最新迁移版本，增加 cameras/camera_streams 的版本化 up/down SQL 与 GORM Store。
- [x] 实现密钥初始化/加载、AES-GCM、格式版本和 AAD；覆盖丢失密钥、错误密钥、篡改密文、并发创建及权限测试。
- [x] 实现结构化输入与无歧义 URL 解析，脱敏错误；事务内不访问网络。

## 2. 探测与调度

- [x] 轻量 DESCRIBE 适配器、鉴权、超时/大小/重定向约束，RTSP 桩证明不发 SETUP/PLAY。
- [x] 按 design.md 第 15 节落实已批准的 Digest 边界适配；独立参考向量覆盖完整 URI、qop、空/缺失/非空 opaque、参数大小写/空白、quoted-pair、stale 与防降级；显式 CSPRNG cnonce，不自写摘要算法。
- [x] 深度 Probe 适配器、主子流共享预算、配置 revision CAS；超时/取消不入库。
- [x] 公平有界调度、去重、抖动、失败阈值、stale/overdue 与停用删除取消。
- [x] 正交状态模型管理（enabled + health + session），活跃流 4s 收包超时检测。

## 3. HTTP、SSE 与装配

- [x] CRUD/诊断 DTO、认证、错误码三语翻译与 Swagger。
- [x] SSE 快照无缝衔接增量、有界背压、会话失效、写超时与停机退出。
- [x] Fx 装配，失败逆序清理；HTTP/SSE 排空后才能释放 Native/DB。

## 4. 验证和回滚门禁

- [x] make go-check（包含 race 与桥接集成桩）。
- [x] 若批准 Native 扩展：make native-test；纯 C ABI 测试和无回调健康查询、共享源重启隔离测试。
- [x] make api-docs && make check && make smoke；只报告实际运行结果。
- [x] 最终完整范围检查、规范同步判断、用户确认提交计划后提交；不包含既存 engine.hpp 改动。

```bash
# 仓库根目录；使用项目 Make 与静态 Native 构建工具链
make go-check
make native-test
make api-docs
make check
make smoke
```

回滚：数据库和密钥先一致性备份；破坏性 down 仅限测试或用户明确授权，不因失败自动删表。新后台启动失败阻断服务并清理资源，不以空密钥重新加密。Native ABI 变更须同批更新所有调用方和测试。目标板验收依然属于 Native 子任务，不由宿主结果代替。
