// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! The stack VM. Totality contract: `run` either finishes or returns an
//! error -- never panics, never blocks, never touches the outside world
//! except through [`NativeEnv`]. An instruction budget bounds runaway loops,
//! and a call-depth cap bounds recursion (memory stays bounded even for
//! `fn f() { f(); }`).

use std::fmt;
use std::rc::Rc;

use crate::bytecode::{Instr, NativeId, Program};
use crate::natives::NativeEnv;
use crate::natives::NativeRegistry;
use crate::value::Value;

/// Default budget: generous for sync-shaped tasks, finite by construction.
pub const DEFAULT_INSTRUCTION_BUDGET: u64 = 1_000_000;

/// Deepest function-call stack. Each live frame holds `locals_base` bookkeeping
/// plus the function's local slots, so this bound is what keeps unbounded
/// recursion (`fn f() { return f(); }`) a bounded-memory error instead of an
/// OOM. Same order of magnitude as the parser's nesting cap.
pub const MAX_CALL_DEPTH: usize = 128;

#[derive(Clone, Debug, PartialEq)]
pub enum VmError {
    StackUnderflow,
    TypeMismatch {
        expected: &'static str,
        found: &'static str,
    },
    UndefinedMember {
        field: String,
    },
    /// A list access with an index the list does not have -- a DATA problem
    /// in the script's inputs, not a malformed program (retrying with
    /// different data can succeed).
    IndexOutOfRange {
        index: i64,
        len: usize,
    },
    /// Integer arithmetic overflowed i64 -- data-driven, like index errors.
    Overflow,
    /// Integer division or remainder by zero.
    DivisionByZero,
    /// Recursion (or a call chain) deeper than [`MAX_CALL_DEPTH`].
    CallDepthExhausted,
    Native(String),
    BudgetExhausted,
    BadProgram(&'static str),
}

impl fmt::Display for VmError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            VmError::StackUnderflow => write!(f, "stack underflow (malformed program)"),
            VmError::TypeMismatch { expected, found } => {
                write!(f, "type mismatch: expected {expected}, found {found}")
            }
            VmError::UndefinedMember { field } => write!(f, "undefined member `{field}`"),
            VmError::IndexOutOfRange { index, len } => {
                write!(f, "list index {index} out of range (len {len})")
            }
            VmError::Overflow => write!(f, "integer overflow"),
            VmError::DivisionByZero => write!(f, "division by zero"),
            VmError::CallDepthExhausted => {
                write!(f, "call depth exceeded {MAX_CALL_DEPTH} frames")
            }
            VmError::Native(message) => write!(f, "native call failed: {message}"),
            VmError::BudgetExhausted => write!(f, "instruction budget exhausted"),
            VmError::BadProgram(reason) => write!(f, "malformed program: {reason}"),
        }
    }
}

/// One live function call: where to resume in the caller, and where the
/// callee's local slots start inside the flat locals vector.
#[derive(Clone, Copy, Debug)]
struct Frame {
    return_pc: usize,
    locals_base: usize,
    /// Locals owned by the CALLER (to truncate back to on return).
    caller_locals_len: usize,
}

pub struct Vm<'env> {
    program: Program,
    env: &'env mut dyn NativeEnv,
    registry: NativeRegistry,
    stack: Vec<Value>,
    /// Flat locals: the main body owns `[0..main_locals)`, each call frame
    /// appends the callee's slots. `LoadLocal`/`StoreLocal` index relative
    /// to the current frame's base.
    locals: Vec<Value>,
    frames: Vec<Frame>,
    budget: u64,
}

impl<'env> Vm<'env> {
    pub fn new(program: Program, env: &'env mut dyn NativeEnv, registry: &NativeRegistry) -> Self {
        let locals = program.locals as usize;
        Vm {
            program,
            env,
            registry: registry.clone(),
            stack: Vec::new(),
            locals: vec![Value::Null; locals],
            frames: Vec::new(),
            budget: DEFAULT_INSTRUCTION_BUDGET,
        }
    }

    pub fn with_budget(mut self, budget: u64) -> Self {
        self.budget = budget;
        self
    }

    /// Base index of the current scope's locals in the flat locals vector.
    fn locals_base(&self) -> usize {
        self.frames.last().map_or(0, |frame| frame.locals_base)
    }

    /// Execute to completion. Errors are values; panics are not possible
    /// (no indexing without bounds checks, no unsafe, no recursion).
    pub fn run(&mut self) -> Result<Value, VmError> {
        let mut pc: usize = 0;
        while pc < self.program.code.len() {
            if self.budget == 0 {
                return Err(VmError::BudgetExhausted);
            }
            self.budget -= 1;
            let instr = self.program.code[pc].clone();
            pc += 1;
            match instr {
                Instr::Const(index) => {
                    let value = self
                        .program
                        .constants
                        .get(index as usize)
                        .ok_or(VmError::BadProgram("constant out of range"))?;
                    self.stack.push(value.clone());
                }
                Instr::LoadLocal(slot) => {
                    let slot = self.locals_base() + slot as usize;
                    let value = self
                        .locals
                        .get(slot)
                        .ok_or(VmError::BadProgram("local out of range"))?;
                    self.stack.push(value.clone());
                }
                Instr::StoreLocal(slot) => {
                    let value = self.pop()?;
                    let slot = self.locals_base() + slot as usize;
                    let slot_ref = self
                        .locals
                        .get_mut(slot)
                        .ok_or(VmError::BadProgram("local out of range"))?;
                    *slot_ref = value;
                }
                Instr::Pop => {
                    self.pop()?;
                }
                Instr::Add => {
                    let (rhs, lhs) = (self.pop()?, self.pop()?);
                    let sum = match (lhs, rhs) {
                        (Value::Int(a), Value::Int(b)) => {
                            a.checked_add(b).map(Value::Int).ok_or(VmError::Overflow)?
                        }
                        (Value::Float(a), Value::Float(b)) => Value::Float(a + b),
                        (Value::Str(a), Value::Str(b)) => Value::Str(a + &b),
                        (Value::Int(a), Value::Float(b)) => Value::Float(a as f64 + b),
                        (Value::Float(a), Value::Int(b)) => Value::Float(a + b as f64),
                        (lhs, rhs) => {
                            return Err(type_mismatch("numbers or strings", &lhs, &rhs));
                        }
                    };
                    self.stack.push(sum);
                }
                Instr::Sub => {
                    let (rhs, lhs) = (self.pop()?, self.pop()?);
                    let diff = match (lhs, rhs) {
                        (Value::Int(a), Value::Int(b)) => {
                            a.checked_sub(b).map(Value::Int).ok_or(VmError::Overflow)?
                        }
                        (Value::Float(a), Value::Float(b)) => Value::Float(a - b),
                        (Value::Int(a), Value::Float(b)) => Value::Float(a as f64 - b),
                        (Value::Float(a), Value::Int(b)) => Value::Float(a - b as f64),
                        (lhs, rhs) => return Err(type_mismatch("numbers", &lhs, &rhs)),
                    };
                    self.stack.push(diff);
                }
                Instr::Mul => {
                    let (rhs, lhs) = (self.pop()?, self.pop()?);
                    let product = match (lhs, rhs) {
                        (Value::Int(a), Value::Int(b)) => {
                            a.checked_mul(b).map(Value::Int).ok_or(VmError::Overflow)?
                        }
                        (Value::Float(a), Value::Float(b)) => Value::Float(a * b),
                        (Value::Int(a), Value::Float(b)) => Value::Float(a as f64 * b),
                        (Value::Float(a), Value::Int(b)) => Value::Float(a * b as f64),
                        (lhs, rhs) => return Err(type_mismatch("numbers", &lhs, &rhs)),
                    };
                    self.stack.push(product);
                }
                // Integers are checked (zero divisor and i64::MIN / -1 are
                // errors, never a panic); floats keep IEEE semantics --
                // infinity/NaN from a zero divisor surface as data errors
                // later, e.g. when compared.
                Instr::Div => {
                    let (rhs, lhs) = (self.pop()?, self.pop()?);
                    let quotient = match (lhs, rhs) {
                        (Value::Int(a), Value::Int(b)) => {
                            if b == 0 {
                                return Err(VmError::DivisionByZero);
                            }
                            a.checked_div(b).map(Value::Int).ok_or(VmError::Overflow)?
                        }
                        (Value::Float(a), Value::Float(b)) => Value::Float(a / b),
                        (Value::Int(a), Value::Float(b)) => Value::Float(a as f64 / b),
                        (Value::Float(a), Value::Int(b)) => Value::Float(a / b as f64),
                        (lhs, rhs) => return Err(type_mismatch("numbers", &lhs, &rhs)),
                    };
                    self.stack.push(quotient);
                }
                Instr::Rem => {
                    let (rhs, lhs) = (self.pop()?, self.pop()?);
                    let remainder = match (lhs, rhs) {
                        (Value::Int(a), Value::Int(b)) => {
                            if b == 0 {
                                return Err(VmError::DivisionByZero);
                            }
                            a.checked_rem(b).map(Value::Int).ok_or(VmError::Overflow)?
                        }
                        (Value::Float(a), Value::Float(b)) => Value::Float(a % b),
                        (Value::Int(a), Value::Float(b)) => Value::Float(a as f64 % b),
                        (Value::Float(a), Value::Int(b)) => Value::Float(a % b as f64),
                        (lhs, rhs) => return Err(type_mismatch("numbers", &lhs, &rhs)),
                    };
                    self.stack.push(remainder);
                }
                Instr::Neg => {
                    let value = self.pop()?;
                    let negated = match value {
                        Value::Int(a) => {
                            a.checked_neg().map(Value::Int).ok_or(VmError::Overflow)?
                        }
                        Value::Float(a) => Value::Float(-a),
                        other => {
                            return Err(VmError::TypeMismatch {
                                expected: "number",
                                found: other.type_name(),
                            })
                        }
                    };
                    self.stack.push(negated);
                }
                Instr::Not => {
                    let value = self.pop()?;
                    self.stack.push(Value::Bool(!value.truthy()));
                }
                Instr::And => {
                    let (rhs, lhs) = (self.pop()?, self.pop()?);
                    self.stack.push(Value::Bool(lhs.truthy() && rhs.truthy()));
                }
                Instr::Or => {
                    let (rhs, lhs) = (self.pop()?, self.pop()?);
                    self.stack.push(Value::Bool(lhs.truthy() || rhs.truthy()));
                }
                Instr::Eq => {
                    let (rhs, lhs) = (self.pop()?, self.pop()?);
                    self.stack.push(Value::Bool(lhs == rhs));
                }
                Instr::NotEq => {
                    let (rhs, lhs) = (self.pop()?, self.pop()?);
                    self.stack.push(Value::Bool(lhs != rhs));
                }
                Instr::Less | Instr::LessEq | Instr::Greater | Instr::GreaterEq => {
                    let (rhs, lhs) = (self.pop()?, self.pop()?);
                    let ordering = compare(&lhs, &rhs)?;
                    let result = match instr {
                        Instr::Less => ordering == std::cmp::Ordering::Less,
                        Instr::LessEq => ordering != std::cmp::Ordering::Greater,
                        Instr::Greater => ordering == std::cmp::Ordering::Greater,
                        _ => ordering != std::cmp::Ordering::Less,
                    };
                    self.stack.push(Value::Bool(result));
                }
                Instr::MemberGet => {
                    let key = self.pop()?;
                    let object = self.pop()?;
                    let field = match key {
                        Value::Str(field) => field,
                        other => {
                            return Err(VmError::TypeMismatch {
                                expected: "string key",
                                found: other.type_name(),
                            })
                        }
                    };
                    match object {
                        Value::Map(map) => match map.get(&field) {
                            Some(value) => self.stack.push(value.clone()),
                            None => return Err(VmError::UndefinedMember { field }),
                        },
                        other => {
                            return Err(VmError::TypeMismatch {
                                expected: "map",
                                found: other.type_name(),
                            })
                        }
                    }
                }
                Instr::ListLen => {
                    let list = self.pop()?;
                    match list {
                        Value::List(items) => self.stack.push(Value::Int(items.len() as i64)),
                        other => {
                            return Err(VmError::TypeMismatch {
                                expected: "list",
                                found: other.type_name(),
                            })
                        }
                    }
                }
                // `list[int]` and `map[str]` share one instruction; the
                // runtime error names what the script actually indexed.
                Instr::IndexGet => {
                    let index = self.pop()?;
                    let container = self.pop()?;
                    match (container, index) {
                        (Value::List(items), Value::Int(index)) => {
                            let item = if index >= 0 {
                                items.get(index as usize)
                            } else {
                                // Negative indices must not wrap through the
                                // `as usize` cast into a bogus lookup.
                                None
                            };
                            let item = item.ok_or(VmError::IndexOutOfRange {
                                index,
                                len: items.len(),
                            })?;
                            self.stack.push(item.clone());
                        }
                        (Value::List(_), other) => {
                            return Err(VmError::TypeMismatch {
                                expected: "int index",
                                found: other.type_name(),
                            })
                        }
                        (Value::Map(map), Value::Str(key)) => match map.get(&key) {
                            Some(value) => self.stack.push(value.clone()),
                            None => return Err(VmError::UndefinedMember { field: key }),
                        },
                        (Value::Map(_), other) => {
                            return Err(VmError::TypeMismatch {
                                expected: "string key",
                                found: other.type_name(),
                            })
                        }
                        (other, _) => {
                            return Err(VmError::TypeMismatch {
                                expected: "list or map",
                                found: other.type_name(),
                            })
                        }
                    }
                }
                Instr::BuildList(argc) => {
                    let count = argc as usize;
                    if count > self.stack.len() {
                        return Err(VmError::StackUnderflow);
                    }
                    let items = self.stack.split_off(self.stack.len() - count);
                    self.stack.push(Value::List(Rc::new(items)));
                }
                Instr::BuildMap(argc) => {
                    let count = argc as usize;
                    if count * 2 > self.stack.len() {
                        return Err(VmError::StackUnderflow);
                    }
                    let flat = self.stack.split_off(self.stack.len() - count * 2);
                    // Stack order is k1 v1 k2 v2 ...: BTreeMap insert means
                    // a duplicate key keeps the LAST occurrence
                    // (JSON-object semantics).
                    let mut map = std::collections::BTreeMap::new();
                    let mut i = 0;
                    while i < flat.len() {
                        let key = match &flat[i] {
                            Value::Str(key) => key.clone(),
                            other => {
                                return Err(VmError::TypeMismatch {
                                    expected: "string key",
                                    found: other.type_name(),
                                })
                            }
                        };
                        map.insert(key, flat[i + 1].clone());
                        i += 2;
                    }
                    self.stack.push(Value::Map(Rc::new(map)));
                }
                Instr::CallFn { index, argc } => {
                    let function = self
                        .program
                        .functions
                        .get(index as usize)
                        .ok_or(VmError::BadProgram("function index out of range"))?;
                    if self.frames.len() >= MAX_CALL_DEPTH {
                        return Err(VmError::CallDepthExhausted);
                    }
                    let argc = argc as usize;
                    if argc > self.stack.len() {
                        return Err(VmError::StackUnderflow);
                    }
                    // Call sites are arity-checked at compile time; this is
                    // the guard for hand-built (adversarial) programs.
                    if argc != function.params as usize {
                        return Err(VmError::BadProgram("function arity mismatch"));
                    }
                    let args = self.stack.split_off(self.stack.len() - argc);
                    let locals_base = self.locals.len();
                    self.locals.extend(args);
                    // A hand-built FunctionInfo could claim fewer slots than
                    // its params; max() keeps the args addressable (the
                    // compiler always emits locals >= params).
                    let slots = (function.locals as usize).max(argc);
                    self.locals.resize(locals_base + slots, Value::Null);
                    self.frames.push(Frame {
                        return_pc: pc,
                        locals_base,
                        caller_locals_len: locals_base,
                    });
                    pc = function.start as usize;
                }
                Instr::CallNative { native, argc } => self.call_native(native, argc)?,
                Instr::Jump(target) => {
                    pc = self.target(target)?;
                }
                Instr::JumpIfFalse(target) => {
                    let cond = self.pop()?;
                    if !cond.truthy() {
                        pc = self.target(target)?;
                    }
                }
                Instr::JumpIfTrue(target) => {
                    let cond = self.pop()?;
                    if cond.truthy() {
                        pc = self.target(target)?;
                    }
                }
                Instr::Return => {
                    let value = self.pop().unwrap_or(Value::Null);
                    match self.frames.pop() {
                        // Main body: the program is done.
                        None => return Ok(value),
                        Some(frame) => {
                            self.locals.truncate(frame.caller_locals_len);
                            pc = frame.return_pc;
                            self.stack.push(value);
                        }
                    }
                }
            }
        }
        Err(VmError::BadProgram("fell off the end without Return"))
    }

    fn target(&self, target: u16) -> Result<usize, VmError> {
        let index = target as usize;
        if index <= self.program.code.len() {
            Ok(index)
        } else {
            Err(VmError::BadProgram("jump target out of range"))
        }
    }

    fn pop(&mut self) -> Result<Value, VmError> {
        self.stack.pop().ok_or(VmError::StackUnderflow)
    }

    fn call_native(&mut self, native: NativeId, argc: u8) -> Result<(), VmError> {
        if (argc as usize) > self.stack.len() {
            return Err(VmError::StackUnderflow);
        }
        if !self.registry.accepts(native, argc) {
            return Err(VmError::BadProgram("native arity mismatch"));
        }
        let mut args = Vec::with_capacity(argc as usize);
        for _ in 0..argc {
            args.push(self.pop()?);
        }
        args.reverse();
        let result = match native {
            NativeId::DbQuery => {
                let (sql, params) = sql_args(args)?;
                let rows = self.env.db_query(&sql, &params).map_err(VmError::Native)?;
                Value::List(Rc::new(rows))
            }
            NativeId::DbExec => {
                let (sql, params) = sql_args(args)?;
                self.env.db_exec(&sql, &params).map_err(VmError::Native)?
            }
            NativeId::NetIsConnected => {
                Value::Bool(self.env.net_is_connected().map_err(VmError::Native)?)
            }
            NativeId::HttpPost => {
                let mut iter = args.into_iter();
                let url = match iter.next() {
                    Some(Value::Str(url)) => url,
                    Some(other) => {
                        return Err(VmError::TypeMismatch {
                            expected: "string url",
                            found: other.type_name(),
                        })
                    }
                    None => return Err(VmError::StackUnderflow),
                };
                let body = iter.next().unwrap_or(Value::Null);
                self.env.http_post(&url, &body).map_err(VmError::Native)?
            }
        };
        self.stack.push(result);
        Ok(())
    }
}

fn sql_args(args: Vec<Value>) -> Result<(String, Vec<Value>), VmError> {
    let mut iter = args.into_iter();
    let sql = match iter.next() {
        Some(Value::Str(s)) => s,
        Some(other) => {
            return Err(VmError::TypeMismatch {
                expected: "string sql",
                found: other.type_name(),
            })
        }
        None => return Err(VmError::StackUnderflow),
    };
    Ok((sql, iter.collect()))
}

fn compare(lhs: &Value, rhs: &Value) -> Result<std::cmp::Ordering, VmError> {
    match lhs.total_cmp(rhs) {
        Some(ordering) => Ok(ordering),
        // NaN has no ordering: report it as the data problem it is instead of
        // silently ordering it "less than everything".
        None if matches!(lhs, Value::Float(a) if a.is_nan())
            || matches!(rhs, Value::Float(b) if b.is_nan()) =>
        {
            Err(VmError::TypeMismatch {
                expected: "an orderable number",
                found: "NaN",
            })
        }
        None => Err(type_mismatch("comparable values", lhs, rhs)),
    }
}

fn type_mismatch(expected: &'static str, lhs: &Value, rhs: &Value) -> VmError {
    let found: &'static str = match (lhs, rhs) {
        (Value::Str(_), _) | (_, Value::Str(_)) => "string",
        (Value::Float(_), _) | (_, Value::Float(_)) => "float",
        (Value::Int(_), _) | (_, Value::Int(_)) => "int",
        (Value::Bool(_), _) | (_, Value::Bool(_)) => "bool",
        (Value::List(_), _) | (_, Value::List(_)) => "list",
        (Value::Map(_), _) | (_, Value::Map(_)) => "map",
        _ => "null",
    };
    VmError::TypeMismatch { expected, found }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::compiler::compile;
    use crate::natives::{row, MemoryNative, NativeRegistry};
    use crate::parser::parse_task;

    fn run(source: &str) -> Result<Value, VmError> {
        let task = parse_task(source).expect("parses");
        let program = compile(&task).expect("compiles");
        let registry = NativeRegistry::standard();
        let mut env = MemoryNative::default();
        Vm::new(program, &mut env, &registry).run()
    }

    #[test]
    fn arithmetic_and_comparisons_evaluate() {
        assert_eq!(
            run("task t() -> void { return 10 - 2 + 1; }").unwrap(),
            Value::Int(9)
        );
        assert_eq!(
            run("task t() -> void { return 1 + 2 < 4; }").unwrap(),
            Value::Bool(true)
        );
        assert_eq!(
            run("task t() -> void { return \"a\" + \"b\" == \"ab\"; }").unwrap(),
            Value::Bool(true)
        );
        assert_eq!(
            run("task t() -> void { return !false; }").unwrap(),
            Value::Bool(true)
        );
        assert_eq!(
            run("task t() -> void { return true && 1 < 2; }").unwrap(),
            Value::Bool(true)
        );
        assert_eq!(
            run("task t() -> void { return false || 0; }").unwrap(),
            Value::Bool(false)
        );
    }

    #[test]
    fn integer_overflow_is_an_error_not_a_panic() {
        let err =
            run("task t() -> void { return 9223372036854775807 + 1; }").expect_err("must overflow");
        assert_eq!(err, VmError::Overflow);
    }

    #[test]
    fn member_access_reads_maps_and_lists() {
        let source = r#"
            task t() -> void {
                let rows = native.db.query("SELECT 1");
                let first = rows[0];
                return first.id;
            }
        "#;
        let task = parse_task(source).expect("parses");
        let program = compile(&task).expect("compiles");
        let registry = NativeRegistry::standard();
        let mut env = MemoryNative {
            rows: vec![row(&[("id", Value::Str("41".into()))])],
            ..MemoryNative::default()
        };
        let mut vm = Vm::new(program, &mut env, &registry);
        assert_eq!(vm.run().unwrap(), Value::Str("41".into()));
    }

    #[test]
    fn undefined_member_is_a_runtime_error() {
        // One pending row, so indexing succeeds and the *member* lookup fails.
        let source = r#"
            task t() -> void {
                let rows = native.db.query("SELECT 1");
                return rows[0].missing;
            }
        "#;
        let task = parse_task(source).expect("parses");
        let program = compile(&task).expect("compiles");
        let registry = NativeRegistry::standard();
        let mut env = MemoryNative {
            rows: vec![row(&[("id", Value::Str("41".into()))])],
            ..MemoryNative::default()
        };
        let mut vm = Vm::new(program, &mut env, &registry);
        assert_eq!(
            vm.run().unwrap_err(),
            VmError::UndefinedMember {
                field: "missing".into()
            }
        );
    }

    #[test]
    fn budget_bounds_even_long_loops() {
        // A for-in over a big list with a tiny budget: must stop, not grind.
        let task = parse_task(
            "task t() -> void { let rows = native.db.query(\"s\"); for r in rows { let x = r; } }",
        )
        .expect("parses");
        let program = compile(&task).expect("compiles");
        let registry = NativeRegistry::standard();
        let mut env = MemoryNative {
            rows: (0..1000).map(Value::Int).collect(),
            ..MemoryNative::default()
        };
        let mut vm = Vm::new(program, &mut env, &registry).with_budget(50);
        assert_eq!(vm.run().unwrap_err(), VmError::BudgetExhausted);
    }

    #[test]
    fn type_confusions_return_errors() {
        assert!(run("task t() -> void { return 1 + \"a\"; }").is_err());
        assert!(run("task t() -> void { return 1 < \"x\"; }").is_err());
        assert!(run("task t() -> void { let x = 1; return x.field; }").is_err());
    }
}

#[cfg(test)]
mod short_circuit_and_data_tests {
    use super::*;
    use crate::compiler::compile;
    use crate::natives::{row, MemoryNative, NativeRegistry};
    use crate::parser::parse_task;

    fn run(source: &str) -> Result<Value, VmError> {
        let task = parse_task(source).expect("parses");
        let program = compile(&task).expect("compiles");
        let registry = NativeRegistry::standard();
        let mut env = MemoryNative::default();
        Vm::new(program, &mut env, &registry).run()
    }

    fn run_with(source: &str, env: &mut MemoryNative) -> Result<Value, VmError> {
        let task = parse_task(source).expect("parses");
        let program = compile(&task).expect("compiles");
        let registry = NativeRegistry::standard();
        Vm::new(program, env, &registry).run()
    }

    #[test]
    fn and_short_circuits_the_right_side() {
        // `false && …` must not fire the http.post on the right side.
        let source = r#"
            task t() -> void {
                return false && native.http.post("https://side-effect", 1);
            }
        "#;
        let mut env = MemoryNative::default();
        assert_eq!(run_with(source, &mut env).unwrap(), Value::Bool(false));
        assert!(
            env.posts.is_empty(),
            "right side of && ran despite false lhs"
        );
    }

    #[test]
    fn or_short_circuits_the_right_side() {
        let source = r#"
            task t() -> void {
                return true || native.http.post("https://side-effect", 1);
            }
        "#;
        let mut env = MemoryNative::default();
        assert_eq!(run_with(source, &mut env).unwrap(), Value::Bool(true));
        assert!(
            env.posts.is_empty(),
            "right side of || ran despite true lhs"
        );
    }

    #[test]
    fn short_circuit_results_are_bools_and_both_sides_run_when_needed() {
        let mut env = MemoryNative::default();
        assert_eq!(
            run_with("task t() -> void { return 1 < 2 && 3 < 4; }", &mut env).unwrap(),
            Value::Bool(true)
        );
        assert_eq!(
            run_with("task t() -> void { return 0 || \"x\"; }", &mut env).unwrap(),
            Value::Bool(true) // truthy("x"), not the raw value
        );
    }

    #[test]
    fn nan_comparisons_are_data_errors_not_silent_orderings() {
        // NaN reaches the VM through host data (the DSL has no float ops that
        // produce it): a row carrying NaN must make ordering an error rather
        // than silently comparing as "less than everything".
        let source = r#"
            task t() -> void {
                let rows = native.db.query("SELECT nan");
                return rows[0].v < 1;
            }
        "#;
        let task = parse_task(source).expect("parses");
        let program = compile(&task).expect("compiles");
        let registry = NativeRegistry::standard();
        let mut env = MemoryNative {
            rows: vec![row(&[("v", Value::Float(f64::NAN))])],
            ..MemoryNative::default()
        };
        let mut vm = Vm::new(program, &mut env, &registry);
        assert_eq!(
            vm.run().unwrap_err(),
            VmError::TypeMismatch {
                expected: "an orderable number",
                found: "NaN"
            }
        );
    }

    #[test]
    fn mixed_int_float_comparisons_are_exact() {
        // 2^53 + 1 is not representable as f64: the old f64 cast compared it
        // equal to 2^53. Exact comparison must keep them distinct.
        assert_eq!(
            run("task t() -> void { return 9007199254740993 > 9007199254740992.0; }").unwrap(),
            Value::Bool(true)
        );
        assert_eq!(
            run("task t() -> void { return 9007199254740992.0 < 9007199254740993; }").unwrap(),
            Value::Bool(true)
        );
        assert_eq!(
            run("task t() -> void { return 0 - 9007199254740993 < 0.0 - 9007199254740992.0; }")
                .unwrap(),
            Value::Bool(true)
        );
    }

    #[test]
    fn list_index_errors_are_data_errors_with_context() {
        let source = r#"
            task t() -> void {
                let rows = native.db.query("SELECT 1");
                return rows[7];
            }
        "#;
        let task = parse_task(source).expect("parses");
        let program = compile(&task).expect("compiles");
        let registry = NativeRegistry::standard();
        let mut env = MemoryNative {
            rows: vec![row(&[("id", Value::Int(1))])],
            ..MemoryNative::default()
        };
        let mut vm = Vm::new(program, &mut env, &registry);
        assert_eq!(
            vm.run().unwrap_err(),
            VmError::IndexOutOfRange { index: 7, len: 1 }
        );
    }

    #[test]
    fn negative_indices_do_not_wrap_around() {
        let source = r#"
            task t() -> void {
                let rows = native.db.query("s");
                return rows[0 - 1];
            }
        "#;
        let task = parse_task(source).expect("parses");
        let program = compile(&task).expect("compiles");
        let registry = NativeRegistry::standard();
        let mut env = MemoryNative {
            rows: vec![row(&[("id", Value::Int(1))])],
            ..MemoryNative::default()
        };
        let mut vm = Vm::new(program, &mut env, &registry);
        assert_eq!(
            vm.run().unwrap_err(),
            VmError::IndexOutOfRange { index: -1, len: 1 }
        );
    }
}
