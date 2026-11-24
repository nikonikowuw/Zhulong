-- 000040_alert_filter_event_type_v1.down.sql
-- 回滚：把过滤器 $.event_types 中的 v1 短名改回旧事件类型名。
UPDATE alert_destinations
SET filter_json = json_set(
        filter_json,
        '$.event_types',
        (
            SELECT json_group_array(mapped)
            FROM (
                SELECT CASE value
                           WHEN 'face' THEN 'face_recognition'
                           WHEN 'vehicle' THEN 'plate_recognition'
                           ELSE value
                       END AS mapped
                FROM json_each(alert_destinations.filter_json, '$.event_types')
                ORDER BY key
            )
        )
    )
WHERE json_valid(filter_json)
  AND json_type(filter_json, '$.event_types') = 'array';
