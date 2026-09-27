// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! Recursive-descent parser for a single `task` definition.

use crate::ast::{BinOp, Expr, Lit, Stmt, Task};
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

struct Parser {
    tokens: Vec<Token>,
    pos: usize,
}

/// Parse a complete task script: exactly one `task` definition.
pub fn parse_task(source: &str) -> Result<Task, ParseError> {
    let tokens = lex(source).map_err(|e| ParseError {
        message: e.message,
        line: e.line,
        col: e.col,
    })?;
    let mut parser = Parser { tokens, pos: 0 };
    parser.parse_task_def()
}

impl Parser {
    fn peek(&self) -> &Tok {
        &self.tokens[self.pos].tok
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
        let body = self.parse_block()?;
        self.expect(&Tok::Eof)?;
        Ok(Task { name, body })
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
        self.parse_or()
    }

    fn parse_or(&mut self) -> Result<Expr, ParseError> {
        let mut lhs = self.parse_and()?;
        while *self.peek() == Tok::Or {
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
        let mut lhs = self.parse_equality()?;
        while *self.peek() == Tok::And {
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
        let mut lhs = self.parse_comparison()?;
        loop {
            let op = match self.peek() {
                Tok::Eq => BinOp::Eq,
                Tok::NotEq => BinOp::NotEq,
                _ => break,
            };
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
        let mut lhs = self.parse_additive()?;
        loop {
            let op = match self.peek() {
                Tok::Less => BinOp::Less,
                Tok::LessEq => BinOp::LessEq,
                Tok::Greater => BinOp::Greater,
                Tok::GreaterEq => BinOp::GreaterEq,
                _ => break,
            };
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
        let mut lhs = self.parse_unary()?;
        loop {
            let op = match self.peek() {
                Tok::Plus => BinOp::Add,
                Tok::Minus => BinOp::Sub,
                _ => break,
            };
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
        if *self.peek() == Tok::Not {
            self.advance();
            let inner = self.parse_unary()?;
            return Ok(Expr::UnaryNot(Box::new(inner)));
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
