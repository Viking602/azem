---
name: skill-author
description: Create or refine an Agent Skills-compatible SKILL.md for a repeatable workflow.
---
# Skill author

Capture one repeatable workflow as an Agent Skill:
1. Use project scope at `.azem/skills/<name>/SKILL.md` by default. Use the active Azem config directory's `skills/<name>/SKILL.md` only when the user explicitly requests a user-wide skill.
2. Choose a name of at most 64 characters using only lowercase ASCII letters, digits, and single interior hyphens.
3. Write YAML frontmatter with exactly the required `name` and `description`, plus only standard Agent Skills fields that the workflow actually needs.
4. Keep the body procedural, concrete, and independent of this conversation. Put large reference material in sibling resource files.
5. Do not embed executable shell directives or claim that `allowed-tools` grants permission.

Before writing, show the complete proposed SKILL.md and its destination. Write it only after the user explicitly asks to save it. After writing, report the path and how to invoke it with `/skill <name> [instruction]`.
