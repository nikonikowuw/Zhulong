-- GB28181 接入（P0）：运行配置单例、设备 allowlist、设备侧通道清单。
--
-- 三张表的分工是硬边界：
--   gb28181_configs  唯一运行配置事实源（不放 config.toml，避免两处互相覆盖）
--   gb_devices       设备 allowlist + Digest 凭据密文；remote_addr 在首次 REGISTER 前为 NULL
--   gb_channels      设备侧原始目录；用户显式「添加为摄像机」后才回填 camera_id
--
-- 通道与摄像机的关系用 ON DELETE SET NULL 表达：删掉摄像机不该毁掉设备清单
-- （清单是设备上报的事实），但也不能留下指向不存在摄像机的 camera_id。
-- 设备删除则级联删通道：通道无法脱离所属设备存在。
CREATE TABLE gb28181_configs (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    sip_id TEXT NOT NULL DEFAULT '',
    sip_realm TEXT NOT NULL DEFAULT '',
    sip_listen TEXT NOT NULL DEFAULT '0.0.0.0:5060',
    sip_transport TEXT NOT NULL DEFAULT 'both' CHECK (sip_transport IN ('udp', 'tcp', 'both')),
    security_profile TEXT NOT NULL DEFAULT 'standard' CHECK (security_profile = 'standard'),
    media_bind_address TEXT NOT NULL DEFAULT '0.0.0.0',
    media_advertised_ip TEXT NOT NULL DEFAULT '',
    -- 每路会话占 RTP + RTCP 两个端口，因此端口范围决定并发点播上限：
    -- floor((max - min + 1) / 2)。默认 101 个端口 → 50 路。
    rtp_port_min INTEGER NOT NULL DEFAULT 30000 CHECK (rtp_port_min BETWEEN 1 AND 65535),
    rtp_port_max INTEGER NOT NULL DEFAULT 30100 CHECK (rtp_port_max BETWEEN 1 AND 65535),
    require_registered_device INTEGER NOT NULL DEFAULT 1 CHECK (require_registered_device = 1),
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (rtp_port_min <= rtp_port_max)
);
INSERT OR IGNORE INTO gb28181_configs (id) VALUES (1);

CREATE TABLE gb_devices (
    device_id TEXT PRIMARY KEY NOT NULL CHECK (length(device_id) = 20 AND device_id NOT GLOB '*[^0-9]*'), -- 20 位国标编码
    name TEXT NOT NULL DEFAULT '',
    manufacturer TEXT,
    model TEXT,
    firmware TEXT,
    transport TEXT NOT NULL DEFAULT 'udp' CHECK (transport IN ('udp', 'tcp')),
    -- 首次 REGISTER 前为 NULL：预置 allowlist 时远端地址未知，
    -- 之后以实际来源/Contact 更新，兼容 NAT。
    remote_addr TEXT,
    -- 下界与 API 校验一致（expiresSeconds 至少 60），避免绕过应用层写入不可用值。
    expires_seconds INTEGER NOT NULL DEFAULT 3600 CHECK (expires_seconds >= 60),
    -- 应用层只会写入 offline/online；CHECK 是最后一道，防未来代码写入未知状态。
    state TEXT NOT NULL DEFAULT 'offline' CHECK (state IN ('offline', 'online')),
    last_keepalive_at DATETIME,
    registered_at DATETIME,
    -- 复用 camera 的 AES-256-GCM Cipher，AAD = v1:gb:<device_id>。
    -- 密文列存在即代表「camera.key 不可丢」，启动时的密钥完整性检查必须覆盖它。
    encrypted_password BLOB,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX idx_gb_devices_state ON gb_devices(state);

CREATE TABLE gb_channels (
    device_id TEXT NOT NULL REFERENCES gb_devices(device_id) ON DELETE CASCADE,
    channel_id TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    manufacturer TEXT,
    model TEXT,
    owner TEXT,
    civil_code TEXT,
    address TEXT,
    parental INTEGER NOT NULL DEFAULT 0,
    parent_id TEXT,
    safety_way INTEGER,
    register_way INTEGER,
    ptz_type INTEGER,
    resolution_width INTEGER,
    resolution_height INTEGER,
    camera_id TEXT REFERENCES cameras(id) ON DELETE SET NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (device_id, channel_id)
);
CREATE INDEX idx_gb_channels_device ON gb_channels(device_id);
CREATE INDEX idx_gb_channels_camera ON gb_channels(camera_id);
