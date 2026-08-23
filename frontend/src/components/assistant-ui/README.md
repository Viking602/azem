# assistant-ui Elements integration

Azem vendors presentational source components from
[assistant-ui Elements](https://www.assistant-ui.com/elements). It does not add
the assistant-ui runtime. Registry components are installed through shadcn and
compiled by Tailwind v4; Azem's durable event stream, Zustand projection,
approvals, session ownership, and tool execution remain the only behavioral
state owners.

## Components

- `components/elements/message-pair.tsx` owns the turn root plus user,
  assistant, progress, and error message surfaces through `data-slot`
  contracts. Azem imports those registry components directly; assistant and
  progress prose use the full bounded transcript width.
- `components/elements/composer.tsx` owns Composer, ComposerBar, menu,
  attachments, textarea, toolbar, attach, context, and send slots. The Azem
  thread component supplies durable input behavior and passes the actual
  ordered `contextComposition` groups with shared localized category labels
  into ComposerContext.
- `Elements.tsx` owns LoadingState, ReasoningPanel, StreamingText, ToolCall,
  ApprovalCard, AgentPlan, and shared label-motion helpers.
- `ToolTimeline.tsx` and `ToolTimelineStep.tsx` own work rows, status marks,
  file statistics, and the single connected process rail.
- `CodeBlock.tsx` owns fenced Markdown code with filename/language, copy, and a
  line-number gutter.
- `CodeDiff.tsx` maps Azem `FileChange` records into the registry-installed
  `components/elements/code-diff.tsx` contract. Its rows enter in 200ms with a
  32ms stagger capped at six rows.
- `elements.css` bridges shadcn semantic colors to Azem's light/dark tokens and
  supplies product-specific layout overrides after the Tailwind entry point.

## Conversation invariants

Assistant prose is the primary reading layer. Model-authored progress stays in
that same column. A completed pre-answer process may fold under its elapsed-time
row, but opening it restores all commentary, reasoning, tools, and diffs to the
ordinary transcript flow immediately before the final answer. The outer
transcript viewport is the only conversation scrollbar.

First-token wait, reasoning, search, and live tools share one ReasoningPanel
bar. The label changes in place; the clock never remounts the body. Settled work
opens as a ToolTimeline with the reasoning item first, followed by tool rows and
file statistics. Every row retains its real completed, running, pending, or
failed state and reduced-motion behavior.

Todo, recap, and conversation sources live under `Conversation` in the
Synara-derived Environment panel. The fixed 288px card sits 12px from the
thread's top-right edge beside real workspace rows for changes, branch, local
servers, files, and terminal. Wide threads reserve its 312px footprint; narrow
threads keep the same card as an overlay. The header Environment button owns
open/close state, and the card never becomes a draggable browser window.
All panel chrome, section labels, row labels, and accessible names resolve
through the shared `translator()` dictionary for both Chinese and English.

## Registry

`components.json` registers `https://r.assistant-ui.com/{name}.json`. Installed
sources and their commands:

```sh
npx shadcn@latest add "@assistant-ui/elements-code-diff"
bunx --bun shadcn@latest add "@assistant-ui/elements-message-pair"
bunx --bun shadcn@latest add "@assistant-ui/elements-composer"
```

Do not restore the custom Message or Composer presentation wrappers, old diff
table, line-number gutter, syntax tokenizer, copy action, or vertical chat-diff
scroller. Shared syntax highlighting for Markdown and workspace viewers lives
independently in `components/syntaxTokens.ts`.

Third-party MIT notices are preserved under `frontend/THIRD_PARTY_NOTICES/`.
