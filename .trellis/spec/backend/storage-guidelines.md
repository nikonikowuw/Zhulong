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
