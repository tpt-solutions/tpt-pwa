// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! Tokenizer for the cortex DSL. Deliberately tiny: keywords, literals,
//! member dots and the operator set the task language needs.

use std::fmt;

#[derive(Clone, Debug, PartialEq)]
pub enum Tok {
    Ident(String),
    Str(String),
    Int(i64),
    Float(f64),
    // keywords
    Task,
    Let,
    If,
    Else,
    For,
    In,
    Return,
    Void,
    // punctuation / operators
    LParen,
    RParen,
    LBrace,
    RBrace,
    Comma,
    Semicolon,
    Dot,
    Question, // '?' inside SQL strings is a plain char; this is for future use
    Arrow,
    Assign,
    Eq,
    NotEq,
    Less,
    LessEq,
    Greater,
    GreaterEq,
    Not,
    Plus,
    Minus,
    And,
    Or,
    Eof,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Token {
    pub tok: Tok,
    pub line: u32,
    pub col: u32,
}

#[derive(Clone, Debug, PartialEq)]
pub struct LexError {
    pub message: String,
    pub line: u32,
    pub col: u32,
}

impl fmt::Display for LexError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{} at {}:{})", self.message, self.line, self.col)
    }
}

pub fn lex(source: &str) -> Result<Vec<Token>, LexError> {
    let mut tokens = Vec::new();
    let chars: Vec<char> = source.chars().collect();
    let mut i = 0usize;
    let mut line = 1u32;
    let mut col = 1u32;

    macro_rules! bump {
        () => {{
            if chars[i] == '\n' {
                line += 1;
                col = 1;
            } else {
                col += 1;
            }
            i += 1;
        }};
    }

    while i < chars.len() {
        let c = chars[i];
        let (tok_line, tok_col) = (line, col);
        // whitespace
        if c.is_whitespace() {
            bump!();
            continue;
        }
        // comments: // ...
        if c == '/' && chars.get(i + 1) == Some(&'/') {
            while i < chars.len() && chars[i] != '\n' {
                bump!();
            }
            continue;
        }
        let ident: Option<String> = {
            if c.is_ascii_alphabetic() || c == '_' {
                let mut s = String::new();
                while i < chars.len() && (chars[i].is_ascii_alphanumeric() || chars[i] == '_') {
                    s.push(chars[i]);
                    bump!();
                }
                Some(s)
            } else {
                None
            }
        };
        if let Some(word) = ident {
            let tok = match word.as_str() {
                "task" => Tok::Task,
                "let" => Tok::Let,
                "if" => Tok::If,
                "else" => Tok::Else,
                "for" => Tok::For,
                "in" => Tok::In,
                "return" => Tok::Return,
                "void" => Tok::Void,
                _ => Tok::Ident(word),
            };
            tokens.push(Token {
                tok,
                line: tok_line,
                col: tok_col,
            });
            continue;
        }
        // numbers (int or float)
        if c.is_ascii_digit() {
            let mut s = String::new();
            let mut is_float = false;
            while i < chars.len() && (chars[i].is_ascii_digit() || chars[i] == '.') {
                if chars[i] == '.' {
                    if is_float {
                        break;
                    }
                    is_float = true;
                }
                s.push(chars[i]);
                bump!();
            }
            let tok = if is_float {
                Tok::Float(s.parse::<f64>().map_err(|_| LexError {
                    message: format!("bad float literal `{s}`"),
                    line: tok_line,
                    col: tok_col,
                })?)
            } else {
                Tok::Int(s.parse::<i64>().map_err(|_| LexError {
                    message: format!("bad integer literal `{s}`"),
                    line: tok_line,
                    col: tok_col,
                })?)
            };
            tokens.push(Token {
                tok,
                line: tok_line,
                col: tok_col,
            });
            continue;
        }
        // strings
        if c == '"' {
            bump!();
            let mut s = String::new();
            loop {
                match chars.get(i) {
                    None => {
                        return Err(LexError {
                            message: "unterminated string".into(),
                            line: tok_line,
                            col: tok_col,
                        });
                    }
                    Some('"') => {
                        bump!();
                        break;
                    }
                    Some('\\') => {
                        bump!();
                        match chars.get(i) {
                            Some(&esc @ ('"' | '\\')) => {
                                s.push(esc);
                                bump!();
                            }
                            Some('n') => {
                                s.push('\n');
                                bump!();
                            }
                            Some('t') => {
                                s.push('\t');
                                bump!();
                            }
                            _ => {
                                return Err(LexError {
                                    message: "bad escape".into(),
                                    line: tok_line,
                                    col: tok_col,
                                });
                            }
                        }
                    }
                    Some(&ch) => {
                        s.push(ch);
                        bump!();
                    }
                }
            }
            tokens.push(Token {
                tok: Tok::Str(s),
                line: tok_line,
                col: tok_col,
            });
            continue;
        }
        // punctuation / operators
        let simple = |t: Tok| Token {
            tok: t,
            line: tok_line,
            col: tok_col,
        };
        match c {
            '(' => {
                bump!();
                tokens.push(simple(Tok::LParen));
            }
            ')' => {
                bump!();
                tokens.push(simple(Tok::RParen));
            }
            '{' => {
                bump!();
                tokens.push(simple(Tok::LBrace));
            }
            '}' => {
                bump!();
                tokens.push(simple(Tok::RBrace));
            }
            ',' => {
                bump!();
                tokens.push(simple(Tok::Comma));
            }
            ';' => {
                bump!();
                tokens.push(simple(Tok::Semicolon));
            }
            '.' => {
                bump!();
                tokens.push(simple(Tok::Dot));
            }
            '?' => {
                bump!();
                tokens.push(simple(Tok::Question));
            }
            '-' => {
                bump!();
                if chars.get(i) == Some(&'>') {
                    bump!();
                    tokens.push(simple(Tok::Arrow));
                } else {
                    tokens.push(simple(Tok::Minus));
                }
            }
            '=' => {
                bump!();
                if chars.get(i) == Some(&'=') {
                    bump!();
                    tokens.push(simple(Tok::Eq));
                } else {
                    tokens.push(simple(Tok::Assign));
                }
            }
            '!' => {
                bump!();
                if chars.get(i) == Some(&'=') {
                    bump!();
                    tokens.push(simple(Tok::NotEq));
                } else {
                    tokens.push(simple(Tok::Not));
                }
            }
            '<' => {
                bump!();
                if chars.get(i) == Some(&'=') {
                    bump!();
                    tokens.push(simple(Tok::LessEq));
                } else {
                    tokens.push(simple(Tok::Less));
                }
            }
            '>' => {
                bump!();
                if chars.get(i) == Some(&'=') {
                    bump!();
                    tokens.push(simple(Tok::GreaterEq));
                } else {
                    tokens.push(simple(Tok::Greater));
                }
            }
            '+' => {
                bump!();
                tokens.push(simple(Tok::Plus));
            }
            '&' => {
                bump!();
                if chars.get(i) == Some(&'&') {
                    bump!();
                    tokens.push(simple(Tok::And));
                } else {
                    return Err(LexError {
                        message: "expected `&&`".into(),
                        line: tok_line,
                        col: tok_col,
                    });
                }
            }
            '|' => {
                bump!();
                if chars.get(i) == Some(&'|') {
                    bump!();
                    tokens.push(simple(Tok::Or));
                } else {
                    return Err(LexError {
                        message: "expected `||`".into(),
                        line: tok_line,
                        col: tok_col,
                    });
                }
            }
            other => {
                return Err(LexError {
                    message: format!("unexpected character `{other}`"),
                    line: tok_line,
                    col: tok_col,
                });
            }
        }
    }
    tokens.push(Token {
        tok: Tok::Eof,
        line,
        col,
    });
    Ok(tokens)
}
