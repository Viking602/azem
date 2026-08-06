# Azem 新上下文压缩内核

- 日期：2026-08-06
- 状态：已实施
- 数据库：schema 20
- 适用范围：主 Agent、Team、subagent、自动压缩、手动 `/compact`、显式 `/rebuild`、恢复

## 1. 最终决策

Azem 只保留一套新压缩内核。旧滚动摘要、固定 block 切割、宽松非 JSON 包装、Gate、shadow 和 legacy fallback 均不再存在。

schema 20 升级会清空可替换的 `session_projections.model_history` 和 Prompt Cache identity，迫使下一次运行从 canonical evidence 进入新内核。以下权威数据不删除：

- canonical `session_blocks`
- Todo
- tool records 与 side-effect recovery
- Context Artifact
- Memory、Recap、History FTS
- session、project ownership、usage、agent control-plane state

## 2. 目标

1. 自动、手动和恢复路径使用完全相同的预算、选择、语义写入和激活规则。
2. 压缩不再反复改写自由文本摘要，而是生成宿主验证的 `SemanticStateV1`。
3. 最近 3 个用户 turn 原文必须精确保留。
4. tool call/result 必须按完整原子组保留或整体进入语义状态。
5. 每个事实必须带可持久回查的 provenance。
6. semantic state、append-only event、manifest 与 Provider checkpoint 在同一事务/CAS 边界提交。
7. mandatory 内容无法满足预算时明确失败，禁止静默截断。

## 3. 数据流

```text
Canonical Evidence
  session_blocks / tool_records / artifacts / todo / memory / recap
                         |
                         v
Bounded Semantic Writer
  strict JSON + host validation + stable provenance
                         |
                         v
SemanticStateV1 + SemanticPatchV1 + WriterCursorV1
                         |
                         v
Unified Context Planner
  system prefix + semantic state + recent 3 users + exact tool tail
                         |
                         v
ContextManifestV1
                         |
                         v
SaveRunCheckpoint / CompactWithSummary transaction
                         |
                         v
ModelHistory V2 + active manifest + semantic revision
```

`internal/app` 负责编排和验证，`internal/session` 提供事务状态 API，`internal/store/sqlite` 负责 schema/query。UI 只投影诊断信息，不成为状态权威。

## 4. SemanticStateV1

状态集合：

- objective
- acceptance criteria
- constraints
- decisions
- current action
- active Todo item ID
- workset
- findings
- failures
- blockers
- next actions
- retrieval hints

每个 `StateFactV1` 包含：

- `id`：宿主基于 collection 与规范化正文稳定派生
- `text`
- `status`：`active`、`resolved`、`superseded`、`invalidated`
- `authority`：`user`、`tool`、`workspace`、`agent`
- `confidence`：`verified`、`reported`、`inferred`
- `sources`
- first/last sequence 与 supersedes（可选）

writer 必须返回严格 JSON。宿主拒绝非 JSON、错误 version、空 objective、非法枚举、超长文本、超量 fact 和无有效来源；只允许一次定向修复重试。writer 没有 shell、编辑、MCP、Todo mutation、Memory mutation 或 subagent 工具权限。

## 5. Provenance

持久来源只允许：

- `sequence:<number>`
- `tool:<run-id>:<tool-call-id>`
- `artifact:<artifact-id>`
- `todo:<revision>:<item-id>`
- `memory:<memory-id>`
- `recap:<session-id>:<revision>`
- `checkpoint:<stable-id>`

`summary:<index>` 和请求内临时 ID 禁止进入最终 semantic state 或 manifest。map/reduce 由宿主维护 authority/source 并集；模型返回的伪造或不可解析来源会被丢弃，并由宿主选择确定性的有效来源。

## 6. WriterCursorV1 与 Patch

cursor 由 canonical sequence、Todo revision、最后完成 tool、最后完成 subagent 组成。`SemanticPatchV1` 保存 base revision、through cursor、source digest 和 append-only operations。

事务保证：

- revision 使用 CAS。
- `(session_id, base_revision, source_digest)` 幂等。
- cursor 只能单调前进。
- writer 失败、输出非法、事务失败或 stale activation 时不推进 revision/cursor。
- stale checkpoint 不得覆盖新用户 turn。

## 7. 统一 Planner

Planner 的 mandatory 顺序：

1. 完整 system prefix。
2. 宿主安全标签和当前 SemanticStateV1。
3. 当前 Todo reminder。
4. 最近 3 个用户 turn 原文。
5. 最新用户 turn 后可容纳的完整 tool 原子组。

其余历史进入 semantic writer。若最新 turn 是 rolling tool turn，语义 checkpoint 紧跟最新用户消息，后面只出现完整 tool call/result group。

自动 soft prepare 在后台只启动一个 writer；相同 source 不重复工作。hard threshold、手动 `/compact` 和 `/rebuild` 同步执行同一 planner。prepared source 仅在仍是当前 history 前缀时激活，append-only tail 通过完整性校验后合并。

预算统一来自现有 `ContextConfig` 和模型 context window：soft/hard/target、安全余量、输出/推理 reserve、最小回收、最大 summary、large tool 和 history retrieval。不存在第二套手动切割参数。

## 8. ContextManifestV1

manifest 记录：

- session/run/reason/policy version
- static identity 与 model route hash
- canonical high-water
- semantic revision/cursor
- Todo revision
- target/estimated tokens
- ordered segments（kind、mandatory、token estimate、content hash、source refs）
- exclusions 与原因
- manifest hash

manifest 采用稳定序列化，ID 从 hash 派生。创建时间和随机值不参与 hash。Prompt Cache identity 包含 static identity、manifest hash 与 Model checkpoint hash；route、policy、语义 revision 或 segment 内容变化会自然失效。

## 9. Artifact V2

Artifact payload 与 SHA256 保持权威。preview 固定包含 version、kind、bytes、lines、sha256、encoding、head、tail、最多 8 条 error/warning、hint 和 truncated。

`context.read_artifact` 仅允许当前 session，支持：

- `preview`（默认）
- `range`
- `line_range`
- `tail`
- `grep`
- `full`（payload 不超过 64 KiB）

单次返回最多 64 KiB；binary 使用 base64 且原始读取最多 48 KiB；regex、offset、行号、模式和 limit 在工具边界验证。

## 10. SQLite schema 20

### session_semantic_state

每个 session 一行：revision、checkpoint ID、cursor、state、source digest、updated time。

### session_semantic_state_events

每个成功 revision 一行：checkpoint/base revision、cursor、patch、source digest、writer run 和 created time。事件 append-only。

### context_manifests

保存派生 ID、session/run、high-water、semantic revision、policy、hash、active 状态和完整 manifest JSON。同一 session 最多一个 active manifest。

`SaveRunCheckpoint` 与 `CompactWithSummary` 在同一事务内提交 semantic state/event、active manifest 和 ModelHistory。任何一步失败，全部回滚。

## 11. ModelHistory V2 与恢复

`ModelHistory` wire version 为 2，新增 manifest hash、semantic revision 和 policy version。wire 不匹配、static identity 不匹配或 schema 20 升级后的旧 history 不会被复用，而是从 canonical blocks 重建。

恢复时加载当前 semantic checkpoint 和 active manifest；cache identity 同时校验 static prefix、manifest、checkpoint。canonical transcript 始终是最终事实来源。

## 12. 诊断与入口

- `/compact`：运行新内核。
- `/rebuild`：显式别名，立即运行同一内核。
- `/context`：显示上下文占用和贡献项。
- Desktop Inspector：显示 policy、semantic revision、writer lag、rebuild reason、manifest hash 和 ordered segments。

内部 reason：`automatic_soft`、`automatic_hard`、`manual`、`resume`、`route_change`。当前激活路径使用 `automatic_hard` 与 `manual`，其余值为同一 manifest 合约预留，不形成第二套算法。

## 13. 不变量与验证

- `schemaVersion == len(migrations)`。
- schema 20 migration 保留 canonical/Todo/Artifact，清除旧 ModelHistory/cache identity。
- SemanticStateV1 严格 JSON 与 provenance 校验。
- 最近 3 个用户 turn 精确保留。
- tool groups 不拆分。
- map/reduce 输入有界，失败不改变 checkpoint。
- semantic commit/manifest/ModelHistory 原子提交，stale CAS 明确失败。
- Artifact 所有模式有界，oversized full 明确失败。
- 自动、手动、Team、subagent 使用同一 summary limit resolver。
- 前端 typecheck/test/build 与 Inspector 投影通过。
- `GOWORK=off go test ./...` 与 Sentrux rules 通过。

## 14. 明确不做

- 不删除或重写 canonical transcript。
- 不保留旧摘要算法或兼容回退。
- 不增加 Gate、shadow、双写或 rollout mode。
- 不复制 Todo 状态机。
- 不自动写 Project/Global Memory。
- 不引入 tokenizer、外部数据库、额外服务或新依赖。
