# 执行与验证

1. Go区域契约校验与测试。
2. 前端绊线schema/草稿/撤销/画布/列表/三语，浏览器测试。
3. 宿主区域provider与广播、IPC枚举、Broker按算法缓存/重放、SDK视图与帧窗口；跨层测试。
4. SDK文档与spec同步，定向检查，make native-build，make check。

仅修改所需文件，保留已存在硬件/音频改动。不自动提交。失败记录证据，不宣称未运行的硬件验证。

## 完成与验证记录

- [x] Go区域模型、前后端绊线校验、稳定ID与方向。
- [x] 绘制、端点拖动/键盘调整、名称与方向、删除、撤销、切换算法、三语。
- [x] 独立区域消息、按算法缓存重放、SDK只读JSON/revision与实际帧窗口转换。
- [x] 真实SDK子进程读取测试；SDK版本1.2.0、契约文档。
- [x] 任务前端17文件180测试通过；最终区域schema定向复验通过。
- [x] native全量36测试通过；最后停机清缓存变更后FrameBroker/MotionGate复验通过。
- [x] make pkg-drift-check、api-docs、go-check通过（全仓vet/race与真实Go桥接）。
- [x] 前端format/lint/build通过；内嵌前端同步并检查。
- [!] make check执行后被前端阻塞：既有live-audio-player的“audioWorklet缺失→降级且不留下悬空AudioContext”断言失败，以及全量浏览器iframe连接/动态加载异常。保留用户音频改动，未扩大修复范围；后续门禁已单独执行。

没有实现计数/越线判定；未执行交叉编译、真实板端或sdk-clean-check。未提交。
