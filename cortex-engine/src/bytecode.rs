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
    /// list -> length
    ListLen,
    /// list index -> element (pops index, then list)
    ListGet,
    /// pop `argc` args (reversed), push native result
    CallNative {
        native: NativeId,
        argc: u8,
    },
    /// unconditional jump to instruction index
    Jump(u16),
    /// pop condition; jump if falsy
    JumpIfFalse(u16),
    /// pop condition; jump if truthy (short-circuit `||`)
    JumpIfTrue(u16),
    /// halt, returning top of stack (or null on empty stack)
    Return,
}

/// A compiled task: constant pool + instruction sequence + local count.
#[derive(Clone, Debug, PartialEq)]
pub struct Program {
    pub constants: Vec<Value>,
    pub code: Vec<Instr>,
    pub locals: u16,
}
