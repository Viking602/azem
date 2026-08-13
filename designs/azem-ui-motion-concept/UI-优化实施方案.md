# Azem UI 优化实施方案

## 目标

把 Azem 从“功能完整的桌面面板”提升成“状态连续、层级稳定、长时间使用不疲劳的 AI 工程工作台”。方案不改变现有信息架构，重点统一视觉层级与动效语言。

设计方向：**静谧机械感（Quiet Mechanics）**。

- 保留当前暖白纸面、橙色品牌点、细边框和紧凑排版。
- 页面负责空间关系，轨迹负责运行状态，颜色只负责语义。
- 动效出现时必须回答一个问题：内容从哪里来、状态变成了什么、用户应该看哪里。

## 当前基线

本方案直接基于以下当前源码，而非截图猜测：

- `frontend/src/styles.css`：现有色彩、字号、圆角、阴影和 reduced-motion 规则。
- `frontend/src/App.tsx`：工作台布局、页面切换、流事件帧预算和检查器状态。
- `frontend/src/components/Sidebar.tsx`：项目、任务和主导航。
- `frontend/src/components/ThreadSurface.tsx`：会话、Composer、模型菜单。
- `frontend/src/components/Timeline.tsx`：工具、commentary、reasoning、final answer 和流式文本。
- `frontend/src/components/Inspector.tsx`：环境、Todo、上下文、子代理和附件投影。

仓库当前没有可绑定的 `designs/*/_ds_manifest.json`，因此这次方案以产品现有 CSS token 为视觉约束，没有引入另一套外部设计系统。

## 设置布局逆向基线

- Codex App：只读分析本机 `ChatGPT.app` 的 SettingsPage 与 General Settings 打包资源。其稳定模式是分组侧栏、单内容列、页面内唯一标题，以及一个分组外框内连续排列的行式控件；搜索属于侧栏，不额外占用内容顶栏。
- ZCode App：实测 1152×768 设置窗口，侧栏约 254px（约 22%），内容从约 48px 内边距开始；常规页使用“标题 + 说明 + 行式设置组”，模型页在内容区内部再按需要拆出提供方列表和详情，不改变外层导航比例。
- Azem 采用两者的共同部分：接近整窗的 1026×760 设置工作区、220px 分组侧栏、736px 内容列、页面内单标题；只在模型目录这类主从任务中使用第二级列表，不把所有设置都做成卡片。
- 逆向截图仅作为布局证据：`reference-zcode-settings-general.png`、`reference-zcode-settings-models.png`。Azem 保留自身暖白、橙色强调和现有线性图标，不复制 ZCode 的紫色品牌或 Codex 的产品文案。

## 视觉结构

### 1. 工作台骨架

- 左侧栏保持 220–260px，继续承担“项目 / 任务 / 全局入口”。
- 中央内容成为唯一主舞台，页面切换只发生在这里，顶部项目和左侧导航保持稳定。
- 右侧检查器统一为 280–320px，所有运行上下文、Todo、文件变化采用相同区块节奏。
- Composer 固定在主舞台底部，但通过背景透明度、阴影和 focus rim 与正文分层。

### 2. 信息层级

- 正文：`--ink`，13–14px，1.65–1.72 行高。
- Commentary：弱一级字号与颜色，蓝色状态点表示仍在推进。
- Tool / Reasoning：轨迹式元信息，不和最终答案争夺视觉重量。
- Final answer：恢复完整正文层级；结束后移除光标与进行中状态。
- 颜色语义固定：橙色=品牌与聚焦，蓝色=进行中，绿色=完成，红色=失败，紫色=模型/智能层。

### 3. 多项目侧栏

- “项目”是侧栏的一级实体，“会话”归属在项目下，不再把所有最近任务混为一列。
- 每个项目头固定展示名称、分支、工作树状态、会话数；折叠时仍显示 PR 数量，避免状态被藏起来。
- 展开项目后，PR 行位于会话列表之前，固定展示编号、标题与检查结论：绿色=通过，红色=失败，琥珀色=等待。
- 同时操作多个项目时，只展开正在关注的项目；切换项目不销毁其他项目的展开状态和运行状态。
- “＋”菜单同时提供“打开已有文件夹”和“新建项目”，新建流程包含名称、路径与 Git 初始化开关。

### 4. 完整设置面板

- 设置使用桌面级双栏布局：左侧为可搜索分类，右侧为当前配置，避免一个长表单混合所有领域。
- 浮层目标尺寸为 `820 × 560px`，并始终保留至少 52px 的窗口留白；新建项目对话框目标宽度为 `380px`，不再占据大面积视野。
- 面板正文保持 11–13px，辅助说明不低于 9px；通过减少卡片高度和空白控制密度，不用缩小字体换取信息量。
- 六个完整分区：模型目录、模型路由、子代理、治理与审批、外观、扩展。
- 模型目录明确区分订阅驱动和 llmux 提供方；凭据状态、模型能力、启停状态处于同一视线。
- 提供方列表顶部保留独立搜索，按名称、驱动类型和能力关键词即时过滤；启用/未启用分组只在存在匹配项时显示，无结果时给出明确空状态，且不占用设置侧栏的全局搜索。
- 模型与提供方图标直接复用产品现有 `ProviderIcon` 契约，从 `https://models.dev/logos/{id}.svg` 加载；不再为图标额外添加圆角方块底座。
- 线性功能图标默认裸露呈现，只有文件夹、状态徽章和安全告警等真正表达容器语义的位置保留底色。
- ChatGPT 与 Grok 订阅提供方保留实时“每周额度、重置时间、额外额度”区；额度字段直接对应现有 `QuotaAvailable / QuotaUsedPercent / QuotaResetsAt / QuotaBalance / QuotaUnlimited` 契约。API 提供方不伪装成订阅额度。
- 设置内容遵循“一层容器 + 平面分区”：身份信息、订阅额度和模型列表使用细分隔线建立层级，不再出现外卡片内套字段框、额度卡片的多重圆角嵌套；提供方选中态只使用左侧强调线。
- Provider 切换使用稳定轨道：左侧条目固定高度且摘要单行截断，右侧账户区固定 `210px`、模型区固定 `175px`。订阅返回模型较少时显示目录同步状态行，不能让面板边界随内容数量跳动。
- 模型路由把主模型、规划、审批、视觉、压缩和子代理用途逐项列出，禁止隐式回退造成配置误判。
- 模型路由不使用浏览器原生下拉：统一改为可搜索浮层，直接展示 models.dev 提供方图标、能力摘要与当前选中状态；Fast 属于 ChatGPT 运行模式，不混入路由模型名称。
- 治理设置只保留内容区一个标题，审批模式、消息策略和失败关闭收敛到一张行式设置表；扩展设置同时表达 Skills、MCP、OAuth 与 Hooks 信任状态。
- 字体、主题与运动选择不使用浏览器原生下拉：统一为自绘浮层，使用暖白选中底色、线性勾选图标和 240ms 来源过渡；字体选项直接展示字形样例与用途说明，并完整支持方向键、Home/End、Enter、Esc 和外部点击关闭。
- 治理表采用单层外框与行分隔：左侧解释、右侧分段控件，失败关闭作为只读状态行，不再重复套用标题卡、选项卡和提示卡。
- 设置工作区参考本机 Codex App 与 ZCode App 的真实布局：使用接近整窗的双栏结构、约 220px 分组侧栏和 760px 内容列，不再使用小尺寸居中模态框或重复横向标题栏。
- 治理页进一步改为 Codex/ZCode 式行设置：左侧说明、右侧分段控件，三项内容共用一个外框和行分隔线，减少容器噪音但保留完整模式图标与状态。
- 取消侧栏底部“本地运行时已连接”常驻提示。健康状态只在异常、重连或需要用户处理时出现。

## 动效系统

| 层级 | 使用位置 | 时长 | 缓动 | 规则 |
|---|---|---:|---|---|
| Instant | hover、pressed、焦点、状态点 | 120–160ms | `ease` | 只改变颜色、边框或 1–2px 位移 |
| Component | 菜单、折叠、工具状态、选择器 | 180–240ms | `cubic-bezier(.22,1,.36,1)` | 保持触发元素可见，建立来源关系 |
| Scene | 页面、检查器、命令面板 | 260–340ms | `cubic-bezier(.16,1,.3,1)` | 最多 10px 位移、3px 模糊和 0.5% 缩放 |
| Stream | 新到达的文字尾部 | 220–280ms | `cubic-bezier(.16,1,.3,1)` | 只动画最新 8 个片段，旧文本立即稳定 |

统一 token：

```css
--motion-instant: 120ms;
--motion-fast: 170ms;
--motion-base: 240ms;
--motion-scene: 320ms;
--ease-out: cubic-bezier(.22, 1, .36, 1);
--ease-emphasis: cubic-bezier(.16, 1, .3, 1);
```

### 页面切换

- `thread / files / changes / pull requests / extensions` 共用一个 Scene 容器。
- 旧页面：`opacity 1→0`、`translateY 0→-6px`、`blur 0→2px`。
- 新页面：`opacity 0→1`、`translateY 10px→0`、`scale .995→1`、`blur 3px→0`。
- 左侧导航选中底板用 shared-layout 平移，不做每项独立闪烁。
- 页面切换期间不阻断键盘焦点；动画结束后聚焦新页面主标题。

### 检查器

- 工作台 grid column 从 `0` 到 `304px`，主内容自然让位，不叠加遮挡。
- 容器 320ms，内部 section 延迟 0 / 50 / 90ms，形成阅读顺序。
- 关闭时立即移除内部交互能力，防止隐藏控件被 Tab 聚焦。

### 工具生命周期

- `queued`：灰色空心节点。
- `awaiting_approval`：琥珀色边框和一次性强调，不循环闪烁。
- `running`：蓝色节点低频呼吸，进度轨迹向前推进。
- `completed / failed`：节点在 180ms 内收敛为绿色勾或红色叉，之后完全静止。
- 状态动画绑定真实生命周期事件，不能用计时器猜测运行结果。

### 流式文字渐显

现有 `StreamingPresentation` 和 `MAX_LIVE_REVEAL_CHUNKS = 8` 应保留。建议只增强每个新片段的进入状态：

```tsx
initial={{ opacity: 0, filter: "blur(5px)", y: 3 }}
animate={{ opacity: 1, filter: "blur(0px)", y: 0 }}
transition={{ duration: 0.26, ease: [0.16, 1, 0.3, 1] }}
```

约束：

- 不逐字符播放，按 provider 增量片段渐显，避免中文阅读抖动。
- 不重新动画稳定文本；只保留最多 8 个 live reveal 节点。
- terminal event 到达时立即完成当前片段，不延迟 final answer。
- `prefers-reduced-motion` 下直接渲染完整增量。
- `aria-live`、事件顺序、持久化内容和 Markdown 完成态保持不变。

## 具体落点

1. `frontend/src/styles.css`
   - 增加统一 motion token。
   - 收拢散落的 transition 时长与缓动。
   - 增加 scene、inspector、tool-state、stream reveal 和 reduced-motion 规则。
2. `frontend/src/App.tsx`
   - 给主内容建立稳定 Scene 容器。
   - 使用 `AnimatePresence` 或等价的 keyed motion wrapper 处理页面切换。
   - 保持 Sidebar、titlebar 和 Inspector 的现有状态所有权。
3. `frontend/src/components/Sidebar.tsx`
   - 选中背景改为 shared-layout 指示器。
   - 将项目恢复为可折叠列表；会话和 PR 作为项目子项投影。
   - 增加项目级 PR 计数、检查状态和“添加项目”入口。
   - 任务切换使用内容交叉淡入，不重排整个侧栏。
4. `frontend/src/components/Timeline.tsx`
   - 每个新增字符独立执行 blur / opacity / y 浮现；旧字符立即合并为稳定文本，动态尾部严格限制为 8 个节点。
   - 标点、空格与换行采用不同节拍，保持真实流式节奏；reduced-motion 下正文一次性可见。
   - 工具状态图标使用真实 lifecycle variant。
   - 保持 commentary / phase-pending / final_answer 的当前语义。
5. `frontend/src/components/ThreadSurface.tsx`
   - Composer focus、队列展开、模型菜单统一使用 motion token。
   - Composer 聚焦只增强中性阴影，不使用橙色 / 黄色描边。
   - 底栏保留审批模式、Plan、带 Provider 图标的模型选择和独立思考努力度选择。
   - 审批模式沿用产品现有的 `Hand / ShieldCheck / ShieldAlert` 线性图标，禁止使用 Emoji 或文字方块代替。
   - 思考努力度使用连续原生 range：12px 渐变轨道内嵌四个刻度点，22px 白色滑块可顺滑拖动，松手吸附到低 / 中 / 高 / 极高四档；方向键按档切换，刻度标签可直接点击，值同步到模型胶囊。
   - 会话模型菜单内置搜索，覆盖 ChatGPT、Grok 与 OpenRouter；ChatGPT 保留独立的线性闪电 Fast 开关，开启后显示“1.5 倍速 / 用量更多”提示并把状态同步到 Composer，不用静态标签冒充开关。
   - 保留已有模型面板 220ms 高度与纵向切换逻辑。
6. `frontend/src/components/Inspector.tsx`
   - section 进入采用短 stagger。
   - Todo、context、background、subagent 只在数据变化时局部过渡。
7. `frontend/src/components/SettingsDialog.tsx`
   - 保留现有六类设置契约，统一为左侧分类、右侧内容的完整桌面面板。
   - 搜索只过滤分类并跳到第一个匹配分区，不隐藏匹配分区内的上下文。
   - 外观和并发控制即时反馈；模型、审批和扩展开关保持明确保存状态。

## 完整页面矩阵

- 任务会话提供三个完整生命周期页面，而不是把状态压缩成同一段局部卡片：
  - **思考中**：展示当前推演目标、三步思考路线、实时 commentary 和已形成的方案方向；不提前渲染最终答案。
  - **执行中**：展示执行轨迹、工具生命周期、进度更新和逐字浮现的正文；正文末尾不再显示模拟插入光标。
  - **已完成**：展示交付摘要、完整页面范围和验证结果；Inspector 同步切换为 4 / 4 完成状态，底部只提供“继续修改计划”和“开始实现”两个决策入口。
- 项目文件、代码改动继续保留独立主页面；模型目录、模型路由、子代理、治理与审批、外观、扩展六个设置页面全部可从左侧设置导航访问。
- 会话阶段切换只替换中间内容与 Inspector 投影，Sidebar、Composer 和项目归属保持稳定，避免用户在任务完成前后失去空间位置。

## 全局图标约束

- 项目字标、PR、工具、文件类型、命令、扩展、审批与安全提示图标全部使用裸露线性图标或纯字标。
- 图标本身不得附加圆角方块底色、描边、内阴影或独立卡片底座；品牌模型图标继续直接使用 models.dev 资产。
- 圆角只服务于有明确交互或状态语义的组件边界，例如按钮、输入框、开关、状态标签和内容面板；这些组件不得被误当作图标底座。
- 新增图标必须通过 `[data-icon-treatment="bare"]` 全局约束验证，默认透明背景、零描边、零圆角、无阴影。

## 分阶段实施

### P0：Token 与无障碍基线

- 建立 motion token。
- 补全 `prefers-reduced-motion` 与显式“减弱动效”设置。
- 验证键盘焦点、`aria-live` 和隐藏面板 inert 状态。

### P1：导航与空间连续性

- 页面 Scene 切换。
- Sidebar shared indicator。
- Inspector 开合和 Command Palette 进入。

### P2：运行状态与流式输出

- Tool lifecycle variants。
- Commentary / reasoning 的低频状态动效。
- 流式文字逐字执行 blur + opacity + 6px 浮现，不显示闪烁光标。

### P3：二级页面统一

- Files、Changes、Pull Requests、Extensions、Recovery 共享标题、列表和空状态节奏。
- 所有 modal / sheet / popover 复用同一 component motion。

### P4：验证与收口

- `bun run typecheck && bun run test && bun run build`。
- `make test-gui`、`make gui`，真实启动 `dist/Azem.app`。
- 60Hz 和 reduced-motion 两套手工路径验证。
- 长会话、快速流式输出、检查器频繁开合下检查掉帧与 layout shift。

## 验收标准

- 页面切换在 340ms 内结束，快速连续切换不会出现重影或失焦。
- 流式增量永不丢字、重排或重复；DOM 尾部动画节点始终不超过 8 个。
- terminal / approval 事件不会被视觉动画延迟。
- reduced-motion 下所有内容立即可见，无脉冲、旋转、模糊或平移。
- 工具 lifecycle 的视觉状态与 store 中真实状态完全一致。
- 窄窗口下检查器自动退出布局，不挤压 Composer 到不可用宽度。
- 三个以上项目同时存在时，项目、PR 和会话归属仍可一眼识别；折叠项目不丢失 PR 数量与失败信号。
- 设置六个分区均有完整内容、空值与不可用状态，不用 toast 代替真实设置页面。
- 在 1058×964 的原型视口中，设置面板占宽约 78%、占高约 58%，主要标签清晰可读且不依赖系统缩放。

## 原型操作

- 左侧沿用 Codex App 的“新对话 / 搜索”入口，项目与会话历史直接位于其下；文件和改动通过会话内入口或命令面板访问。
- 从项目列表打开任一会话后，使用顶部“思考中 / 执行中 / 已完成”查看任务生命周期的三个完整页面，右侧 Inspector 会同步更新。
- 已完成页面底部“继续修改计划”：返回思考中页面并聚焦输入框；“开始实现”：进入执行中页面并重启实现进度。
- 左侧“项目”列表：展开 / 折叠 Azem、llmux、Venat，检查 PR 通过、失败与等待状态。
- 项目标题旁“＋”：打开已有文件夹，或进入带路径和 Git 选项的新建项目流程。
- 左下“设置”或 `⌘,`：打开完整设置；六个分类、搜索、主题、字号、并发和开关均可交互。
- 顶部“重播流式输出”：自动切换到执行中页面，查看每个字符从不可见、模糊和下移状态浮现到基线。
- 右上“动效规范”：切换暖白 / 夜间与完整 / 减弱动效。
- “侧栏”按钮：查看检查器开合。
- `⌘K`：查看命令面板进入动效。
