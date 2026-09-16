# cortex-engine (Rust)

The bytecode VM for tpt-cortex task scripts — the statically-typed DSL from [spec.txt](../spec.txt) §6. Pipeline: **lex → parse → compile → stack VM**, with every side effect routed through the [`NativeEnv`](src/natives.rs) trait (`native.db`, `native.net`, `native.http`) so the execution core is pure and auditable.

```sh
cargo test                     # unit + integration + safety tests
cargo run -- run examples/sync.ctx
```

## Layout

| Module | Role |
| --- | --- |
| `lexer.rs` / `parser.rs` | Tokens and AST for `task name() -> void { ... }` scripts |
| `compiler.rs` | AST → bytecode; resolves locals and `native.*` paths at compile time |
| `bytecode.rs` | The (deliberately small) instruction set |
| `vm.rs` | Stack machine; instruction budget bounds every program |
| `natives.rs` | `NativeEnv` trait, standard registry, deterministic test double |
| `examples/sync.ctx` | The spec §6 background-sync task, verbatim |

## Safety posture

No `unsafe`, no recursion, no unbounded loops: every instruction either steps or returns a `VmError`, and a hard instruction budget stops runaway scripts. `tests/safety.rs` executes thousands of deterministic adversarial programs and asserts error-not-panic — the executable subset of the [formal verification story](../docs/formal-verification.md).
