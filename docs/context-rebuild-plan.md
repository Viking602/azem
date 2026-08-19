# Azem 确定性上下文归档

- 日期：2026-08-18
- 状态：已实施
- 适用范围：Main、Team、subagent、自动维护、手动 `/compact`、显式 `/rebuild`、恢复

## 1. 决策

Azem 只有一条上下文压缩路径：宿主确定性归档。

压缩成功不再依赖模型生成摘要、JSON schema、后台 prepare、provider 重试、输出预算或 semantic revision。被移出热上下文的完整消息先编码为 `contextarchive.SourceV1`，再作为 session-scoped `context_archive` artifact 持久化。模型只接收一个有界 carrier 和最近的原始消息；artifact 才是归档事实来源。

旧 `SemanticStateV1` writer、`agents.compaction` 路由、soft/hard ratio 状态机和 semantic activation 已从活动运行时删除。schema 20 的历史 semantic 表仍保留在 SQLite schema 中以保证既有数据库可直接打开，但没有活动代码读取或写入这些表；`context_manifests.semantic_revision` 固定写入 `0`。

## 2. 为什么替换旧路径

旧路径把“模型及时返回满足宿主 schema 的完整 JSON”放在主 run 的必经路径。真实数据库中 compaction provider 请求多于 semantic commit，失败包含：

- 输出被截断或预算不足；
- stream open/`Recv` 长时间无输出；
- Markdown fence、字段形状和 evidence 引用漂移；
- stale semantic revision/CAS 冲突；
- 重试后仍无法生成合法状态。

继续增加 token 上限、normalization 或重试只能缓解单个症状，无法消除 provider 可靠性对主 run 的控制。新路径的正确性只依赖本地确定性代码、artifact/blob 持久化和现有 checkpoint CAS。

## 3. 数据流

```text
canonical transcript + live model history
                 |
                 v
normalize oversized tool results
                 |
                 v
compute OMP-style safe cut
  - keep system prefix
  - keep latest 3 complete shared user turns
  - keep assistant tool call + results atomic
  - prefer keep_recent_tokens hot tail
                 |
                 v
contextarchive.SourceV1 canonical JSON
                 |
                 +--> context_archive artifact + SHA-256
                 |
                 +--> bitmap carrier when model explicitly supports images
                 |    or bounded text/artifact carrier otherwise
                 v
ArchiveContextManifestV1 (policy v3)
                 |
                 v
one SaveRunCheckpoint activation
```

Automatic, manual, rebuild, Main, Team 和 subagent 都调用这条路径，没有第二个摘要或 fallback 内核。

## 4. 触发与预算

自动归档阈值为：

```text
trigger = model_context_window - tool_definition_tokens - reserve_tokens
```

当前默认值：

|字段|默认值|含义|
|---|---:|---|
|`enabled`|`true`|启用自动和显式归档|
|`reserve_tokens`|`16384`|为下一次模型输出保留的固定 headroom|
|`keep_recent_tokens`|`20000`|优先保留的原始 hot-tail token 下限|
|`large_tool_result_tokens`|`12000`|大工具结果 artifact offload 阈值|
|`history_retrieval_tokens`|`4096`|私有 session history FTS 证据预算|

`keep_recent_tokens` 是优化偏好，不是高于硬窗口的强制条件。如果它因为一个超大旧消息导致所有 carrier 都无法放入目标，reducer 会放弃这个可选 token floor，再按“最近 3 个完整 shared user turn”重切一次。最近三轮本身仍是强制边界；如果它们加 carrier 仍超过硬限制，归档明确失败，并且不写 artifact、不改 live history、不提交部分 checkpoint。

## 5. 切分不变量

- system prefix 保持原顺序；
- 最近 3 个完整、非 private 的 user turn 原样保留；
- private history/vision evidence 不参与“最近 user turn”计数；
- assistant tool-call message 与其连续 tool results 不可拆分；
- Todo reminder 在构建前刷新，在 carrier 组装后再次校正；
- 旧 carrier 必须先按 `source_artifact_id` 展开，禁止 carrier 嵌套；
- 同一个输入和选项生成相同 source SHA、页选择、frame hash 和 manifest hash；
- 任何持久化或 checkpoint 错误都返回原 history，不用半成品继续运行。

## 6. 模型无关的第一层 reducer

归档前先处理旧的大工具结果：

1. 只考虑最近三轮之前、超过 1 KiB 且能显著回收空间的结果；
2. 完整 payload 写入 `context_artifact`；
3. 原位置替换为带 artifact ID、SHA-256、preview 和原 token 数的 locator；
4. 每替换一个就重新估算；一旦低于目标立即停止；
5. 不改变消息顺序或 tool call/result 配对。

常规 `normalizeToolResults` 仍负责超过 `large_tool_result_tokens` 的 provider-visible 结果。两层都保留完整 artifact，不把静默截断当作压缩。

## 7. Carrier 选择

### Bitmap

当模型目录明确声明支持图片时，`internal/contextarchive` 使用内置 Silver CJK/Unicode 像素字体渲染 1568×1568 PNG：

- 最多 8 帧；
- 总 PNG payload 最多 4 MiB；
- 页选择、hash 和 attachment ID 稳定；
- 候选按 8 帧、4 帧、2 帧依次尝试；
- 每帧按 3400 token 计入 host 预算。

### Text/artifact

模型明确不支持图片、能力未知、视觉候选过大或 renderer 不可用时，carrier 只包含有界 head/tail preview、source artifact locator 和 manifest。完整 source 不嵌入模型消息。

Bitmap 只是便宜的模型输入载体，不是恢复副本。任何缺失或损坏帧都从 source artifact 重新生成；source SHA 不匹配则失败，不猜测修复。

## 8. 持久化与恢复

每次成功激活同时更新：

- `ModelHistory` wire version 3；
- `ArchiveContextManifestV1`，policy version 3；
- active `context_manifests` 行；
- archive carrier message；
- canonical high-water、Todo revision、source/exclusion refs 和 manifest hash。

`SaveRunCheckpoint` 保持现有事务和 source high-water CAS。若 canonical transcript 在准备后变化，旧结果不能覆盖新 user turn。恢复时校验 wire version、static identity、manifest hash、source SHA 和 frame attachment；不兼容的 derived checkpoint 被丢弃并从 canonical transcript 重建。

Fork 只复制 canonical transcript、终态 tool records、Todo/Recap 与普通 artifact。`ModelHistory`、provider cache、context manifest 和 `context_archive` 都是 session-scoped derived state，在目标 session 中清零，避免跨 session 复用 archive ID 或 provider state。

## 9. 失败语义

|失败|行为|
|---|---|
|缺少模型 context-window 元数据|显式失败，不连接 provider|
|artifact store 不可用|显式失败，保留原 history|
|source 超过 32 MiB|显式失败，不生成不完整 archive|
|bitmap renderer/图片能力不可用|尝试更小 bitmap，最后降为 text/artifact carrier|
|carrier 在可选 token floor 下过大|放弃 token floor，仍保留最近 3 个完整 turn 后重试|
|最近 3 个完整 turn 仍超过硬限制|显式失败，不持久化|
|frame 丢失或损坏|从匹配 SHA 的 source 确定性修复|
|source SHA 不匹配|显式失败，不接受损坏 source|
|checkpoint source stale|采用现有 run 的 durable high-water 重新构建；禁止覆盖新 transcript|

不存在“provider 压缩失败但继续等后台摘要”的状态。

## 10. Prefix cache

wire version 3、policy version 3 和 archive carrier 会让旧 derived cache identity 在首次切换时失效。之后 system prefix、消息顺序和 carrier 位置稳定；重复归档只替换一个 carrier 并继续追加热尾。删除 `agents.compaction` 也删除了每轮额外 provider 请求，不向主模型 static prefix 注入任何新 prompt。

## 11. 参考来源

- Oh My Pi compaction design: <https://github.com/can1357/oh-my-pi/blob/main/docs/compaction.md>
- Can Bölük, “Snapcompact: SotA compaction - instant, local, free. Pick 3”: <https://blog.can.ac/2026/06/10/snapcompact/>

Azem 采用 OMP 的关键结构：安全切点、原始历史持久化、机械 reducer、文本/视觉 carrier 分离和重复压缩先展开旧 source。Azem 额外保留 SQLite checkpoint CAS、session ownership、artifact/blob 生命周期和明确的 text-only provider 支持。
