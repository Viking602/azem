# Beautiful UI integration

Azem vendors the relevant [Beautiful UI](https://www.beautifului.dev/) copy-paste primitives instead of adding a runtime package. `Primitives.tsx` owns the Thinking, Streaming Text, Tool Row, Approval Card, and Task Row shells; `beautiful-ui.css` owns their tokens and interaction styling.

Product state, tool lifecycle, approval actions, and durable timeline projection remain owned by Azem's existing components. The upstream MIT notice is preserved in `LICENSE`.
