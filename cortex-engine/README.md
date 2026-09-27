# cortex-engine (Rust)

The bytecode VM for tpt-cortex task scripts — the statically-typed DSL from [spec.txt](../spec.txt) §6. Pipeline: **lex → parse → compile → stack VM**, with every side effect routed through the [`NativeEnv`](src/natives.rs) trait (`native.db`, `native.net`, `native.http`) so the execution core is pure and auditable.

```sh
cargo test                     # unit + integration + safety tests
cargo run -- run examples/sync.ctx
```

## Two execution modes

- `cortex-engine run <script.ctx>` — execute against the scripted in-memory environment (`MemoryNative`); used by tests and for local checks.
- `cortex-engine exec-host --script <script.ctx>` — execute with every `native.*` call served by the **parent process** over a line-delimited JSON protocol on stdio (`src/host.rs`): the engine emits `{"id":1,"method":"http.post","params":[…]}` and blocks on `{"id":1,"result":…}`. This is how the Go daemon executes tasks through the real VM (`cortex-daemon/internal/engineexec`) while the VM core stays pure I/O-free. The protocol has an integration test that spawns the actual binary (`tests/host_protocol.rs`).

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
