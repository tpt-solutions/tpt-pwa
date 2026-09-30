// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! The bytecode. Small on purpose: the VM's totality argument (docs
//! docs/formal-verification.md) is only as strong as the instruction set is
//! small.

use crate::value::Value;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum NativeId {
    DbQuery,
    DbExec,
    NetIsConnected,
    HttpPost,
    /// The task's outbox entries — the sync script's data source (the
    /// daemon injects them per task). Distinct from `db.query`, which is a
    /// real SQL query against the daemon's database.
    OutboxEntries,
}

#[derive(Clone, Debug, PartialEq)]
pub enum Instr {
    /// Push a constant-pool value.
    Const(u16),
    LoadLocal(u16),
    StoreLocal(u16),
    Pop,
    Add,
    Sub,
    Mul,
    Div,
    Rem,
    /// Numeric negation (checked on integers).
    Neg,
    Not,
    And,
    Or,
    Eq,
    NotEq,
    Less,
    LessEq,
    Greater,
    GreaterEq,
    /// map key -> value (pops string key, then map)
    MemberGet,
    /// list -> length (the `for..in` iterator protocol)
    ListLen,
    /// container index -> element. Lists take an int index, maps a string
    /// key (pops index, then container).
    IndexGet,
    /// pop `argc` element values, push `[...]` (last popped is last element)
    BuildList(u16),
    /// pop `2 * argc` values as key/value pairs, push `{...}`. Keys must be
    /// strings at runtime; duplicate keys keep the LAST value (JSON-ish).
    BuildMap(u16),
    /// pop `argc` args (reversed), push native result
    CallNative {
        native: NativeId,
        argc: u8,
    },
    /// call `functions[index]` with `argc` args (already on the stack):
    /// pushes a frame, execution continues at the function's start offset.
    /// Arity is checked at compile time; the VM checks the index and depth.
    CallFn {
        index: u16,
        argc: u8,
    },
    /// unconditional jump to instruction index
    Jump(u16),
    /// pop condition; jump if falsy
    JumpIfFalse(u16),
    /// pop condition; jump if truthy (short-circuit `||`)
    JumpIfTrue(u16),
    /// return from the current function (or halt, when in the main body),
    /// yielding top of stack (or null on empty stack)
    Return,
}

/// One compiled function: flattened start offset in `Program::code`, its
/// parameter count, and its total local count (params + locals).
#[derive(Clone, Debug, PartialEq)]
pub struct FunctionInfo {
    pub name: String,
    pub start: u16,
    pub params: u8,
    pub locals: u16,
}

/// A compiled task: constant pool + flattened instruction stream (main body
/// first, then each function's body) + per-scope local counts.
#[derive(Clone, Debug, PartialEq)]
pub struct Program {
    pub constants: Vec<Value>,
    pub code: Vec<Instr>,
    pub functions: Vec<FunctionInfo>,
    /// locals of the main body; functions carry their own counts.
    pub locals: u16,
}
