# 人脸识别轨迹级跨帧融合与身份投票

来源：`.trellis/tasks/11-15-face-track-fusion-identity/task.json` 与其 `prd.md`，原文见 [source-prd.md](./source-prd.md)。原状态 `planning`，优先级 `P2`，创建日期 `2026-11-15`，创建人及经办人 `nikoniko`。本次仅登记规划，未启动实现。

## 背景与目标

在不降低单帧比对阈值的前提下，规划 CCTV 跨帧识别召回与稳定性的改进。

## In Scope

仅 `packages/face_recognition` 的 ONNX / RK3568 双档：空间优先关联、Top-K 特征融合、轨迹证据与身份投票。

## Out of Scope

不更改 ABI、IPC、Go、前端、模型、ROI 或默认单帧阈值；不将包内跟踪标识作为宿主 track_id。

## 影响范围

包内融合、身份决策、配置分层和对应单测；交付与档位漂移约束见 SDK 规格。
