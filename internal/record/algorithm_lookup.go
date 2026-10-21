package record

import (
	"context"
	"sync"
	"time"

	"gorm.io/gorm"
)

// AlgorithmSnapshot 是算法身份在感知记录里的冗余快照。
//
// 为什么不存引用而是快照：算法包可以被卸载，卸载后 algorithms 行消失，
// 而历史证据必须继续回答「这批告警是哪个算法产生的」。这与 person_name /
// gallery_image_path 冗余人脸底库信息的理由完全一致（database-guidelines §63）。
type AlgorithmSnapshot struct {
	ID      string
	Name    string
	Version string
}

// algorithmCacheTTL 是算法身份快照的缓存有效期。
//
// 抓拍/告警是高频事件，每条记录都查一次 algorithms 表属于无谓放大（一次
// 1080p 检测结果可能含十几个目标）。而算法身份几乎不变（只在安装/升级/
// 卸载时变化），因此用短 TTL 缓存即可。
//
// ⚠️ TTL 必须短：算法升级会改 name/version，缓存太久会让升级后头几分钟的
// 记录打上旧版本号。5 秒远小于任何有意义的观察窗口。
const algorithmCacheTTL = 5 * time.Second

// AlgorithmResolver 把算法 ID 解析成可冗余写入的名称与版本。
//
// 设计取舍：放在 record 域内直接读 algorithms 表，而不是注入 algorithm.Service。
// 理由是这个查询只取三个不可变列（id/name/version），不触发任何业务逻辑；
// 反过来注入 algorithm 域会让 record 依赖算法监督者的生命周期顺序，把两个
// 本可独立启停的域绑死。
type AlgorithmResolver struct {
	store *Store

	mu       sync.RWMutex
	cached   map[string]AlgorithmSnapshot
	cachedAt time.Time
}

// NewAlgorithmResolver 构造算法身份解析器。纯构造函数，不查库。
func NewAlgorithmResolver(store *Store) *AlgorithmResolver {
	return &AlgorithmResolver{store: store}
}

// resolveAlgorithmSnapshots 批量解析算法 ID → 快照。
//
// 返回的 map 中缺失的 key 表示「该 ID 不对应任何在册算法」：调用方应写入
// 空名称而不是报错 —— 记录本身必须落库，来源标注的完整性是次级信息。
func (r *AlgorithmResolver) resolveAlgorithmSnapshots(ctx context.Context, ids []string) map[string]AlgorithmSnapshot {
	result := make(map[string]AlgorithmSnapshot, len(ids))
	if r == nil || r.store == nil || len(ids) == 0 {
		return result
	}

	// 先取缓存
	r.mu.RLock()
	fresh := time.Since(r.cachedAt) < algorithmCacheTTL && r.cached != nil
	cached := r.cached
	r.mu.RUnlock()

	missing := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if fresh {
			if snap, ok := cached[id]; ok {
				result[id] = snap
				continue
			}
		}
		missing = append(missing, id)
	}

	if len(missing) == 0 {
		return result
	}

	// 缓存未命中：查库（只取不可变的三列）
	db, err := r.store.db(ctx)
	if err != nil {
		return result
	}
	var rows []struct {
		ID      string
		Name    string
		Version string
	}
	if err := db.Table("algorithms").
		Select("id, name, version").
		Where("id IN ?", missing).
		Scan(&rows).Error; err != nil && err != gorm.ErrRecordNotFound {
		return result
	}

	// 刷新缓存：把本次查到的结果并入（刻意不缓存「查不到的 ID」，
	// 否则刚安装的算法会被持续判为不存在直到 TTL 过期）
	r.mu.Lock()
	next := make(map[string]AlgorithmSnapshot, len(cached)+len(rows))
	if fresh {
		for k, v := range cached {
			next[k] = v
		}
	}
	for _, row := range rows {
		snap := AlgorithmSnapshot{ID: row.ID, Name: row.Name, Version: row.Version}
		next[row.ID] = snap
		result[row.ID] = snap
	}
	r.cached = next
	r.cachedAt = time.Now()
	r.mu.Unlock()

	return result
}
