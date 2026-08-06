# Changelog

## Unreleased

- Providers: integrate `github.com/Viking602/llmux` v0.2.0 as the generic
  language-model transport for native and OpenAI-compatible providers while
  retaining the existing ChatGPT and Grok subscription drivers.
- Desktop: add searchable Model settings for provider enablement, API base URL,
  protected API-key storage, arbitrary model IDs, context windows, and
  reasoning levels. Configured models are immediately available to the default
  session, title, plan, compaction, and subagent routes.
- Desktop: widen Model settings, align provider credential fields, use the
  available viewport for the provider directory, and show provider logos in
  provider catalogs and every model-selection surface.

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
