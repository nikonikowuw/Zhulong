-- 按依赖逆序删除：先子表（gb_channels）后父表（gb_devices），最后配置单例。
DROP INDEX IF EXISTS idx_gb_channels_camera;
DROP INDEX IF EXISTS idx_gb_channels_device;
DROP TABLE IF EXISTS gb_channels;

DROP INDEX IF EXISTS idx_gb_devices_state;
DROP TABLE IF EXISTS gb_devices;

DROP TABLE IF EXISTS gb28181_configs;
