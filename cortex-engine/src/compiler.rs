// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! AST -> bytecode compiler. Resolves locals to slot indices at compile time
//! so the VM never does name lookups, and rejects anything it cannot prove
//! static (e.g. calls to unknown functions) -- failures surface at compile
//! time, not mid-flight.

use std::collections::BTreeMap;
use std::fmt;

use crate::ast::{BinOp, Expr, Lit, Stmt, Task};
use crate::bytecode::{Instr, NativeId, Program};
use crate::value::Value;

#[derive(Clone, Debug, PartialEq)]
pub struct CompileError {
    pub message: String,
}

impl fmt::Display for CompileError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.message)
    }
}

/// Native name -> id mapping for `native.<sub>.<call>` paths.
pub fn resolve_native(path: &[String]) -> Option<NativeId> {
    match path {
        [head, sub, call] if head == "native" => match (sub.as_str(), call.as_str()) {
            ("db", "query") => Some(NativeId::DbQuery),
            ("db", "exec") => Some(NativeId::DbExec),
            ("net", "isConnected") => Some(NativeId::NetIsConnected),
            ("http", "post") => Some(NativeId::HttpPost),
            _ => None,
        },
        _ => None,
    }
}

pub fn compile(task: &Task) -> Result<Program, CompileError> {
    let mut compiler = Compiler::default();
    compiler.enter(); // task-level scope
    compiler.stmts(&task.body)?;
    compiler.emit(Instr::Return);
    Ok(Program {
        constants: compiler.constants,
        code: compiler.code,
        locals: compiler.locals,
    })
}

#[derive(Default)]
struct Compiler {
    code: Vec<Instr>,
    constants: Vec<Value>,
    scopes: Vec<BTreeMap<String, u16>>,
    locals: u16,
}

impl Compiler {
    fn emit(&mut self, instr: Instr) {
        self.code.push(instr);
    }

    /// A bytecode offset that is guaranteed to fit the u16 encoding: casting
    /// `as u16` would silently wrap on programs past 65535 instructions.
    fn offset(&self, index: usize) -> Result<u16, CompileError> {
        u16::try_from(index).map_err(|_| CompileError {
            message: "program exceeds 65535 instructions".into(),
        })
    }

    fn constant(&mut self, value: Value) -> Result<u16, CompileError> {
        let index = self
            .constants
            .iter()
            .position(|v| v == &value)
            .unwrap_or_else(|| {
                self.constants.push(value);
                self.constants.len() - 1
            });
        u16::try_from(index).map_err(|_| CompileError {
            message: "constant pool overflow".into(),
        })
    }

    fn declare(&mut self, name: &str) -> Result<u16, CompileError> {
        let scope = self.scopes.last_mut().ok_or_else(|| CompileError {
            message: "no active scope".into(),
        })?;
        let slot = self.locals;
        self.locals = slot.checked_add(1).ok_or_else(|| CompileError {
            message: "too many locals".into(),
        })?;
        scope.insert(name.to_string(), slot);
        Ok(slot)
    }

    fn resolve(&self, name: &str) -> Option<u16> {
        for scope in self.scopes.iter().rev() {
            if let Some(slot) = scope.get(name) {
                return Some(*slot);
            }
        }
        None
    }

    fn enter(&mut self) {
        self.scopes.push(BTreeMap::new());
    }

    fn leave(&mut self) {
        self.scopes.pop();
    }

    fn stmts(&mut self, stmts: &[Stmt]) -> Result<(), CompileError> {
        for stmt in stmts {
            self.stmt(stmt)?;
        }
        Ok(())
    }

    fn stmt(&mut self, stmt: &Stmt) -> Result<(), CompileError> {
        match stmt {
            Stmt::Let { name, expr } => {
                self.expr(expr)?;
                let slot = self.declare(name)?;
                self.emit(Instr::StoreLocal(slot));
            }
            Stmt::If {
                cond,
                then_branch,
                else_branch,
            } => {
                self.expr(cond)?;
                let then_patch = self.code.len();
                self.emit(Instr::JumpIfFalse(0));
                self.stmts(then_branch)?;
                match else_branch.is_empty() {
                    true => {
                        let end = self.offset(self.code.len())?;
                        self.code[then_patch] = Instr::JumpIfFalse(end);
                    }
                    false => {
                        let else_patch = self.code.len();
                        self.emit(Instr::Jump(0));
                        let else_start = self.offset(self.code.len())?;
                        self.code[then_patch] = Instr::JumpIfFalse(else_start);
                        self.stmts(else_branch)?;
                        let end = self.offset(self.code.len())?;
                        self.code[else_patch] = Instr::Jump(end);
                    }
                }
            }
            Stmt::For { var, iter, body } => {
                self.enter();
                self.expr(iter)?;
                let iter_slot = self.declare("#iter")?;
                self.emit(Instr::StoreLocal(iter_slot));
                // index = 0
                let zero = self.constant(Value::Int(0))?;
                self.emit(Instr::Const(zero));
                let idx_slot = self.declare("#idx")?;
                self.emit(Instr::StoreLocal(idx_slot));
                let loop_start = self.offset(self.code.len())?;
                // cond: idx < len(iter)
                self.emit(Instr::LoadLocal(idx_slot));
                self.emit(Instr::LoadLocal(iter_slot));
                self.emit(Instr::ListLen);
                self.emit(Instr::Less);
                let exit_patch = self.code.len();
                self.emit(Instr::JumpIfFalse(0));
                // item = iter[idx]
                self.emit(Instr::LoadLocal(iter_slot));
                self.emit(Instr::LoadLocal(idx_slot));
                self.emit(Instr::ListGet);
                self.enter();
                let item_slot = self.declare(var)?;
                self.emit(Instr::StoreLocal(item_slot));
                self.stmts(body)?;
                self.leave();
                // idx = idx + 1
                self.emit(Instr::LoadLocal(idx_slot));
                let one = self.constant(Value::Int(1))?;
                self.emit(Instr::Const(one));
                self.emit(Instr::Add);
                self.emit(Instr::StoreLocal(idx_slot));
                self.emit(Instr::Jump(loop_start));
                let end = self.offset(self.code.len())?;
                self.code[exit_patch] = Instr::JumpIfFalse(end);
                self.leave();
            }
            Stmt::Expr(expr) => {
                self.expr(expr)?;
                self.emit(Instr::Pop);
            }
            Stmt::Return(expr) => {
                match expr {
                    Some(e) => self.expr(e)?,
                    None => {
                        let null = self.constant(Value::Null)?;
                        self.emit(Instr::Const(null));
                    }
                }
                self.emit(Instr::Return);
            }
        }
        Ok(())
    }

    fn expr(&mut self, expr: &Expr) -> Result<(), CompileError> {
        match expr {
            Expr::Lit(lit) => {
                let value = match lit {
                    Lit::Null => Value::Null,
                    Lit::Bool(b) => Value::Bool(*b),
                    Lit::Int(i) => Value::Int(*i),
                    Lit::Float(f) => Value::Float(*f),
                    Lit::Str(s) => Value::Str(s.clone()),
                };
                let index = self.constant(value)?;
                self.emit(Instr::Const(index));
            }
            Expr::Ident(name) => {
                let slot = self.resolve(name).ok_or_else(|| CompileError {
                    message: format!("undefined variable `{name}`"),
                })?;
                self.emit(Instr::LoadLocal(slot));
            }
            Expr::Index { object, index } => {
                self.expr(object)?;
                self.expr(index)?;
                self.emit(Instr::ListGet);
            }
            Expr::Member(object, field) => {
                // Compile-time resolution of `native.<sub>.<call>` chains:
                // known ones are reserved, unknown ones are rejected, and any
                // other chain is an ordinary (runtime-checked) member access.
                if let Some(path) = flatten_member_chain(object) {
                    if path.first().map(String::as_str) == Some("native") {
                        let mut full = path;
                        full.push(field.clone());
                        if resolve_native(&full).is_some() {
                            return Err(CompileError {
                                message: format!("native `{}` used without a call", full.join(".")),
                            });
                        }
                        return Err(CompileError {
                            message: format!("unknown native `{}`", full.join(".")),
                        });
                    }
                }
                self.expr(object)?;
                let index = self.constant(Value::Str(field.clone()))?;
                self.emit(Instr::Const(index));
                self.emit(Instr::MemberGet);
            }
            Expr::Call { callee, args } => {
                if let Expr::Member(object, field) = &**callee {
                    if let Some(mut path) = flatten_member_chain(object) {
                        path.push(field.clone());
                        if let Some(native) = resolve_native(&path) {
                            let argc = u8::try_from(args.len()).map_err(|_| CompileError {
                                message: "too many arguments".into(),
                            })?;
                            for arg in args {
                                self.expr(arg)?;
                            }
                            self.emit(Instr::CallNative { native, argc });
                            return Ok(());
                        }
                    }
                }
                return Err(CompileError {
                    message: "only native.* calls are supported in this skeleton".into(),
                });
            }
            Expr::UnaryNot(inner) => {
                self.expr(inner)?;
                self.emit(Instr::Not);
            }
            Expr::Binary { op, lhs, rhs } => match op {
                // `&&`/`||` short-circuit: the right side must not run (and
                // its native calls must not fire) when the left side already
                // decides the result. Shape for `a && b`:
                //   eval a; JumpIfFalse(F); eval b; Not; Not; Jump(E)
                //   F: Const false;  E:            (Not;Not normalises to bool)
                // `a || b` mirrors it with JumpIfTrue / Const true.
                BinOp::And | BinOp::Or => {
                    self.expr(lhs)?;
                    let side_patch = self.code.len();
                    self.emit(match op {
                        BinOp::And => Instr::JumpIfFalse(0),
                        _ => Instr::JumpIfTrue(0),
                    });
                    self.expr(rhs)?;
                    self.emit(Instr::Not);
                    self.emit(Instr::Not);
                    let end_patch = self.code.len();
                    self.emit(Instr::Jump(0));
                    let short_value = self.offset(self.code.len())?;
                    self.code[side_patch] = match op {
                        BinOp::And => Instr::JumpIfFalse(short_value),
                        _ => Instr::JumpIfTrue(short_value),
                    };
                    let literal = self.constant(Value::Bool(*op == BinOp::Or))?;
                    self.emit(Instr::Const(literal));
                    let end = self.offset(self.code.len())?;
                    self.code[end_patch] = Instr::Jump(end);
                }
                _ => {
                    self.expr(lhs)?;
                    self.expr(rhs)?;
                    self.emit(match op {
                        BinOp::Eq => Instr::Eq,
                        BinOp::NotEq => Instr::NotEq,
                        BinOp::Less => Instr::Less,
                        BinOp::LessEq => Instr::LessEq,
                        BinOp::Greater => Instr::Greater,
                        BinOp::GreaterEq => Instr::GreaterEq,
                        BinOp::Add => Instr::Add,
                        BinOp::Sub => Instr::Sub,
                        BinOp::And | BinOp::Or => unreachable!("handled above"),
                    });
                }
            },
        }
        Ok(())
    }
}

fn flatten_member_chain(expr: &Expr) -> Option<Vec<String>> {
    match expr {
        Expr::Ident(name) => Some(vec![name.clone()]),
        Expr::Member(object, field) => {
            let mut path = flatten_member_chain(object)?;
            path.push(field.clone());
            Some(path)
        }
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::parser::parse_task;

    #[test]
    fn allocates_distinct_slots_and_resolves_scopes() {
        let task = parse_task("task t() -> void { let a = 1; if true { let b = 2; } let c = a; }")
            .expect("parses");
        let program = compile(&task).expect("compiles");
        // a, b, c each get a slot; `c = a` resolves a's slot, proving scope
        // lookup across the closed if-block.
        assert_eq!(program.locals, 3);
        let stores: Vec<_> = program
            .code
            .iter()
            .filter(|instr| matches!(instr, Instr::StoreLocal(_)))
            .collect();
        assert_eq!(stores.len(), 3);
    }

    #[test]
    fn if_compiles_to_patched_jump_to_end() {
        let task = parse_task("task t() -> void { if false { return; } }").expect("parses");
        let program = compile(&task).expect("compiles");
        let jump = program
            .code
            .iter()
            .find(|instr| matches!(instr, Instr::JumpIfFalse(_)))
            .expect("conditional jump present");
        // The patched target must be the Return's index, not 0.
        let return_index = program
            .code
            .iter()
            .position(|i| *i == Instr::Return)
            .expect("return present");
        match jump {
            // The branch exits to just past the then-branch's final Return
            // (which is followed by the task's implicit trailing Return).
            Instr::JumpIfFalse(target) => assert_eq!(*target as usize, return_index + 1),
            other => panic!("unexpected {other:?}"),
        }
    }

    #[test]
    fn calling_a_non_native_is_a_compile_error() {
        // Parsing accepts the call shape; compiling rejects it.
        let task = parse_task("task t() -> void { for x in items() { } }").expect("parses");
        let err = compile(&task).expect_err("items() is not a native");
        assert!(err.message.contains("only native.* calls"));
    }

    #[test]
    fn native_call_compiles_to_call_native() {
        let task =
            parse_task("task t() -> void { let ok = native.net.isConnected(); }").expect("parses");
        let program = compile(&task).expect("compiles");
        assert!(program.code.iter().any(|i| matches!(
            i,
            Instr::CallNative {
                native: NativeId::NetIsConnected,
                argc: 0
            }
        )));
    }

    #[test]
    fn unknown_natives_and_bare_natives_are_compile_time_errors() {
        let bad = parse_task("task t() -> void { native.db.dropAll(); }").expect("parses");
        assert!(compile(&bad).is_err());
        let bare = parse_task("task t() -> void { let f = native.db.query; }").expect("parses");
        assert!(compile(&bare).is_err());
    }
}

#[cfg(test)]
mod overflow_tests {
    use super::*;
    use crate::parser::parse_task;

    #[test]
    fn programs_past_the_u16_encoding_are_a_compile_error_not_a_wraparound() {
        // 33k flat expression statements (~66k instructions) followed by an
        // `if`: the jump that closes it targets past u16::MAX. (A long
        // `1 + 1 + …` chain is rejected earlier, by the parser's nesting
        // bound; straight-line code has no jump targets to wrap.) The old
        // `as u16` casts wrapped silently and produced a corrupt program;
        // compilation must refuse.
        let mut source = String::from("task t() -> void { ");
        source.push_str(&"1; ".repeat(33_000));
        source.push_str("if true { return; } }");
        let task = parse_task(&source).expect("parses (flat, under the token cap)");
        let err = compile(&task).expect_err("must refuse to emit wraparound jumps");
        assert!(err.message.contains("65535"), "got: {err}");
    }
}
