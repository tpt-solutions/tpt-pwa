// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! CLI: `cortex-engine run <script.ctx>` -- parse, compile and execute a
//! task against a scripted in-memory environment. Useful for local checks
//! and as the seam where the daemon will plug in its real host environment.

use std::process::ExitCode;

use cortex_engine::natives::MemoryNative;

fn main() -> ExitCode {
    let mut args = std::env::args().skip(1);
    let command = match args.next() {
        Some(cmd) => cmd,
        None => {
            eprintln!("usage: cortex-engine run <script.ctx>");
            return ExitCode::from(2);
        }
    };
    if command != "run" {
        eprintln!("unknown command `{command}` (expected `run`)");
        return ExitCode::from(2);
    }
    let path = match args.next() {
        Some(path) => path,
        None => {
            eprintln!("usage: cortex-engine run <script.ctx>");
            return ExitCode::from(2);
        }
    };
    let source = match std::fs::read_to_string(&path) {
        Ok(source) => source,
        Err(err) => {
            eprintln!("cannot read {path}: {err}");
            return ExitCode::from(2);
        }
    };

    let mut env = MemoryNative::default();
    match cortex_engine::run_source(&source, &mut env) {
        Ok(result) => {
            println!("ok: {result}");
            println!("posts: {}", env.posts.len());
            println!("db.exec statements: {}", env.executed_sql.len());
            ExitCode::SUCCESS
        }
        Err(err) => {
            eprintln!("{err}");
            ExitCode::FAILURE
        }
    }
}
