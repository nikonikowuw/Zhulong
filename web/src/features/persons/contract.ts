/**
 * 人员特性的跨特性契约入口。
 *
 * 跨特性消费方（电子地图的人员轨迹检索）需要的 `Person` 模型与全量列表 hook
 * 统一从这里取，不穿透 `features/persons/hooks/*` 或 `features/persons/api/*`。
 */
export { useAllPersons, usePersons } from './hooks/use-persons'
export { type Person } from './data/schema'
