# `.ctx` recipe cookbook

Copy-paste starting points for cortex tasks, each exercised by
`cortex-engine/tests/recipes.rs` (parse + compile + run against the scripted environment).

| Recipe | Pattern |
| --- | --- |
| [retry-upload.ctx](retry-upload.ctx) | connectivity-gated push loop |
| [periodic-fetch.ctx](periodic-fetch.ctx) | scheduled refresh, offline no-op |
| [batch-sync.ctx](batch-sync.ctx) | one-pass outbox drain with short-circuit guard |

Enqueue a recipe: `cortex.task.enqueue {kind: "...", payload: {...}}` after
registering the script with `-engine` / `-engine-script` (see
docs/jsonrpc-contract.md). The language itself is documented in
[docs/language-reference.md](../../docs/language-reference.md).
