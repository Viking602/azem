# Changelog

## Unreleased

- Providers: integrate `github.com/Viking602/llmux` v0.2.3 as the generic
  language-model transport for native and OpenAI-compatible providers while
  retaining the existing ChatGPT and Grok subscription drivers. Main and
  subagent requests now apply each configured model's output-token limit, so
  Anthropic-compatible providers no longer silently fall back to 4,096 tokens;
  a provider `length` stop is reported as truncation instead of success.
- Desktop: add searchable Model settings for provider enablement, API base URL,
  protected API-key storage, arbitrary model IDs, context windows, and
  reasoning levels. Configured models are immediately available to the default
  session, title, plan, compaction, and subagent routes.
- Desktop: widen Model settings, align provider credential fields, use the
  available viewport for the provider directory, and show provider logos in
  provider catalogs and every model-selection surface.
- Desktop: progressively load the complete llmux provider catalog while
  scrolling, and preserve llmux's Anthropic Messages preference for compatible
  providers instead of routing them through OpenAI Chat Completions.
- Desktop: enabled llmux providers can fetch their authenticated model list
  directly from the provider API. Matching models.dev records enrich the list
  with names, descriptions, context/output limits, modalities, tool and
  structured-output capabilities, and exact reasoning-effort levels. Provider
  icons use models.dev IDs throughout the catalog and model selectors.
- Providers: adopt llmux v0.2.1's canonical hyphenated provider IDs, read
  legacy underscore IDs for compatibility, and rewrite them canonically on the
  next settings save. The remaining non-identical provider names use the full
  current models.dev logo alias set rather than broken image requests.
- Desktop: Model settings now includes the existing OpenAI/ChatGPT and Grok
  subscription login flows with account status, live remaining weekly quota, reset
  time, available credit balance, and logout controls. Official
  llmux API base URLs are visible but read-only; only profiles that explicitly
  require a self-hosted endpoint remain editable. Missing OpenCode Zen,
  FreeModel, and Xpersona defaults are filled from models.dev.
- Desktop: list Model settings providers immediately without waiting on
  subscription quota HTTP calls; quota is refreshed asynchronously after the
  catalog is shown, and the pane shows loading and error states instead of a
  blank “no providers” screen.
- Providers: map Venat dotted tool names (for example `coding.read_file`) to
  OpenAI/DeepSeek-safe `^[a-zA-Z0-9_-]+$` wire names on every llmux request, and
  restore the canonical names on streamed tool calls so DeepSeek and other
  strict OpenAI-compatible providers no longer reject tool definitions.
- Models: normalize ChatGPT, Grok, and llmux catalogs through one models.dev
  resolver. Provider IDs, slugs, and aliases remain valid protocol identifiers,
  while every model picker displays the models.dev name and role-model search
  accepts aliases without sending them to the provider.
- Desktop: show each signed-in ChatGPT and Grok subscription's live model
  catalog with capability badges on its provider card, and use provider display
  names consistently in model selectors instead of exposing lowercase protocol IDs.
- Providers: use llmux v0.2.2's provider-native model discovery API instead of
  maintaining duplicate model-list transports in Azem; models.dev remains the
  metadata source and fallback catalog.
- Desktop: add persistent global interface font and 11–20 px font-size controls
  under Appearance, backed by the font families installed on the host Mac,
  localized labels, search, immediate preview, and a one-click default reset.
- Desktop: compact the advanced model-control footer and keep its active state
  on the “Advanced” label instead of highlighting the full row.
- Desktop: compact Model routes into a constrained, single-line route table
  with shared column labels and responsive stacked controls on narrow windows.
- Desktop: explicitly reload durable session history after frontend
  initialisation so a missed one-shot startup event cannot leave the sidebar empty.

- Context: replace the rolling summary implementation with one semantic
  compaction kernel shared by automatic, manual `/compact`, explicit
  `/rebuild`, Team, subagent, and resume paths. It preserves the latest three
  user turns verbatim, validates stable provenance, keeps tool groups atomic,
  and emits deterministic context manifests.
- Context: Artifact V2 stores bounded head/tail/error/warning previews and the
  read tool now supports bounded preview, byte range, line range, tail, grep,
  and size-limited full modes.
- Persistence: schema 20 adds semantic state, append-only semantic events, and
  context manifests. The upgrade invalidates legacy replaceable ModelHistory
  and cache identity while retaining canonical conversation and durable state.
- Desktop: Inspector now shows semantic revision, writer lag, rebuild reason,
  manifest hash, and segment token estimates.
- Runtime: dispatch independent tool calls in parallel so foreground subagents
  and shell commands no longer queue behind unrelated calls; their existing
  runtime concurrency limits still apply.
- Desktop: frame-pace streamed text with a restrained activity cursor, bounded
  catch-up, and reduced-motion support so long output does not monopolize UI
  rendering.
- Packaging: use the supplied borderless icon for the macOS bundle and ad-hoc
  sign and verify local `make gui` builds so Finder can launch the result.
- Desktop: persist multiple opened projects in SQLite, restore the most recent
  valid project on app launch, and group sessions by their owning project.
- Desktop: opening a session from another project now launches it with that
  project's workspace so branch, Pull Request, Skills, hooks, and tool context
  remain aligned.
- Persistence: schema 19 adds `desktop_projects` and `session_workspaces`.
  Existing sessions are adopted automatically only when one valid project can
  be identified; ambiguous legacy ownership is not guessed.
- Configuration: project history is no longer written to `workspace.root`.
