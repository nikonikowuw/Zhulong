-- 主机身份配置（单行表）。
--
-- 只放「会随现场变化、又必须被运行时改动」的主机标识：device_id 是这台边缘主机
-- 对外业务身份，会写进每一条告警/识别推送载荷，让汇聚平台知道「这条事件来自哪个盒子」。
-- 不放 config.toml：那里是启动期只读配置，改一次要重启；设备编号属于现场运维,
-- 必须能在 Web UI 里改完即时生效。
CREATE TABLE IF NOT EXISTS system_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    device_id TEXT NOT NULL DEFAULT '',
    -- device_id_auto = 1 表示当前值是系统首次启动时自动生成的占位编号，
    -- 一旦运维手工保存过就置 0，UI 据此显示「自动生成 / 自定义」。
    device_id_auto BOOLEAN NOT NULL DEFAULT 1,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT OR IGNORE INTO system_settings (id) VALUES (1);
