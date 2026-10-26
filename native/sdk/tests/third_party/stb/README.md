# stb 图像编解码（开发期测试专用）

`stb_image.h` / `stb_image_write.h` 只服务**开发期**的算法包自测
（`packages/<id>/<target>/tests/dev_infer.cpp`：读包根 `sample.jpg` → 画框 → 写
`result.jpg`）。它们**不**参与产品代码、**不**进算法包归档、**不**进 `libZhulongAlgoRuntime.a`。

- 来源：<https://github.com/nothings/stb>（`stb_image` v2.26 / `stb_image_write` v1.15）
- 许可：public domain / MIT，见同目录 `LICENSE.txt`
- 完整性：`SHA256SUMS`，由 `native/CMakeLists.txt` 与包级 CMakeLists 在 configure
  阶段校验。构建期不联网、不下载（与 `make native-deps` 的固定依赖策略一致）。
- 为什么 vendored 而不是系统 `libjpeg`：包作者的开发机（尤其干净容器）不该额外装
  图像库；stb 是 header-only、零动态依赖，`make test` 在任何 host 上都能跑。

升级步骤：替换两个头 → `sha256sum stb_image.h stb_image_write.h > SHA256SUMS` →
跑 `make -C packages/<id>/<target> test`，确认 `result.jpg` 仍正确。
