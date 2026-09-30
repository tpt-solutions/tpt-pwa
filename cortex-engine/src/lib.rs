// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! cortex-engine: the bytecode VM for tpt-cortex task scripts (spec §3/§6).
//!
//! Pipeline: source -> [`lexer`] -> [`parser`] (AST) -> [`compiler`]
//! (bytecode + constant pool) -> [`vm`] (stack machine). Task scripts call
//! *native bindings* (`native.db`, `native.net`, `native.http`) that the host
//! (cortex-daemon) provides via the [`natives::NativeEnv`] trait -- the VM
//! itself does no I/O, which keeps the execution core pure and auditable.
//!
//! Safety posture (see docs/formal-verification.md): the VM is total -- every
//! instruction either steps or returns an error; there is no `unsafe`, no
//! recursion, and a hard instruction budget bounds every loop. The parser
//! bounds ITS recursion too (nesting depth and token caps, see
//! `parser::MAX_NESTING_DEPTH`), which also keeps the AST's recursive `Drop`
//! off the native stack's worst case; data errors (index range, overflow,
//! NaN ordering) are distinct error variants, and the CLI reports permanent
//! (parse/compile) vs transient (runtime) failures with exit codes 2 / 1.

pub mod ast;
pub mod bytecode;
pub mod compiler;
pub mod host;
pub mod lexer;
pub mod natives;
pub mod parser;
pub mod value;
pub mod vm;

pub use compiler::CompileError;
pub use natives::NativeEnv;
pub use parser::ParseError;
pub use vm::{Vm, VmError};

use natives::NativeRegistry;

/// Parse, compile and run a `*.ctx` task script against a native environment.
/// Convenience for the daemon: one call from source to result.
pub fn run_source(source: &str, env: &mut dyn NativeEnv) -> Result<value::Value, EngineError> {
    let task = parser::parse_task(source)?;
    let program = compiler::compile(&task)?;
    let registry = NativeRegistry::standard();
    let mut vm = Vm::new(program, env, &registry);
    Ok(vm.run()?)
}

/// Every failure mode of the engine, front to back.
#[derive(Debug, PartialEq)]
pub enum EngineError {
    Parse(ParseError),
    Compile(CompileError),
    Vm(VmError),
}

impl std::fmt::Display for EngineError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            EngineError::Parse(e) => write!(f, "parse error: {e}"),
            EngineError::Compile(e) => write!(f, "compile error: {e}"),
            EngineError::Vm(e) => write!(f, "vm error: {e}"),
        }
    }
}

impl std::error::Error for EngineError {}

impl From<ParseError> for EngineError {
    fn from(e: ParseError) -> Self {
        EngineError::Parse(e)
    }
}

impl From<CompileError> for EngineError {
    fn from(e: CompileError) -> Self {
        EngineError::Compile(e)
    }
}

impl From<VmError> for EngineError {
    fn from(e: VmError) -> Self {
        EngineError::Vm(e)
    }
}
