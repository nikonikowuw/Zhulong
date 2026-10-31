# SQLite 与 GORM 数据库开发规范

> SQLite 持久化、版本化迁移、连接参数与并发事务规则。

---

## 1. 核心铁律：版本化自动迁移 (Versioned Migrations)

1. **绝对禁止 GORM `AutoMigrate`**：生产环境严禁自动建表改表，避免历史数据破坏。
2. **应用启动自动迁移**：
   - 迁移脚本放置于 `internal/database/migrations/`（如 `000001_init.up.sql`），通过 `//go:embed` 打入二进制。
   - **在 Fx `OnStart` 阶段、对外监听流量和启动 Pipeline 之前执行迁移**。
   - ⚠️ **迁移失败必须阻断启动并退出**，严禁在损坏/未就绪的 Schema 上运行业务。
3. **零手动 SQL**：嵌入式设备禁止要求终端用户手动敲 SQL 脚本升级。

### 迁移演进规则（golang-migrate 按版本号记账）

`golang-migrate` 只在 `schema_migrations` 表里记录**版本号**，不校验文件内容。因此：

1. **已应用的迁移是只读的**：改写 `00000N_*.up.sql` 的正文，对已经跑过该版本的库**不会生效**，只会让文件与真实 Schema 静默不一致。这是最难排查的一类故障——单测每次都用全新临时库，永远全绿；现场库却缺列缺约束。此时必须**新增一个迁移**来补齐差异。
2. **未交付的迁移可以就地改**：仍在同一开发分支内、尚未发布到任何现场的迁移，直接改正文并**重置本地开发库**（删除 `<data-dir>/zhulong.db` 后重启）即可，不必为一个并不存在的旧库写补丁迁移。判断标准是"是否已经有人在跑它"，不是"文件是否已提交"。
3. **编号严格递增、不跳号复用**：`000001` 起连续递增，不足三位补零；废弃的版本号不得被新迁移复用。
4. **`up.sql` 与 `down.sql` 必须成对**：`down.sql` 按依赖逆序 `DROP`（先子表后父表）。
5. **表结构变更必须同步三处**：迁移 SQL、GORM 模型 tag、以及任务文档（`prd.md` / `design.md`）中的建表语句。文档落后于实现是缺陷，不是"注释问题"。

---

## 2. 连接池与 PRAGMA 配置

当前宿主将数据库文件命名为 `<data-dir>/zhulong.db`，DSN 使用 SQLite file URL，参数由 `sqlitePragmas()` 统一构造（生产 DSN 与测试断言共用同一来源，禁止在别处手写 PRAGMA 字符串）。GORM 连接池固定为一个 open/idle connection，避免本地单进程骨架打开多个写连接；GORM 日志级别为 Silent。

```go
// internal/database/store.go
const (
	sqliteBusyTimeoutMS = 5000
	sqlitePageCacheKiB  = 32768 // 负值单位为 KiB，即 32 MiB
)

func sqlitePragmas() url.Values {
	params := url.Values{}
	params.Set("_busy_timeout", strconv.Itoa(sqliteBusyTimeoutMS))
	params.Set("_foreign_keys", "on")
	params.Set("_journal_mode", "WAL")
	params.Set("_synchronous", "NORMAL")
	params.Set("_cache_size", strconv.Itoa(-sqlitePageCacheKiB))
	return params
}

db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
	Logger:  gormlog.Default.LogMode(gormlog.Silent),
	NowFunc: func() time.Time { return time.Now().UTC() },
})
sqlDB, err := db.DB()
sqlDB.SetMaxOpenConns(1)
sqlDB.SetMaxIdleConns(1)
```

迁移器另用 `database/sql` 打开迁移连接，并将连接数设为 1；它使用嵌入的 `iofs` SQL migrations 和 `golang-migrate` SQLite driver。迁移成功后才将 GORM Store 标记 Ready。迁移连接复用同一 DSN，因此也继承下面的 PRAGMA。

- **`journal_mode=WAL`**：允许并发读取与单写入者模式。
- **`busy_timeout=5000`**：SQLite 锁冲突等待最长 5 秒。
- **`foreign_keys=on`**：启用外键约束。
- **`synchronous=NORMAL`**：⚠️ **性能关键项，不得删除**。WAL 下 NORMAL 只在 checkpoint 时 fsync，进程崩溃或掉电不会损坏数据库文件，最坏丢失最近若干毫秒未 checkpoint 的事务。SQLite 默认的 `FULL` 会让**每次提交都 fsync 数据页与 WAL** —— 在 eMMC/SD 卡上（`fsync` 为毫秒级）这是把写入吞吐压到百 TPS 量级的头号原因，而不是 SQLite 本身的上限。
- **`cache_size=-32768`**：32 MiB 页缓存（负值单位为 KiB）。边缘设备需与 NPU 推理、视频缓冲共享内存，该值是按 RK3568（2-4 GiB）取的保守值；调大前先确认设备可用内存，**不要**在低内存板子上盲目设成 128 MiB 以上。

### PRAGMA 验证铁律

驱动会**静默忽略**不认识的 DSN 参数名（写错 `_sync_mode` 之类不会有任何报错）。因此修改或审视连接参数时必须读回实际生效值，`internal/database/store_test.go` 中的 `TestOpenAndMigrateAppliesConnectionPragmas` 承担这个守门职责：它断言 `journal_mode=wal`、`synchronous=1(NORMAL)`、`cache_size=-32768`、`foreign_keys=1`。**新增或调整 PRAGMA 时必须同步更新该测试**，只改 DSN 不改断言等于没有验证。

参考实测（2000 条 INSERT，桌面 NVMe；eMMC/SD 上 FULL 的惩罚更显著）：

| 配置 | 总耗时 | TPS | 相对改动前 |
| --- | --- | --- | --- |
| `FULL` + 单条提交 | 863 ms | 2 318 | 1× |
| `NORMAL` + 单条提交 | 69 ms | 28 797 | **12.4×** |
| `NORMAL` + 每批 100 条（当前路径） | 9 ms | 228 970 | **98.8×** |

结论前置：写入吞吐不足时，**先确认批量路径真的在被调用、PRAGMA 正确**，再考虑换数据库。`internal/record/service.go` 的高频写入必须走 `Create*Batch`（`CreateInBatches(_, 100)`），单条 `Create` 接口仅用于低频管理操作。


---

## 3. 时间与时区规范

- 所有表示绝对时刻的字段（如 `created_at`、`updated_at`）统一按 **UTC** 存储，Go 模型使用 `time.Time`；禁止保存依赖宿主本地时区的墙上时间。
- GORM 配置 `NowFunc` 返回 `time.Now().UTC()`；模型使用 `CreatedAt` / `UpdatedAt` 字段，让 GORM 在常规 Create / Updates 操作中维护时间戳。原始 SQL 写入或更新必须显式提供或更新时间。
- SQLite 的 `DATETIME` 列按 UTC 解释。SQLite `CURRENT_TIMESTAMP` 也是 UTC，但只有秒精度且不带时区后缀；需要亚秒精度时使用 GORM 写入的 UTC `time.Time`，不要依赖该默认值。
- 纯日期使用 `DATE` / `YYYY-MM-DD`，不做时区转换；持续时间使用带明确单位的数值字段（如 `duration_ms`），不要当作时间点存储。

### 感知记录与告警事件的时序主键契约 (Perception & Alarm Temporal Contract)

- **物理抓拍时间戳铁律 (`captured_at`)**：对于 `recognitions`、`alarms`、`captures` 等流式高频机器视觉感知事件，**业务主时间戳强制采用 `captured_at DATETIME NOT NULL`**，严格取自视频帧物理抓拍瞬间的绝对 UTC 时间戳（`InferenceResult.Timestamp` / `capture_time_ms`），坚决禁止使用推理完成或数据插入时的宿主墙上时间，彻底杜绝排队延迟导致的时序漂移；
- **时序索引设计**：所有基于相机的时间范围查询、降序列表与事件回放，核心索引统一建立为 `(camera_id, captured_at DESC, id DESC)`，全表降序列表用 `(captured_at DESC, id DESC)`（迁移 `000021`）。**排序键必须完整进索引**：只索引 `captured_at` 时 SQLite 会对每一页做 `USE TEMP B-TREE FOR LAST TERM OF ORDER BY`，带 `camera_id` 过滤时更退化成全表扫描 + 全量排序 —— 深翻页的成本全在这里。`internal/record/pagination_test.go` 用 `EXPLAIN QUERY PLAN` 断言计划里不出现 `TEMP B-TREE`，改列表 SQL 的 ORDER BY 时必须同步；
- **内部记账分离**：`created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP` 仅用于 SQLite 底层插入时间记账，不得用于业务回溯或前后端事件时间对账；
- **弱外键与快照冗余保护**：感知事件中的实体引用（如 `recognitions.person_id`）采用弱外键设计（`REFERENCES persons(id) ON DELETE SET NULL`），并强行冗余快照不可变字段（`person_name`、`person_external_id`、`gallery_image_path`），确保底库人员注销或底照变更后，已发生的历史证据链完好无损；
- **对外业务键与内部主键在记录表里必须分列存放（迁移 `000018` 起）**：
  - `recognitions.person_id` 只装内部代理主键 `persons.id`，承外键、级联与筛选；
  - `recognitions.person_external_id` 装**写入时快照**的对外业务键 `persons.person_id`，承上报对账；
  - ⚠️ **底库 IPC 载荷里的 `person_id` 是业务键原值，绝不能直接写进外键列**：只要人员的业务键 ≠ 内部主键，每次识别命中都会 `FOREIGN KEY constraint failed`，`CreateRecognitionBatch` 整批失败而 `record.Service` 只记日志 —— 表现为「识别记录静默消失」。归属必须经 `matched_face_id → person_faces.person_id → persons.id` 解析（`Store.ResolveGalleryFaces`），顺带以数据库为准刷新 `person_name` / `gallery_image_path`，免疫 runner 持有的陈旧底库；
  - 人员从未绑定业务键 → 快照为空串，该条记录**不可上报**，严禁回退成内部主键充当身份；
  - 业务键首次绑定（NULL → 值）时回填历史空快照（`PersonKeyBackfiller`，只写空串行，幂等），已赋值的业务键不可改写，所以补录不会造成同一记录的身份漂移。
- **相对路径存储**：图片统一存储相对路径且首段必须为 `snapshots/...`，绝不在数据库中存储绝对物理路径或裸 URL。
- **引用完整性的三条硬规则（迁移 `000014`/`000015` 起）**：
  1. **可空外键用 NULL 表达「未标注」，绝不用空串**：SQLite 的外键检查对空串一视同仁地要求父表存在匹配行，只有 NULL 才跳过校验。把「算法归属未知」写成 `''` 会让整条记录 insert 失败，而 `record.Service` 只记日志不返回错误 —— 表现就是记录静默消失。对外 API 仍以 `""` 呈现（`derefString`），内部必须是 `*string`。
  2. **`algorithm_id` 与 `person_id` 都用 `ON DELETE SET NULL` + 冗余快照**：证据是**已发生事实的记录**，它的价值不依赖于产生它的工具是否还装在本机。卸载算法是卸载工具，不是销毁历史。
     - `person_id` 冗余 `person_name` / `person_external_id` / `gallery_image_path`；
     - `algorithm_id` 冗余 `algorithm_name` / `algorithm_version`。三张感知表（`recognitions` / `alarms` / `captures`）**逐一具备**这三个字段 —— 迁移 `000015` 补齐前两者，`000016` 补齐 `captures`（它此前连 `algorithm_id` 列都没有，导致抓拍无法按算法归因）。
     - ⚠️ 只有 id 没有快照 = 卸载后「谁产生的」永久丢失；只有快照没有 id = 无法按算法筛选统计。**两者必须成对**。
     - ⚠️ 冗余值必须**写入时快照**，严禁改成读取时 join 回父表补全 —— 那正是算法/人员被删后数据消失的根因。
     - ⚠️ 迁移 `000014` 曾错误地把 `algorithm_id` 设成 `ON DELETE CASCADE`（理由「label 语义随包消失」），`000015` 已纠正。判据是：**「能不能解释」影响的是展示，不是存废**；无法解释的证据仍应保留并标注来源缺失。
  3. **`camera_id` 用 `ON DELETE CASCADE`**：与上条方向相反且**不可动摇** —— `image_path` 指向 `snapshots/{camera_id}/`，相机一删整棵子树无主，记录也无法再回答「画面从哪来」。判据：**卸工具不该毁证据，删对象来源则让证据失去物理载体**。同样地在 `camera/store.go` 里手动删记录是错误的，那是把外键约束的责任搬到业务层。

### 列表分页的读一致性 (Snapshot Reads)

- **count 与分页数据必须在同一个只读事务里**：`Store.listInReadSnapshot` 用 `db.Transaction` 包住 `COUNT(*)` 与 `LIMIT/OFFSET` 两条语句。WAL 模式下读事务在第一条语句处取快照，后续语句读同一版本；否则两条语句之间的持续写入（边缘设备上这是常态）会让 `total` 与当前页不自洽，表现为翻页时重复行或漏行。读事务不阻塞写者，代价只是一次 `BEGIN/COMMIT`。
- **不要用 `COUNT(*) OVER ()` 窗口函数代替**：窗口函数会把整个匹配集物化后才应用 `LIMIT`，等于每次列表查询都全表扫描；空页时还拿不到 `total`，仍要补一次 `COUNT`。
- **`Limit(0)` 不是「不返回数据」而是「不加 LIMIT」**：GORM 的这个语义会把整表读进内存。任何接受分页窗口的 store 方法都必须对 `PageSize <= 0` 做零值防御（回落 `httputil.DefaultPageSize`），不能指望调用方一定归一化过。

### 感知记录的保留清理归属 (Retention Ownership)

- **文件归 storage，行归 record**：`storage.CleanerEngine` 只删 `snapshots/` 下的**文件**，不碰 SQLite。告警图片带 `.lock` 标记，告警行不参加自动 TTL 清理。
- **普通行的删除前提**：`recognitions` / `captures` 只有在 `captured_at` 超过配置的 `snapshotsRetentionDays` 截止时间，且该行的所有快照引用（识别全幅/人脸图、抓拍全幅/裁剪图）都已不存在时，才由 `record.RetentionPruner` 删除。图片仍在则行保留，避免留下孤儿文件。
- **协调与恢复**：storage 每次成功清理文件后，app 层触发一次 record 对账；启动后的后台巡检以相同规则恢复中断或失败的对账。mediafile 的安全路径解析用于判断图片是否仍存在；无法读取配置或核验失败时不删除行。
- **依赖方向**：storage 不依赖 record，record 不直接删除文件；通过 app 注入保留期、文件检查器和清理后通知完成组合。
- **读不到保留期或检查器就不删**：provider 缺失、读配置报错、文件检查器缺失/报错时，`RetentionPruner` 安全空操作或返回错误 —— 宁可不删，也不猜测。
- **分批 + 让渡**：单批不超过 `DefaultRetentionBatchSize`（2000 行），批间让渡 20ms，避免长时间占用 WAL writer 拖慢 AI 结果落库（同连接池）。

## 4. 持久化规约与防爆边界

1. **业务自管 Store**：数据操作收敛在各模块 `internal/<module>/store.go`，禁止跨模块大表模型。
2. **强制绑定上下文**：所有 GORM 查询必须使用 `.WithContext(ctx)` 支持超时取消。
3. **短事务铁律**：事务内只允许纯数据操作，**绝对严禁在事务内部调用 CGO 推理接口或网络 RTSP 请求**，防止 SQLite 数据库被长时间独占锁死。
4. **对外业务键铁律**：凡是由外部系统用于关联本系统记录的业务键（如 `persons.person_id`），必须同时满足：
   - **可选可空**：外部业务键属于跨系统对接的桥梁，系统独立运行或未接入外部平台时允许为 `NULL`；禁止为了满足非空而拿内部代理主键偷梁换柱；
   - **条件唯一**：建条件 `UNIQUE` 索引（`WHERE col IS NOT NULL`），允许多行未配置外部键，已配置的确保全局唯一；
   - **一次性补录与不可变保护**：未绑定时允许补录绑定（从 NULL 写入值）；一旦赋值，则不得擅自改写，对非法变更返回显式冲突错误（如 409 `PERSON_ID_IMMUTABLE`）；
   - **与内部主键分离**：内部代理主键（`id`）负责系统内部实体关系（如外键关联与级联删除）与硬件底库原子索引，对外业务键仅作为业务对账元数据，二者不得互相混淆顶替。跨系统传输或底库同步时，未设置则使用空值或空串，**绝不得回退**到内部主键。
5. **实体代理主键统一 ULID 铁律**：
   - 凡声明为 `TEXT PRIMARY KEY` 的动态业务实体（如 `persons.id`、`person_faces.id`、`cameras.id`、`analysis_tasks.id`），在服务端未收到显式自定义主键时，**一律统一使用 `internal/idutil.New()` 生成纯 ULID**（26 字符 Crockford's Base32，48 位时间戳毫秒单调递增 + 80 位加密安全熵源）；
   - **严禁使用完全无序随机串**（如 `crypto/rand.Text()` 或 UUIDv4）：SQLite 表基于 B-Tree 组织，完全无序的字符串主键插入会导致高频叶子节点页分裂（Page Split）与磁盘随机写 I/O 放大；ULID 确保新记录始终追加在 B-Tree 最右侧叶子；
   - **严禁全表扫描伪自增**：严禁通过 `SELECT MAX(CAST(...))` 计算下一自增数字并持有应用层互斥锁；
   - **严禁伪语义拼接复合主键**：代理主键必须保持纯粹、不透明；实体间的约束与关联（如 `UNIQUE(camera_id, algorithm_id)`）交由唯一索引与外键保证，人类可读信息通过查询联合展示，严禁拼接形如 `task_{cam}_{algo}` 的超长混合串；
   - **内部高频追加表例外**：如 `audit_logs`、`camera_streams` 等纯内部流式追加表，继续使用 SQLite 原生 `INTEGER PRIMARY KEY AUTOINCREMENT`。

---

## 5. GORM 原生 Generics API (`gorm.G[T]`) 规范

项目持久化层全面采用 GORM 官方原生的 **Generics API (`gorm.G[T]`)**，消除旧式基于反射指针的语法啰嗦与运行时隐患。

### 核心守则

1. **坚持领域专属 Store，拒绝大一统泛型 Repository**：
   - 架构上依然保持 `type CameraStore interface`、`type UserStore interface` 等各领域的专属接口，由上层领域服务（Service）注入消费，严禁抽象无意义的 `BaseStore[T]`。
   - GORM 内部具体实现（`internal/<domain>/store.go`）全面使用 `gorm.G[Model](db)`。
2. **彻底杜绝 SQL 累加污染 (SQL Pollution)**：
   - `gorm.G[T]` 采用不可变构建器模型，链式调用每次返回全新 `chainG[T]` 且不污染原始 Statement。
   - 禁止在事务中为了防御子句污染而滥用 `tx.Session(&gorm.Session{})`；动态条件构造通过 `var g gorm.ChainInterface[T] = gorm.G[T](db).Scopes()` 优雅链式递推。
3. **强制显式上下文传播**：
   - `gorm.G[T]` 的所有终结方法（`First(ctx)`、`Find(ctx)`、`Create(ctx, ...)`、`Delete(ctx)`、`Update(ctx, ...)`）强制要求将 `ctx context.Context` 作为第一参数传入，必须从 HTTP/Service 入口全程透传，严禁传入 `context.Background()` 或遗漏超时约束。
4. **编译期强类型返回值**：
   - 单条查询 `m, err := gorm.G[Model](db).Where(...).First(ctx)` 直接返回强类型 `m (Model)`，无命中记录返回 `gorm.ErrRecordNotFound`，由 Store 统一使用 `errors.Is` 解包映射为领域 NotFound 错误；
   - 列表查询 `list, err := gorm.G[Model](db).Where(...).Find(ctx)` 直接返回 `[]Model`，对空切片防御归一化为 `[]Model{}`，严禁返回 `nil` 切片以防序列化为 JSON `null`。
5. **事务与高级子句范式**：
   - 在 `db.Transaction(func(tx *gorm.DB) error { ... })` 内部直接传递 `tx`：`gorm.G[Model](tx)`；
   - 冲突子句（`clause.OnConflict`）、查询提示（`hints`）直接作为变长表达式传入构造：`gorm.G[Model](db, clause.OnConflict{...})`；
   - 带乐观锁（`Revision`）的行级锁查询统一使用：`gorm.G[Camera](tx, clause.Locking{Strength: "UPDATE"}).Where(...).First(ctx)`。
6. **动态 Map 更新取舍**：
   - 强类型实体更新或单列更新优先使用 `gorm.G[Model](db).Where(...).Update(ctx, "col", val)` 或 `.Updates(ctx, model)`；
   - 对于包含 `gorm.Expr`（如自增/CASE WHEN 补丁）或带动态空值排除逻辑的复杂字典更新，保持 `tx.Model(&Model{}).Where(...).Updates(updates)`，确保 SQLite 表达式求值与字段选择语义清晰可控。

