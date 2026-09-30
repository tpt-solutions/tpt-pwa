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
    fn push(row) {
        return native.http.post(row.endpoint, row);
    }
    let rows = native.db.query("SELECT id, action, payload FROM outbox");
    if !native.net.isConnected() {
        return;
    }
    for row in rows {
        push(row);
    }
}
```

- `task <name>() -> void { … }` — the only top-level form. The parameter
  list is empty by design: tasks receive their inputs through
  `native.db.query` (today: the task's own outbox entries; see
  [jsonrpc-contract.md](jsonrpc-contract.md)).
- `fn name(params) { … }` — declared **only at the top of the task body**.
  Functions close over nothing: they see their parameters, their own
  locals, and other functions (declared before or after — mutual recursion
  works). Falling off the end returns `null`.

## Statements

| Statement | Notes |
| --- | --- |
| `let name = expr;` | Declares a new local. |
| `name = expr;` | Re-assigns an **existing** local. Assigning to an undeclared name is a compile error — containers stay immutable, only plain locals can be assigned. |
| `if expr { … } else { … }` | `else if` chains work. Condition is truthiness-based. |
| `while expr { … }` | Loop while the condition is truthy. The instruction budget bounds it like everything else. |
| `for x in expr { … }` | Iterates a **list**. `expr` must be a list. |
| `return;` / `return expr;` | Ends the task (or function); the value is reported by the engine. |
| `expr;` | Expression statement (usually a native or function call). |

## Expressions

| Form | Result |
| --- | --- |
| `123` / `1.5` / `"str"` / `true` / `false` / `null` | Literals (i64, f64, string, bool, null) |
| `[a, b, c]` | List literal (elements are arbitrary expressions) |
| `{"key": expr, …}` | Map literal; keys evaluate to **strings** at runtime, duplicate keys keep the last value |
| `name` | Local variable |
| `a.b` | Member access (maps); `native.x.y()` chains resolve at compile time |
| `list[i]` / `map["k"]` | Index access — int index into lists (bounds-checked), string key into maps |
| `name(args)` | Call a task-level function (arity is checked at compile time) |
| `!expr` | Logical not (truthiness) |
| `-expr` | Numeric negation (integers checked; `-9223372036854775808` is out of the literal range — write `0 - 9223372036854775807 - 1`) |
| `a + b` `a - b` | Numbers add/subtract (i64 checked, no overflow wrap), strings concatenate |
| `a * b` `a / b` `a % b` | Numbers; int/int is checked — **division or remainder by zero is an error**, and floats keep IEEE semantics |
| `a == b` `a != b` | Equality |
| `a < b` `a <= b` `a > b` `a >= b` | Ordering — numbers compare exactly across int/float; **NaN comparisons are errors** |
| `a && b` `a || b` | **Short-circuit** — the right side (including its native calls) does not run when the left decides |
| `native.db.query(sql, params…)` | → list of row maps |
| `native.db.exec(sql, params…)` | → affected count |
| `native.net.isConnected()` | → bool |
| `native.http.post(url, body)` | → status code; non-2xx is a runtime error |

Precedence, loosest to tightest: `||`, `&&`, `== !=`, `< <= > >=`, `+ -`,
`* / %`, unary `! -`, member/index/call.

## Functions

```ctx
task t() -> void {
    fn fib(n) {
        if n < 2 { return n; }
        return fib(n - 1) + fib(n - 2);
    }
    return fib(10);   // 55
}
```

- Arity is exact and checked at **compile time**; so are unknown function
  names, duplicate functions, and duplicate parameters.
- Recursion is bounded by `MAX_CALL_DEPTH` (128 frames) — deeper recursion
  is a `call depth exceeded` runtime error, never a stack overflow or OOM.
- Functions are not values: a call is always `name(args)` (or a native
  path). Passing functions around is a compile error.

## Values and types

`null`, `bool`, `int` (i64), `float` (f64), `string`, `list`, `map`.
There is no implicit numeric coercion except int⇄float in mixed arithmetic
and comparisons; everything else is a checked `type mismatch` error.
Lists and maps are immutable and reference-counted, so re-using a big list
in a loop is O(1) per reference — assignment rebinds the local, it never
mutates a container.

## Error model

| Class | Examples | CLI exit | Scheduler behavior |
| --- | --- | --- | --- |
| Parse error | bad syntax, nesting > 128, > 100k tokens | 2 | permanent — never retried |
| Compile error | unknown native or function, arity mismatch, program > 65535 instrs | 2 | permanent — never retried |
| Runtime (data) error | list index out of range, integer overflow, division by zero, call depth exceeded, NaN ordering, native failure | 1 | retried with backoff, then parked `failed` |

## Resource bounds

- **Nesting depth** ≤ 128 (`parser::MAX_NESTING_DEPTH`) — parser, compiler,
  and AST drop all recurse, so this bounds them all.
- **Tokens** ≤ 100,000 per script.
- **Instructions** ≤ 1,000,000 per execution (`DEFAULT_INSTRUCTION_BUDGET`)
  — this bounds `while` loops and unbounded `for` inputs alike.
- **Call depth** ≤ 128 (`vm::MAX_CALL_DEPTH`) — bounds recursion memory.

## Deliberately missing (see TODO.md Tier 3)

Closures (functions capture nothing), container mutation (data is
immutable), first-class function values, strings as iterables, a standard
library. The language grows only when a real task needs it.
