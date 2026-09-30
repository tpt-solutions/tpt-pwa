// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! Recursive-descent parser for a single `task` definition.

use crate::ast::{BinOp, Expr, Function, Lit, Stmt, Task};
use crate::lexer::{lex, Tok, Token};

#[derive(Clone, Debug, PartialEq)]
pub struct ParseError {
    pub message: String,
    pub line: u32,
    pub col: u32,
}

impl std::fmt::Display for ParseError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{} at {}:{}", self.message, self.line, self.col)
    }
}

/// Deepest expression/statement nesting accepted. The parser, the compiler,
/// and the AST's recursive `Drop` all walk this structure recursively, so
/// this bound is what keeps a hostile script (`((((…))))` a million deep)
/// from overflowing the native stack -- the precondition for the "no
/// recursion unbounded" clause of the totality contract in lib.rs.
pub const MAX_NESTING_DEPTH: u32 = 128;

/// Most tokens one script may carry. A cap here bounds lexing and the
/// parser's token vector no matter how pathological the input.
pub const MAX_TOKENS: usize = 100_000;

struct Parser {
    tokens: Vec<Token>,
    pos: usize,
    depth: u32,
}

/// Parse a complete task script: exactly one `task` definition.
pub fn parse_task(source: &str) -> Result<Task, ParseError> {
    let tokens = lex(source).map_err(|e| ParseError {
        message: e.message,
        line: e.line,
        col: e.col,
    })?;
    if tokens.len() > MAX_TOKENS {
        return Err(ParseError {
            message: format!("script exceeds the {} token limit", MAX_TOKENS),
            line: 1,
            col: 1,
        });
    }
    let mut parser = Parser {
        tokens,
        pos: 0,
        depth: 0,
    };
    parser.parse_task_def()
}

impl Parser {
    /// Enter one nesting level; every recursive descent point wraps its body
    /// with `enter`/`leave` so a hostile script cannot overflow the native
    /// stack. (An RAII guard would pin the `&mut self` borrow across the
    /// whole guarded body, so the pair is explicit.)
    fn enter(&mut self) -> Result<(), ParseError> {
        self.depth += 1;
        if self.depth > MAX_NESTING_DEPTH {
            self.depth -= 1;
            let token = &self.tokens[self.pos];
            return Err(ParseError {
                message: format!("nesting deeper than {MAX_NESTING_DEPTH} levels is rejected"),
                line: token.line,
                col: token.col,
            });
        }
        Ok(())
    }

    fn leave(&mut self) {
        self.depth -= 1;
    }
    fn peek(&self) -> &Tok {
        &self.tokens[self.pos].tok
    }

    /// Look-ahead token (0 = current). Bounded by the Eof sentinel: the
    /// tokenizer never advances past it, so `pos + offset` clamps safely.
    fn peek_at(&self, offset: usize) -> &Tok {
        let index = (self.pos + offset).min(self.tokens.len() - 1);
        &self.tokens[index].tok
    }

    fn advance(&mut self) -> Token {
        let token = self.tokens[self.pos].clone();
        if self.pos < self.tokens.len() - 1 {
            self.pos += 1;
        }
        token
    }

    fn error<T>(&self, message: impl Into<String>) -> Result<T, ParseError> {
        let token = &self.tokens[self.pos];
        Err(ParseError {
            message: message.into(),
            line: token.line,
            col: token.col,
        })
    }

    fn expect(&mut self, expected: &Tok) -> Result<(), ParseError> {
        if self.peek() == expected {
            self.advance();
            Ok(())
        } else {
            self.error(format!("expected {expected:?}, found {:?}", self.peek()))
        }
    }

    fn expect_ident(&mut self) -> Result<String, ParseError> {
        match self.peek().clone() {
            Tok::Ident(name) => {
                self.advance();
                Ok(name)
            }
            other => self.error(format!("expected identifier, found {other:?}")),
        }
    }

    fn parse_task_def(&mut self) -> Result<Task, ParseError> {
        self.expect(&Tok::Task)?;
        let name = self.expect_ident()?;
        self.expect(&Tok::LParen)?;
        self.expect(&Tok::RParen)?;
        self.expect(&Tok::Arrow)?;
        self.expect(&Tok::Void)?;
        // `fn` definitions live only at the top of the task body: nested
        // functions would need closures to be meaningful, and the language
        // has none by design.
        let mut functions = Vec::new();
        let mut body = Vec::new();
        self.expect(&Tok::LBrace)?;
        while *self.peek() != Tok::RBrace {
            if *self.peek() == Tok::Fn {
                functions.push(self.parse_fn()?);
            } else {
                body.push(self.parse_stmt()?);
            }
        }
        self.expect(&Tok::RBrace)?;
        self.expect(&Tok::Eof)?;
        Ok(Task {
            name,
            functions,
            body,
        })
    }

    fn parse_fn(&mut self) -> Result<Function, ParseError> {
        self.enter()?;
        let result = self.parse_fn_inner();
        self.leave();
        result
    }

    fn parse_fn_inner(&mut self) -> Result<Function, ParseError> {
        self.expect(&Tok::Fn)?;
        let name = self.expect_ident()?;
        self.expect(&Tok::LParen)?;
        let mut params = Vec::new();
        if *self.peek() != Tok::RParen {
            loop {
                params.push(self.expect_ident()?);
                if *self.peek() == Tok::Comma {
                    self.advance();
                } else {
                    break;
                }
            }
        }
        self.expect(&Tok::RParen)?;
        let body = self.parse_block()?;
        Ok(Function { name, params, body })
    }

    fn parse_block(&mut self) -> Result<Vec<Stmt>, ParseError> {
        self.expect(&Tok::LBrace)?;
        let mut stmts = Vec::new();
        while *self.peek() != Tok::RBrace {
            stmts.push(self.parse_stmt()?);
        }
        self.expect(&Tok::RBrace)?;
        Ok(stmts)
    }

    fn parse_stmt(&mut self) -> Result<Stmt, ParseError> {
        self.enter()?;
        let result = self.parse_stmt_inner();
        self.leave();
        result
    }

    fn parse_stmt_inner(&mut self) -> Result<Stmt, ParseError> {
        match self.peek().clone() {
            Tok::Let => {
                self.advance();
                let name = self.expect_ident()?;
                self.expect(&Tok::Assign)?;
                let expr = self.parse_expr()?;
                self.expect(&Tok::Semicolon)?;
                Ok(Stmt::Let { name, expr })
            }
            Tok::If => self.parse_if(),
            Tok::For => {
                self.advance();
                let var = self.expect_ident()?;
                self.expect(&Tok::In)?;
                let iter = self.parse_expr()?;
                let body = self.parse_block()?;
                Ok(Stmt::For { var, iter, body })
            }
            Tok::While => {
                self.advance();
                let cond = self.parse_expr()?;
                let body = self.parse_block()?;
                Ok(Stmt::While { cond, body })
            }
            // `x = expr;` re-assignment, distinguished from an expression
            // statement by one token of lookahead (`==` lexes as Eq, so
            // `x == y;` still parses as a comparison).
            Tok::Ident(name) if *self.peek_at(1) == Tok::Assign => {
                let name = name.clone();
                self.advance();
                self.advance();
                let expr = self.parse_expr()?;
                self.expect(&Tok::Semicolon)?;
                Ok(Stmt::Assign { name, expr })
            }
            Tok::Return => {
                self.advance();
                if *self.peek() == Tok::Semicolon {
                    self.advance();
                    return Ok(Stmt::Return(None));
                }
                let expr = self.parse_expr()?;
                self.expect(&Tok::Semicolon)?;
                Ok(Stmt::Return(Some(expr)))
            }
            _ => {
                let expr = self.parse_expr()?;
                self.expect(&Tok::Semicolon)?;
                Ok(Stmt::Expr(expr))
            }
        }
    }

    fn parse_if(&mut self) -> Result<Stmt, ParseError> {
        self.enter()?;
        let result = self.parse_if_inner();
        self.leave();
        result
    }

    /// `else if` chains recurse here, hence the depth bound.
    fn parse_if_inner(&mut self) -> Result<Stmt, ParseError> {
        self.expect(&Tok::If)?;
        let cond = self.parse_expr()?;
        let then_branch = self.parse_block()?;
        let else_branch = if *self.peek() == Tok::Else {
            self.advance();
            if *self.peek() == Tok::If {
                vec![self.parse_if()?]
            } else {
                self.parse_block()?
            }
        } else {
            Vec::new()
        };
        Ok(Stmt::If {
            cond,
            then_branch,
            else_branch,
        })
    }

    // ---- expressions (precedence climbing) ----

    fn parse_expr(&mut self) -> Result<Expr, ParseError> {
        self.enter()?;
        let result = self.parse_or();
        self.leave();
        result
    }

    fn parse_or(&mut self) -> Result<Expr, ParseError> {
        let base = self.depth;
        let result = self.parse_or_inner();
        self.depth = base; // restore even on error paths
        result
    }

    fn parse_or_inner(&mut self) -> Result<Expr, ParseError> {
        let mut lhs = self.parse_and()?;
        while *self.peek() == Tok::Or {
            // `a || b || c…` nests left-ward one level per iteration: the
            // increment must PERSIST (no matching leave), or the compiler's
            // recursion over the AST overflows. The wrapper restores depth.
            self.enter()?;
            self.advance();
            let rhs = self.parse_and()?;
            lhs = Expr::Binary {
                op: BinOp::Or,
                lhs: Box::new(lhs),
                rhs: Box::new(rhs),
            };
        }
        Ok(lhs)
    }

    fn parse_and(&mut self) -> Result<Expr, ParseError> {
        let base = self.depth;
        let result = self.parse_and_inner();
        self.depth = base;
        result
    }

    fn parse_and_inner(&mut self) -> Result<Expr, ParseError> {
        let mut lhs = self.parse_equality()?;
        while *self.peek() == Tok::And {
            self.enter()?;
            self.advance();
            let rhs = self.parse_equality()?;
            lhs = Expr::Binary {
                op: BinOp::And,
                lhs: Box::new(lhs),
                rhs: Box::new(rhs),
            };
        }
        Ok(lhs)
    }

    fn parse_equality(&mut self) -> Result<Expr, ParseError> {
        let base = self.depth;
        let result = self.parse_equality_inner();
        self.depth = base;
        result
    }

    fn parse_equality_inner(&mut self) -> Result<Expr, ParseError> {
        let mut lhs = self.parse_comparison()?;
        loop {
            let op = match self.peek() {
                Tok::Eq => BinOp::Eq,
                Tok::NotEq => BinOp::NotEq,
                _ => break,
            };
            self.enter()?;
            self.advance();
            let rhs = self.parse_comparison()?;
            lhs = Expr::Binary {
                op,
                lhs: Box::new(lhs),
                rhs: Box::new(rhs),
            };
        }
        Ok(lhs)
    }

    fn parse_comparison(&mut self) -> Result<Expr, ParseError> {
        let base = self.depth;
        let result = self.parse_comparison_inner();
        self.depth = base;
        result
    }

    fn parse_comparison_inner(&mut self) -> Result<Expr, ParseError> {
        let mut lhs = self.parse_additive()?;
        loop {
            let op = match self.peek() {
                Tok::Less => BinOp::Less,
                Tok::LessEq => BinOp::LessEq,
                Tok::Greater => BinOp::Greater,
                Tok::GreaterEq => BinOp::GreaterEq,
                _ => break,
            };
            self.enter()?;
            self.advance();
            let rhs = self.parse_additive()?;
            lhs = Expr::Binary {
                op,
                lhs: Box::new(lhs),
                rhs: Box::new(rhs),
            };
        }
        Ok(lhs)
    }

    fn parse_additive(&mut self) -> Result<Expr, ParseError> {
        let base = self.depth;
        let result = self.parse_additive_inner();
        self.depth = base;
        result
    }

    fn parse_additive_inner(&mut self) -> Result<Expr, ParseError> {
        let mut lhs = self.parse_multiplicative()?;
        loop {
            let op = match self.peek() {
                Tok::Plus => BinOp::Add,
                Tok::Minus => BinOp::Sub,
                _ => break,
            };
            self.enter()?;
            self.advance();
            let rhs = self.parse_multiplicative()?;
            lhs = Expr::Binary {
                op,
                lhs: Box::new(lhs),
                rhs: Box::new(rhs),
            };
        }
        Ok(lhs)
    }

    /// `*`, `/`, `%` bind tighter than `+`/`-` and looser than unary ops.
    fn parse_multiplicative(&mut self) -> Result<Expr, ParseError> {
        let base = self.depth;
        let result = self.parse_multiplicative_inner();
        self.depth = base;
        result
    }

    fn parse_multiplicative_inner(&mut self) -> Result<Expr, ParseError> {
        let mut lhs = self.parse_unary()?;
        loop {
            let op = match self.peek() {
                Tok::Star => BinOp::Mul,
                Tok::Slash => BinOp::Div,
                Tok::Percent => BinOp::Rem,
                _ => break,
            };
            self.enter()?;
            self.advance();
            let rhs = self.parse_unary()?;
            lhs = Expr::Binary {
                op,
                lhs: Box::new(lhs),
                rhs: Box::new(rhs),
            };
        }
        Ok(lhs)
    }

    fn parse_unary(&mut self) -> Result<Expr, ParseError> {
        match self.peek() {
            Tok::Not => {
                self.enter()?;
                self.advance();
                let inner = self.parse_unary();
                self.leave();
                return inner.map(|inner| Expr::UnaryNot(Box::new(inner)));
            }
            Tok::Minus => {
                self.enter()?;
                self.advance();
                let inner = self.parse_unary();
                self.leave();
                return inner.map(|inner| Expr::UnaryNeg(Box::new(inner)));
            }
            _ => {}
        }
        self.parse_postfix()
    }

    fn parse_postfix(&mut self) -> Result<Expr, ParseError> {
        let mut expr = self.parse_primary()?;
        loop {
            match self.peek() {
                Tok::Dot => {
                    self.advance();
                    let field = self.expect_ident()?;
                    expr = Expr::Member(Box::new(expr), field);
                }
                Tok::LBracket => {
                    self.advance();
                    let index = self.parse_expr()?;
                    self.expect(&Tok::RBracket)?;
                    expr = Expr::Index {
                        object: Box::new(expr),
                        index: Box::new(index),
                    };
                }
                Tok::LParen => {
                    self.advance();
                    let mut args = Vec::new();
                    if *self.peek() != Tok::RParen {
                        loop {
                            args.push(self.parse_expr()?);
                            if *self.peek() == Tok::Comma {
                                self.advance();
                            } else {
                                break;
                            }
                        }
                    }
                    self.expect(&Tok::RParen)?;
                    expr = Expr::Call {
                        callee: Box::new(expr),
                        args,
                    };
                }
                _ => break,
            }
        }
        Ok(expr)
    }

    fn parse_primary(&mut self) -> Result<Expr, ParseError> {
        match self.peek().clone() {
            Tok::Int(v) => {
                self.advance();
                Ok(Expr::Lit(Lit::Int(v)))
            }
            Tok::Float(v) => {
                self.advance();
                Ok(Expr::Lit(Lit::Float(v)))
            }
            Tok::Str(v) => {
                self.advance();
                Ok(Expr::Lit(Lit::Str(v)))
            }
            Tok::True => {
                self.advance();
                Ok(Expr::Lit(Lit::Bool(true)))
            }
            Tok::False => {
                self.advance();
                Ok(Expr::Lit(Lit::Bool(false)))
            }
            Tok::Null => {
                self.advance();
                Ok(Expr::Lit(Lit::Null))
            }
            Tok::LBracket => {
                self.enter()?;
                self.advance();
                let mut items = Vec::new();
                if *self.peek() != Tok::RBracket {
                    loop {
                        items.push(self.parse_expr()?);
                        if *self.peek() == Tok::Comma {
                            self.advance();
                        } else {
                            break;
                        }
                    }
                }
                self.expect(&Tok::RBracket)?;
                self.leave();
                Ok(Expr::List(items))
            }
            Tok::LBrace => {
                self.enter()?;
                self.advance();
                let mut pairs = Vec::new();
                if *self.peek() != Tok::RBrace {
                    loop {
                        let key = self.parse_expr()?;
                        self.expect(&Tok::Colon)?;
                        let value = self.parse_expr()?;
                        pairs.push((key, value));
                        if *self.peek() == Tok::Comma {
                            self.advance();
                        } else {
                            break;
                        }
                    }
                }
                self.expect(&Tok::RBrace)?;
                self.leave();
                Ok(Expr::Map(pairs))
            }
            Tok::Ident(name) => {
                self.advance();
                Ok(Expr::Ident(name))
            }
            Tok::LParen => {
                self.advance();
                let inner = self.parse_expr()?;
                self.expect(&Tok::RParen)?;
                Ok(inner)
            }
            other => self.error(format!("unexpected token {other:?} in expression")),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::ast::{Expr, Lit, Stmt};

    #[test]
    fn parses_control_flow_and_member_chains() {
        let source = r#"
            task demo() -> void {
                let rows = native.db.query("SELECT 1");
                if !native.net.isConnected() {
                    return;
                } else {
                    for row in rows {
                        native.http.post("https://example", row.id);
                    }
                }
            }
        "#;
        let task = parse_task(source).expect("parses");
        assert_eq!(task.name, "demo");
        assert_eq!(task.body.len(), 2);
        match &task.body[0] {
            Stmt::Let { name, .. } => assert_eq!(name, "rows"),
            other => panic!("expected let, got {other:?}"),
        }
        match &task.body[1] {
            Stmt::If {
                cond, else_branch, ..
            } => {
                assert!(matches!(cond, Expr::UnaryNot(_)));
                assert_eq!(else_branch.len(), 1, "else branch holds the for loop");
            }
            other => panic!("expected if, got {other:?}"),
        }
    }

    #[test]
    fn precedence_binds_comparison_looser_than_addition() {
        let task = parse_task("task t() -> void { return 1 + 2 < 4; }").expect("parses");
        match &task.body[0] {
            Stmt::Return(Some(Expr::Binary {
                op: BinOp::Less,
                lhs,
                ..
            })) => {
                assert!(matches!(lhs.as_ref(), Expr::Binary { op: BinOp::Add, .. }));
            }
            other => panic!("expected (1+2) < 4 shape, got {other:?}"),
        }
    }

    #[test]
    fn missing_semicolon_is_an_error_with_position() {
        let err = parse_task("task t() -> void { let x = 1 }").expect_err("must fail");
        assert!(err.message.contains("expected Semicolon"));
        assert_eq!(err.line, 1);
    }

    #[test]
    fn non_native_call_is_a_parse_success_but_shape_is_preserved() {
        // The parser accepts the call shape; the COMPILER rejects it later.
        let task = parse_task("task t() -> void { helper(1, \"x\"); }").expect("parses");
        match &task.body[0] {
            Stmt::Expr(Expr::Call { args, .. }) => assert_eq!(args.len(), 2),
            other => panic!("expected call statement, got {other:?}"),
        }
        assert_eq!(Expr::Lit(Lit::Int(1)), Expr::Lit(Lit::Int(1)));
    }
}

#[cfg(test)]
mod adversarial_tests {
    use super::*;

    #[test]
    fn deeply_nested_parens_are_rejected_not_a_stack_overflow() {
        let mut source = String::from("task t() -> void { return ");
        source.push_str(&"(".repeat(10_000));
        source.push('1');
        source.push_str(&")".repeat(10_000));
        source.push_str("; }");
        let err = parse_task(&source).expect_err("must reject runaway nesting");
        assert!(err.message.contains("nesting deeper"), "got: {err}");
    }

    #[test]
    fn deeply_nested_blocks_are_rejected() {
        let mut source = String::from("task t() -> void { ");
        for _ in 0..10_000 {
            source.push_str("if true { ");
        }
        source.push_str(&"} ".repeat(10_000));
        source.push('}');
        let err = parse_task(&source).expect_err("must reject runaway blocks");
        assert!(err.message.contains("nesting deeper"), "got: {err}");
    }

    #[test]
    fn long_unary_not_chains_are_rejected() {
        let source = format!("task t() -> void {{ return {}true; }}", "!".repeat(10_000));
        let err = parse_task(&source).expect_err("must reject runaway not-chains");
        assert!(err.message.contains("nesting deeper"), "got: {err}");
    }

    #[test]
    fn else_if_chains_are_bounded() {
        let mut source = String::from("task t() -> void { ");
        for _ in 0..10_000 {
            source.push_str("if false { } else ");
        }
        source.push_str("{ } }");
        let err = parse_task(&source).expect_err("must reject runaway else-if chains");
        assert!(err.message.contains("nesting deeper"), "got: {err}");
    }

    #[test]
    fn absurd_token_counts_are_capped() {
        // One enormous identifier list: bounded by MAX_TOKENS, not memory.
        let mut source = String::from("task t() -> void { return 1");
        source.push_str(&" + 1".repeat(MAX_TOKENS)); // way past the cap
        source.push_str("; }");
        let err = parse_task(&source).expect_err("must reject token floods");
        assert!(err.message.contains("token limit"), "got: {err}");
    }

    #[test]
    fn nesting_just_under_the_limit_still_parses() {
        // 100 levels of parens sit comfortably inside MAX_NESTING_DEPTH.
        let mut source = String::from("task t() -> void { return ");
        source.push_str(&"(".repeat(100));
        source.push('1');
        source.push_str(&")".repeat(100));
        source.push_str("; }");
        assert!(parse_task(&source).is_ok(), "legitimate nesting must work");
    }
}
