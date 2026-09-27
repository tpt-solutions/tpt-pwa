// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! CLI:
//!   cortex-engine run <script.ctx>          -- execute against the scripted in-memory environment
//!   cortex-engine exec-host --script <file> -- execute with native calls served by the parent
//!                                             process over the stdio host protocol (src/host.rs)

use std::io::{BufReader, Write};
use std::process::ExitCode;

use cortex_engine::host::HostNative;
use cortex_engine::natives::MemoryNative;

fn main() -> ExitCode {
    let mut args = std::env::args().skip(1);
    let command = match args.next() {
        Some(cmd) => cmd,
        None => {
            eprintln!("usage: cortex-engine run <script.ctx> | exec-host --script <script.ctx>");
            return ExitCode::from(2);
        }
    };
    match command.as_str() {
        "run" => run_command(args.next().as_deref()),
        "exec-host" => exec_host_command(args),
        other => {
            eprintln!("unknown command `{other}` (expected `run` or `exec-host`)");
            ExitCode::from(2)
        }
    }
}

fn run_command(path: Option<&str>) -> ExitCode {
    let Some(path) = path else {
        eprintln!("usage: cortex-engine run <script.ctx>");
        return ExitCode::from(2);
    };
    let Ok(source) = std::fs::read_to_string(path) else {
        eprintln!("cannot read {path}");
        return ExitCode::from(2);
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

fn exec_host_command(mut args: std::iter::Skip<std::env::Args>) -> ExitCode {
    let mut script_path: Option<String> = None;
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--script" => script_path = args.next(),
            other => {
                eprintln!("exec-host: unexpected argument `{other}`");
                return ExitCode::from(2);
            }
        }
    }
    let Some(path) = script_path else {
        eprintln!("usage: cortex-engine exec-host --script <script.ctx>");
        return ExitCode::from(2);
    };
    let Ok(source) = std::fs::read_to_string(&path) else {
        eprintln!("exec-host: cannot read {path}");
        return ExitCode::from(2);
    };

    let stdin = std::io::stdin();
    let stdout = std::io::stdout();
    let mut env = HostNative::new(BufReader::new(stdin.lock()), stdout.lock());
    // stdout is protocol-only: make sure diagnostics never land there.
    let _ = std::io::stdout().flush();
    match cortex_engine::run_source(&source, &mut env) {
        Ok(result) => {
            eprintln!("exec-host: task finished: {result}");
            ExitCode::SUCCESS
        }
        Err(err) => {
            eprintln!("exec-host: {err}");
            ExitCode::FAILURE
        }
    }
}
