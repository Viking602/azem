---
name: verify
description: Verify the requested behavior with the strongest available evidence before reporting completion.
---
# Verify

Before reporting completion:
1. State the observable behavior the request requires.
2. Choose the smallest check that exercises that real behavior. Prefer the running workflow over a build, and a targeted test over a broad suite.
3. Run the check and compare the observed output with the required result.
4. If the check exposes a defect, fix the source and repeat the same check.
5. Report what was verified, the exact evidence, and anything that remains unverified.

A build or typecheck alone does not prove runtime behavior. Never claim a check passed unless its output was observed. If a required check cannot run, state the exact blocker and do not relabel the work as verified.
