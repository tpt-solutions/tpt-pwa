# The `.ctx` language reference

A `.ctx` file holds exactly one **task**: a small, effectful program the
cortex-daemon executes through the cortex-engine VM (spec §6). The language
is deliberately tiny — it exists so a task can *decide when to push data*,
not so it can compute. Everything the VM cannot prove statically is a
runtime **error value**, never a panic; see [formal-verification.md](formal-verification.md)
for the totality contract.

## Program shape

```ctx
task sync_notes() -> void {
    let rows = native.db.query("SELECT id, action, payload FROM outbox");
    if !native.net.isConnected() {
        return;
    }
    for row in rows {
        native.http.post(row.endpoint, row);
    }
}
```

- `task <name>() -> void { … }` — the only top-level form. The parameter
  list is empty by design: tasks receive their inputs through
  `native.db.query` (today: the task's own outbox entries; see
  [jsonrpc-contract.md](jsonrpc-contract.md)).

## Statements

| Statement | Notes |
| --- | --- |
| `let name = expr;` | Single assignment — there is no `=` re-assignment yet. |
| `if expr { … } else { … }` | `else if` chains work. Condition is truthiness-based. |
| `for x in expr { … }` | Iterates a **list**. `expr` must be a list. |
| `return;` / `return expr;` | Ends the task; the value is reported by the engine. |
| `expr;` | Expression statement (usually a native call). |

## Expressions

| Form | Result |
| --- | --- |
| `123` / `1.5` / `"str"` / `true` / `false` | Literals (i64, f64, string, bool) |
| `name` | Local variable |
| `a.b` | Member access (maps); `native.x.y()` chains resolve at compile time |
| `list[i]` | Index access (i64 index, bounds-checked) |
| `!expr` | Logical not (truthiness) |
| `a + b` `a - b` | Numbers add/subtract (i64 checked, no overflow wrap), strings concatenate |
| `a == b` `a != b` | Equality |
| `a < b` `a <= b` `a > b` `a >= b` | Ordering — numbers compare exactly across int/float; **NaN comparisons are errors** |
| `a && b` `a || b` | **Short-circuit** — the right side (including its native calls) does not run when the left decides |
| `native.db.query(sql, params…)` | → list of row maps |
| `native.db.exec(sql, params…)` | → affected count |
| `native.net.isConnected()` | → bool |
| `native.http.post(url, body)` | → status code; non-2xx is a runtime error |

Precedence, loosest to tightest: `||`, `&&`, `== !=`, `< <= > >=`, `+ -`,
unary `!`, member/index/call.

## Values and types

`null`, `bool`, `int` (i64), `float` (f64), `string`, `list`, `map`.
There is no implicit numeric coercion except int⇄float in mixed arithmetic
and comparisons; everything else is a checked `type mismatch` error.
Lists and maps are immutable and reference-counted, so re-using a big list
in a loop is O(1) per reference.

## Error model

| Class | Examples | CLI exit | Scheduler behavior |
| --- | --- | --- | --- |
| Parse error | bad syntax, nesting > 128, > 100k tokens | 2 | permanent — never retried |
| Compile error | unknown native, non-native call, program > 65535 instrs | 2 | permanent — never retried |
| Runtime (data) error | list index out of range, integer overflow, NaN ordering, native failure | 1 | retried with backoff, then parked `failed` |

## Resource bounds

- **Nesting depth** ≤ 128 (`parser::MAX_NESTING_DEPTH`) — parser, compiler,
  and AST drop all recurse, so this bounds them all.
- **Tokens** ≤ 100,000 per script.
- **Instructions** ≤ 1,000,000 per execution (`DEFAULT_INSTRUCTION_BUDGET`).
- No `while`, no assignment, no functions: loops are finite by construction,
  and the budget catches everything else.

## Deliberately missing (see TODO.md Tier 3)

`null` literals in source, assignment, `* / %`, unary minus, list/map
literals, user functions, `while`. The VM supports more than the compiler
exposes; the language grows only when a real task needs it.
