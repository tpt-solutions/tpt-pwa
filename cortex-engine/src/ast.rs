// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! AST for the cortex DSL task language: `task`, `fn`, `let`, assignment,
//! `if`, `for..in`, `while`, native calls, member/index access, list and map
//! literals, arithmetic/comparison.

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
    Mul,
    Div,
    Rem,
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
    UnaryNeg(Box<Expr>),
    /// `[a, b, c]`
    List(Vec<Expr>),
    /// `{"key": expr, ...}` -- keys are arbitrary expressions evaluated to
    /// strings at runtime.
    Map(Vec<(Expr, Expr)>),
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
    /// Re-assignment of an existing local (`x = expr;`). Containers stay
    /// immutable: only plain locals can be assigned.
    Assign {
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
    While {
        cond: Expr,
        body: Vec<Stmt>,
    },
    Expr(Expr),
    Return(Option<Expr>),
}

/// One `fn name(params) { ... }` declared in the task body. Functions close
/// over nothing: they see their parameters, their own locals, and other
/// functions -- never the caller's locals.
#[derive(Clone, Debug, PartialEq)]
pub struct Function {
    pub name: String,
    pub params: Vec<String>,
    pub body: Vec<Stmt>,
}

/// One `task name() -> void { ... }` script.
#[derive(Clone, Debug, PartialEq)]
pub struct Task {
    pub name: String,
    pub functions: Vec<Function>,
    pub body: Vec<Stmt>,
}
