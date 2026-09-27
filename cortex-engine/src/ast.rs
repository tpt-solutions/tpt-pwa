// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! AST for the cortex DSL task language (spec §6 shape: `task`, `let`, `if`,
//! `for..in`, native calls, member access, arithmetic/comparison).

#[derive(Clone, Debug, PartialEq)]
pub enum Lit {
    Null,
    Bool(bool),
    Int(i64),
    Float(f64),
    Str(String),
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum BinOp {
    Eq,
    NotEq,
    Less,
    LessEq,
    Greater,
    GreaterEq,
    Add,
    Sub,
    And,
    Or,
}

#[derive(Clone, Debug, PartialEq)]
pub enum Expr {
    Lit(Lit),
    Ident(String),
    Member(Box<Expr>, String),
    Index {
        object: Box<Expr>,
        index: Box<Expr>,
    },
    Call {
        callee: Box<Expr>,
        args: Vec<Expr>,
    },
    UnaryNot(Box<Expr>),
    Binary {
        op: BinOp,
        lhs: Box<Expr>,
        rhs: Box<Expr>,
    },
}

#[derive(Clone, Debug, PartialEq)]
pub enum Stmt {
    Let {
        name: String,
        expr: Expr,
    },
    If {
        cond: Expr,
        then_branch: Vec<Stmt>,
        else_branch: Vec<Stmt>,
    },
    For {
        var: String,
        iter: Expr,
        body: Vec<Stmt>,
    },
    Expr(Expr),
    Return(Option<Expr>),
}

/// One `task name() -> void { ... }` script.
#[derive(Clone, Debug, PartialEq)]
pub struct Task {
    pub name: String,
    pub body: Vec<Stmt>,
}
