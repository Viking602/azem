# Changelog

## Unreleased

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
