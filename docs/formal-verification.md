# Formal Verification of cortex-engine — Scope, Method, Status

Phase 4 of [TODO.md](../TODO.md). Covers the `cortex-engine` Rust VM (the "native execution layer" of spec §3) and evaluates the applicability of the **tpt-telos** methodology.

## 1. What is being guaranteed

The engine's contract, in decreasing order of importance:

| Guarantee | Statement |
| --- | --- |
| **Memory safety** | No out-of-bounds access, no use-after-free, no undefined behavior for any input bytecode or script. |
| **Crash resistance** | `run()` is *total*: for every input it returns `Ok(Value)` or `Err(VmError)` — never a panic, never a hang (bounded by an instruction budget), never an unbounded allocation. |
| **Effect containment** | The VM performs no I/O; every effect goes through the `NativeEnv` trait, so the audited core is small and the hosts (Go daemon, Android) can sandbox effects. |

## 2. Why this is tractable here

- **The instruction set is small** (`bytecode.rs`, ~20 instructions). A soundness argument scales with the semantic surface, not with the code size.
- **No `unsafe`, no recursion, no threads** in the VM. Rust's type system already discharges use-after-free and data races; what remains to argue is bounds/totality, which is visible in `vm.rs`.
- **Locals and constants are resolved at compile time**; the VM only needs bounds-checked indexed access, each of which maps to an explicit `Err` (`local out of range`, `constant out of range`, `jump target out of range`).

## 3. Method: layers of assurance

### Layer 1 — executable, in this repo (done)

`cortex-engine/tests/safety.rs` is the always-green subset of the verification story:

- `adversarial_bytecode_never_panics` — 2,000 deterministic pseudo-random programs (out-of-range jumps/locals/constants, arity-mismatched native calls, type-confused operands) execute under a budget; the only accepted outcomes are `Ok` or `Err`.
- `runaway_loop_hits_budget_instead_of_hanging` — a crafted infinite loop terminates with `BudgetExhausted`.
- `stack_is_bounded_by_budget` — unbalanced pushes cannot grow the stack past the budget (no OOM).
- `hostile_native_implementations_return_errors_not_panics` — even failing host environments surface as `VmError::Native`.
- The spec §6 `sync.ctx` runs end-to-end (`tests/sync_ctx.rs`), pinning real semantics, not just absence of crashes.

This is a *sampling* argument (a fuzz-style test), not a proof — it continuously re-earns confidence as the VM evolves.

### Layer 2 — model checking (planned)

[kani](https://github.com/model-checking/kani) harnesses over `Vm::run`:

- assertion: no panic for any `Program` within bounded sizes (unwind-limited);
- proof: every reachable instruction respects stack height bounds implied by a per-instruction effect table (a stack-depth type system over bytecode — cheap to encode, strong evidence for the totality claim).

The instruction-set table lives next to the enum in `bytecode.rs` so the harness and the semantics cannot drift silently.

### Layer 3 — tpt-telos evaluation (external dependency)

**Status: evaluated for applicability, not applied — tpt-telos is not built in this repo** (TODO.md Phase 4 is explicit about this).

| Aspect | Assessment |
| --- | --- |
| Fit | tpt-telos-style refinement (spec → model → executable) maps cleanly onto this codebase: the instruction set *is* the model, `vm.rs` *is* the refinement, and the two live in the same crate. |
| Cost/benefit | Full formal treatment of the parser/compiler (currently ~2 files) is disproportionate; restricting tpt-telos to the VM core + bytecode semantics captures the crash-resistance guarantee where it matters. |
| Prerequisite | The DSL grammar is still a skeleton; freezing the grammar and instruction set (versioned bytecode) is required before any proof investment, otherwise proofs churn with every feature. |
| Recommendation | Adopt incrementally: (1) freeze + version the bytecode format; (2) Kani harnesses (Layer 2); (3) machine-checked stack-effect discipline for `Instr`; (4) only then consider deeper tpt-telos integration for the compiler's register allocation. |

## 4. Verification scope

| Component | In scope | Current layer |
| --- | --- | --- |
| `vm.rs` (execution core) | yes — totality + memory safety | L1 done, L2 planned |
| `bytecode.rs` (instruction set) | yes — semantics table | L2 planned |
| `compiler.rs` | partially — well-formedness of emitted code | L1 (via running compiled output) |
| `parser.rs` / `lexer.rs` | total functions, no panics | L1 (adversarial inputs) |
| `natives.rs` hosts (daemon/Android) | no — effects are host-side; covered by Go unit tests and contract tests | out of scope here |

## 5. What would falsify the guarantee

Any `panic!`/`unwrap` reachable from `Vm::run` inputs, an instruction handler that can loop without consuming budget, or a `NativeEnv` implementation that blocks (the trait contract says: return errors, never hang). CI (`cargo clippy -D warnings`, `cargo test`) plus the safety suite guard all three today.
