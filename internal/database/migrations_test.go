package database

import (
	"io/fs"
	"regexp"
	"testing"
)

var migrationNamePattern = regexp.MustCompile(`^(\d{6})_(.+)\.(up|down)\.sql$`)

// TestMigrationFilesArePairedAndUnique 钉死迁移目录的三条结构性约定：
//
//  1. 每个版本号恰有一对 .up.sql / .down.sql；
//  2. 版本号唯一——同名不同编号、不同名同编号都会被 golang-migrate 静默踩平；
//  3. 文件名只能是 6 位补零的 NNNNNN_name.(up|down).sql。这条同时保证目录遍历顺序
//     等于版本序，所以不需要再单独比较文件名与版本号的排序。
//
// 版本号**不要求连续**：仓库里存在 000026 空洞，断言连续性会让正常的历史缺口把构建卡死。
func TestMigrationFilesArePairedAndUnique(t *testing.T) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}

	type pair struct {
		up   bool
		down bool
	}

	pairs := make(map[string]*pair)  // key = version + "_" + name
	owner := make(map[string]string) // version -> key，用于抓版本号被两个名字复用
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := migrationNamePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			t.Fatalf("unexpected migration filename %q (want NNNNNN_name.(up|down).sql)", entry.Name())
		}
		version, name, direction := match[1], match[2], match[3]
		key := version + "_" + name

		item := pairs[key]
		if item == nil {
			if other, exists := owner[version]; exists {
				t.Fatalf("migration version %s is used by both %q and %q", version, other, key)
			}
			owner[version] = key
			item = &pair{}
			pairs[key] = item
		}

		switch direction {
		case "up":
			if item.up {
				t.Fatalf("duplicate up migration for %s", key)
			}
			item.up = true
		case "down":
			if item.down {
				t.Fatalf("duplicate down migration for %s", key)
			}
			item.down = true
		}
	}

	if len(pairs) == 0 {
		t.Fatal("no migrations found")
	}
	for key, item := range pairs {
		if !item.up || !item.down {
			t.Fatalf("migration %s must have both .up.sql and .down.sql (up=%v down=%v)",
				key, item.up, item.down)
		}
	}
}
