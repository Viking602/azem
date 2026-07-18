---
name: simplify
description: Review changed code for reuse, clarity, correctness, and unnecessary work, then fix confirmed issues and re-verify.
---
# Simplify

Review only the code relevant to the current change:
1. Reuse: find existing helpers or patterns that should replace duplicate logic.
2. Clarity: remove needless abstraction, indirection, copies, and hidden control flow.
3. Correctness: check boundaries, errors, state transitions, concurrency, and caller expectations.
4. Efficiency: remove avoidable allocation, repeated work, and overly broad operations.

Fix only issues supported by the code or failing behavior. Preserve unrelated user work. Re-run the smallest verification that proves every fix, then report the fixes and evidence.
