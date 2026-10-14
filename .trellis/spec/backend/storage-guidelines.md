# 边缘媒体存储配置与动态生命周期管理规范 (Storage Guidelines)

> 异构边缘 Linux 设备（RK3588、Jetson、昇腾、工控机）媒体存储安全、statfs 水位感知、外挂盘防穿透与双水位回差清理规约。

---

## 1. 媒体根路径组织与防穿透安全保护 (Fallthrough Protection)

### 1.1 标准目录结构
所有落盘媒体文件严格组织在统一的媒体根路径（`MediaDirectory`）下，禁止散落各处：
- `recordings/{camera_id}/{YYYY-MM-DD}/{HH-MM-SS}.mp4`：视频流持续与切片录像；
- `snapshots/{camera_id}/{YYYY-MM-DD}/{event_id}.jpg`：AI 推理告警抓拍图；
- `exports/{task_id}.mp4`：用户临时导出视频剪辑（受 TTL 管理）。

### 1.2 外挂盘掉线防穿透保护 (Fallthrough Hazard)
- **设备号绑定**：系统启动或修改配置时，通过 Linux `unix.Stat` 获取路径所在分区的设备号 `st_dev`，并与系统根分区 `/` 的设备号对比。
- **掉线锁定**：若配置的路径原本为独立外挂存储（`st_dev != root_st_dev`），在运行时若检测到 `st_dev == root_st_dev`（即外挂盘异常掉电脱机，目录裸露退化为根分区上的普通空文件夹），**必须立即触发防穿透锁定**：
  1. `EmergencyGate.CanWrite()` 原子置为 `false`；
  2. 健康状态置为 `StatusError`，上报严重告警；
  3. **严禁向该目录写入任何视频**，杜绝 4K 视频填满 eMMC 系统根分区导致设备变砖。

### 1.3 路径热切流与无搬迁平滑过渡 (Hot-Switching & Zero-Copy Drain)
- 用户修改存储路径保存后，运行时指针原子切换至新路径，新视频流立刻写入新目录；
- 历史录像索引保持原有物理路径只读回放；
- 清理引擎在执行淘汰时，优先遍历并排空旧路径上的存量历史文件，直至旧盘彻底腾空；
- **禁止在后台执行 TB 级大文件跨盘拷贝**，防止磁盘 IO 拥塞导致全系统瘫痪。

### 1.4 客户端提供的路径引用必须做形态收敛

> **历史条目（形态收敛规则本身仍然有效）**：本节描述的是 API 直接接受客户端路径字符串时的校验要求。
> `POST /api/v1/persons/{id}/faces` 已**不再接受任何客户端路径**，见下方「人员底库登记照」条目；
> 但该模块保留的 `resolveFaceFilePath` 仍会对库内既有的绝对路径记录做同等的形态校验与根目录前缀断言。

当 API 接受客户端提供的媒体路径时，**即使该文件的落盘位置由别处管理，也必须校验路径形态**：

- **必须是绝对路径**（`filepath.IsAbs`）；
- **必须是已规范化路径**（`filepath.Clean(p) == p`）——这一条同时排除 `../` 穿越、`./`、`//` 与结尾分隔符；
- **拒绝 NUL 字节**；
- **长度有上限**（当前 512 字节），避免无界文本随每次列表响应回传。

**禁止把客户端路径原样入库**：任何将来"按该路径打开或返回文件"的消费者都会继承路径穿越面。若某模块确实需要把文件限定在某个根目录内，还必须在规范化之后追加**前缀断言**（`strings.HasPrefix(cleanPath, cleanRoot)`），与 native 侧 Zip-Slip 防御保持同样的纵深防御标准。

> **人员底库登记照：服务端绝对路径输入已彻底移除。** 该端点已改造为 `multipart/form-data` 文件直传（`POST /api/v1/persons/{id}/faces`），请求中**不存在** `imagePath` 字段。图片流经魔数 / `http.DetectContentType` 嗅探校验后由服务端写入底库目录（`config.toml` 的 `[person] faces_directory`，留空默认继承 `<data.directory>/faces`），数据库只持久化服务端生成的**相对文件名** `{person_id}_{face_id}.ext`，读取时动态拼装。
>
> ⚠️ **只读响应仍会回传该相对文件名**（`Face.imagePath`），前端不得把它当作可回传的输入；任何未来重新引入「客户端提供路径」的改动都必须回到本节规则。
>
> ⚠️ 遗留的 `INVALID_IMAGE_PATH` 错误码与 `imagePath must be a clean absolute path...` 文案描述的是**旧契约**：代码中 `resolveFaceFilePath` 现仅用于解析**服务端自己写入**的库内记录，其「必须绝对路径」分支已不可达（非法记录一律落入 `INVALID_IMAGE_PATH` 的逃逸分支）。修改该文案时不要照抄本节旧措辞。

---

## 2. 双水位滞后回差清理引擎 (Dual-Watermark Hysteresis Cleaner)

### 2.1 滞后回差算法 (Hysteresis)
- **高水位激活 (High Watermark)**：默认 90%（范围 50%~95%）。磁盘使用率触碰高水位时，后台清理协程自动唤醒；
- **低水位休眠 (Low Watermark)**：默认 80%（范围 40%~85%）。清理协程持续淘汰旧文件，直到磁盘使用率降低至低水位以下，协程安全休眠；
- **缓冲带保护**：低水位与高水位之间至少保留 5%~10% 的缓冲带，彻底消灭“刚降 0.1% 又反复唤醒清理”的磁盘 I/O 剧烈震荡。

### 2.2 三级阶梯级联淘汰 (Priority Cascade)
触碰高水位或手动触发清理时，必须严格按优先级顺序淘汰：
1. **第一级（过期临时导出）**：创建时间超过 `ExportsRetentionHours`（默认 48h）的 `exports/` 切片全量删除；
2. **第二级（超期常规录像）**：创建时间超过 `RecordingsRetentionDays`（默认 15 天）的常规视频文件；
3. **第三级（FIFO 阶梯淘汰）**：若容量仍高于低水位，按文件修改时间排序，由远及近 FIFO 淘汰最早的未加锁常规录像；
4. **铁律保护**：带有 AI 告警事件标记、或带有 `.lock` / `_locked` 保护标记的高价值证据录像与抓拍，**严禁提前被杀**。若常规录像已排空仍无法降至低水位，必须触发告警并停止进一步误杀。

### 2.3 批次步进流控与礼貌避让 (Throttled Batch Deletion)
- 避免批量 `os.Remove` 持续持有 ext4 `jbd2` 日志锁导致视频写盘丢帧；
- 每次删除一个 Batch（如 20~30 个文件）后，主动执行毫秒级让渡休眠（`time.Sleep(50ms)`）；
- 每批次让渡前重新检查 `isAboveLowWatermark()`，一旦落回低水位立即退出，杜绝过度删除。

---

## 3. 极限容量硬红线与系统熔断 (Emergency Write-Stop Gate)

### 3.1 熔断触发条件
任何视频写入协程在创建新切片前，必须调用 `storage.CanWrite() bool`：
- 使用率达到 `EmergencyStopPercent`（默认 95%）；或
- 磁盘可用空间低于 `EmergencyStopMinMB`（默认 2048 MB，2GB）；或
- 发生只读挂载或外挂盘掉线。

### 3.2 熔断后行为
- `CanWrite()` 立即返回 `false`，停止视频落盘写入；
- 记录 `STORAGE_EMERGENCY_STOP` 审计事件，状态切换为 `emergency_stopped`；
- 保护底层 SQLite WAL 数据库和操作系统关键进程免遭 `ENOSPC` 崩溃；
- 当清理协程或运维清理使可用空间恢复安全水位后，自动解除熔断并恢复录像。

---

## 4. 跨平台开发与编译隔离

- Linux 生产环境依赖 `golang.org/x/sys/unix`（`unix.Statfs` 与 `unix.Stat`），使用 `//go:build linux` 严格隔离；
- 非 Linux 宿主（macOS / Windows）提供 `inspector_fallback.go`（`//go:build !linux`），支持开发者离线编写代码与执行测试；
- 核心算法（`gate.go`, `cleaner.go`, `service.go`）依赖 `PathInspector` 接口，必须配合 Mock 探针进行 100% 确定性的并发与水位边界测试。

---

## 5. 媒体读取路径的掉盘穿透校验（读侧，与写侧熔断严格区分）

> 适用于 `internal/mediafile` 这类**只读**消费媒体根的服务。写侧的 `CanWrite()` 熔断规则见 §3，两者**不可互相复用**。

### 5.1 读侧必须放行的场景（易错点）

`CanWrite() == false` **不代表**读取应当被拒绝。以下场景读取必须放行：

- **空间不足熔断**：用户正是要审核旧证据，读被拒等于把设备上的数据锁死；
- **只读挂载**：只读传输本来就不写；
- **媒体根位于系统根文件系统**（默认配置 `MediaDirectory = "data/media"`）。

### 5.2 掉盘穿透判定：与「媒体根自采快照」比对

**禁止**写成 `stats.DeviceID != expectedDeviceID`。`storage.Service` 的 `expectedDeviceID` 是**未导出**字段，且**只在外挂盘场景才被赋值**（根文件系统时保持 `0`），因此该判定在默认配置下**恒真**，会把每一次读取都判为掉盘。

正确口径是服务自己采集快照：

```text
建/换句柄时：selfDev = 媒体根当时的 st_dev
请求期（仅建句柄时）：
    selfDev != rootDev 且 stats.DeviceID == rootDev  → 拒绝（外挂盘掉线，裸退到根分区）
    否则                                              → 放行
```

即：只有「原本不在根设备上，现在解析到了根设备」才是穿透。媒体根本来就在根设备上时**不做掉盘判定**。

⚠️ 该规则与 `service.go` 的 `RefreshStatus` dismount 判定**同构**，但**不得读取其私有状态**。

### 5.3 校验时机：只在建/换句柄时，不在请求热路径

`DefaultPathInspector.InspectFS` 会做 `MkdirAll` + `Stat` + `Statfs`，并经 `findMountInfoLinux` **每次 `os.Open("/proc/mounts")` 全行扫描**。一次视频预览会产生数十至数百次 Range 请求，把该校验放在热路径上是纯浪费（边缘设备 CPU 敏感）。

挂载/卸载必然使已持有的目录 fd 失效，因此「句柄仍可用」本身就是「设备未变」的强证据，建句柄时校验一次即可。

### 5.4 非 Linux 宿主必须跳过该校验

`storage` 仅在 Linux 实现真实遥测；非 Linux 是 `inspector_fallback.go` 的**合成常量**（device = 1、fstype = "simulated"）。若把合成设备号与 `os.Root.Stat` 返回的**真实** `st_dev` 相比，会在开发机上产生**假掉盘**并拒绝一切读取。用编译期常量（如 `deviceTelemetryIsReal`）门控，并把判定本身抽成纯函数以便在任意平台测试。

### 5.5 `st_dev` 比较只能用 `Dev` 字段

`syscall.Stat_t` 含嵌套数组（含时间戳纳秒字段），结构体 `==` 会**比较全部字段**，同一目录两次 `Stat` 结果为 `false`。必须只比 `Dev`。
