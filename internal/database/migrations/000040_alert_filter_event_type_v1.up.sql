-- 000040_alert_filter_event_type_v1.up.sql
-- 告警推送载荷 v1 将外部事件类型统一为短名（与载荷子对象同名），
-- 推送目标过滤器中已存储的旧名同步改写。
-- 只改写 $.event_types 数组：algorithm_ids / camera_ids 里即便出现同名字符串也不受影响。
UPDATE alert_destinations
SET filter_json = json_set(
        filter_json,
        '$.event_types',
        (
            SELECT json_group_array(mapped)
            FROM (
                SELECT CASE value
                           WHEN 'face_recognition' THEN 'face'
                           WHEN 'plate_recognition' THEN 'vehicle'
                           ELSE value
                       END AS mapped
                FROM json_each(alert_destinations.filter_json, '$.event_types')
                ORDER BY key
            )
        )
    )
WHERE json_valid(filter_json)
  AND json_type(filter_json, '$.event_types') = 'array';
