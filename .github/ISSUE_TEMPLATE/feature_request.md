---
name: Feature request
about: A capability that's missing
labels: enhancement
---

**Problem**
What are you trying to do, and what stands in the way?

**Proposed solution**
What should happen instead?

**Alternatives you've considered**

**Scope check**
Does this touch the trust boundary (daemon = only component with OS access)?
Does it extend the `.ctx` language or the JSON-RPC contract? Contract changes
need the doc updated in the same PR (docs/jsonrpc-contract.md).
