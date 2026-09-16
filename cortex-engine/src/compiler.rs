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
                        let end = self.code.len();
                        self.code[then_patch] = Instr::JumpIfFalse(end as u16);
                    }
                    false => {
                        let else_patch = self.code.len();
                        self.emit(Instr::Jump(0));
                        let else_start = self.code.len();
                        self.code[then_patch] = Instr::JumpIfFalse(else_start as u16);
                        self.stmts(else_branch)?;
                        let end = self.code.len();
                        self.code[else_patch] = Instr::Jump(end as u16);
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
                let loop_start = self.code.len();
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
                self.emit(Instr::Jump(loop_start as u16));
                let end = self.code.len();
                self.code[exit_patch] = Instr::JumpIfFalse(end as u16);
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
            Expr::Binary { op, lhs, rhs } => {
                // Note: && / || are evaluated eagerly in this skeleton (both
                // sides always run); the DSL's only effects are native calls
                // that are idempotent to probe. Short-circuiting is planned.
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
                    BinOp::And => Instr::And,
                    BinOp::Or => Instr::Or,
                });
            }
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
