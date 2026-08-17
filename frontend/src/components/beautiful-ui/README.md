# Beautiful UI integration

Azem vendors the relevant [Beautiful UI](https://www.beautifului.dev/) copy-paste
primitives instead of adding a runtime package. `Primitives.tsx` owns the
Loading, Thinking, Streaming Text, Prompt Bar, Tool Row, Approval Card, and
Task Row shells. `CodeBlock.tsx` owns the transcript fenced-code card: an 18px white
paper card with filename, language, Copy, and a line-number gutter. First-token wait uses `ThinkingState`
(same sparkle row as a live or completed trace). Live reasoning and process traces use `ThinkingState` (sparkle).
In-progress wait, thinking, search, and tools stay on that one sparkle
row and only change the label. A thinking-only trail is just the 思考 header
plus reasoning prose; elapsed time sits in the bar meta slot. After the current step completes with tools, the sparkle bar becomes a
plain count row (`N tool calls, N messages`) with no capsule. Opening it shows
the 思考 chip first, then write/shell/read chips, then file-change pills.
Live wait, thinking, and running tools stay on `ThinkingState`. There is no second disclosure card and no per-group header
repeating what the bar already said. Inspector Todo phases use the Task Row **Capsules**
shape: a white rounded card, status mark (green check or numbered progress
ring), bold title, honest `done/total` metric, status badge, and an expand
rail for the phase's items. Do not invent metrics the store does not have.
`beautiful-ui.css` is imported last so its cool gray / blue tokens win over
the prototype warm palette.

Product state, tool lifecycle, approval actions, subagent run cards, and
durable timeline projection remain owned by Azem's existing components.
Progress commentary is ordinary prose, not a titled duration card. Do not lay
that prose on the old 15px marker grid. The upstream MIT notice is preserved
in `LICENSE`.

Select Action (`ActionIsland`) is a floating pill that appears when the user
highlights assistant, commentary, or user prose. Confirming 解释, 改进, or a
custom 描述编辑 sends a normal user turn through the existing composer
`submitTurn` path with a Markdown-quoted selection. Thinking, tool dumps, and
wake notices are not actionable.

Tool Chips (`ToolChip.tsx`) restyle actual tool rows: icon, bold label, and a
light-gray mono chip for the path, command, or query. Process groups may show
honest `N 次工具调用` / `N messages` counts. Executed file edits add white
`path +N -N` pills. Reasoning-only traces stay on ThinkingState. A live
step stays on the sparkle bar; after it completes, the Tool Chip list
opens as a chip card with the 思考 chip first. First-token wait stays on the same
Thinking sparkle row.

`StepRow.tsx` wraps every chip in that list with a status mark and one rail
segment; `timeline/stepRail.ts` owns the pure state, edge, and stagger logic.
The thread runs in its own left gutter beside the marks, so an expanded chip
body cannot break it and no mark needs a paper mask. Marks report the real
aggregate — Azem dispatches tools in parallel, so several rows may spin at
once. New rows cascade in at 120ms, capped at six rows; a row the window has
already shown must never replay that entrance while scrolling.

One step is one bar. A step is one model message plus the tools and thinking
that follow it, so a trail reads message, bar, message, bar. `ProcessStep`
renders the bar and owns the body; the rows never appear outside it, and the
body opens from the bar. `ProcessStepBar` renders `ThinkingState` and owns the
running clock, so a tick cannot re-render the body and replay its entrances.
While the step runs, `activityBarLabel` names the row that is executing and
keeps changing with it; a settled step prints the step's own total instead. A
live step that has not called a tool yet keeps its reasoning open. An opened
live body reads reasoning first as quiet prose; a settled card puts the 思考
chip first, then its tool rows. The label
crossfades only when its meaning changes, never on a clock tick. The
first-token wait is that same step, so the element that reads 思考 is the one
that later reads 读取文件.
Subagent spawn stays on
the existing run card. Queued and approval-bound writes never appear as file
pills.
