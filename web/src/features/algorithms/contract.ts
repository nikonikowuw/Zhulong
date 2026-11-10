/**
 * 算法包特性的跨特性契约入口。
 *
 * 遵循 directory-structure.md §2.2：非组件契约（API、Hook、Schema 类型）
 * 统一从 `contract.ts` 暴露，避免跨模块直接穿透私有目录。
 */
export { algorithmsApi } from './api/algorithms-api'
export { ALGORITHMS_QUERY_KEY, useAlgorithms } from './hooks/use-algorithms'
export { type Algorithm, type AlgorithmStatus } from './data/schema'
