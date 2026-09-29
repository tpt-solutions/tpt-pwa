// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! CLI:
//!   cortex-engine run <script.ctx>          -- execute against the scripted in-memory environment
//!   cortex-engine exec-host --script <file> -- execute with native calls served by the parent
//!                                             process over the stdio host protocol (src/host.rs)
//!
//! Exit codes carry failure classes so callers (the cortex-daemon's
//! scheduler) can stop retrying permanent failures:
//!   0  success
//!   1  the script FAILED at runtime (VM error) -- retrying may help
//!   2  the script could not be parsed/compiled, or the CLI was misused --
//!      permanent: retrying the same bytes can never succeed

use std::io::{BufReader, Write};
use std::process::ExitCode;

use cortex_engine::host::HostNative;
use cortex_engine::natives::MemoryNative;

/// Exit code for parse/compile failures and CLI misuse: permanent.
const EXIT_PERMANENT: u8 = 2;

fn main() -> ExitCode {
    let mut args = std::env::args().skip(1);
    let command = match args.next() {
        Some(cmd) => cmd,
        None => {
            eprintln!("usage: cortex-engine run <script.ctx> | exec-host --script <script.ctx>");
            return ExitCode::from(EXIT_PERMANENT);
        }
    };
    match command.as_str() {
        "run" => run_command(args.next().as_deref()),
        "exec-host" => exec_host_command(args),
        other => {
            eprintln!("unknown command `{other}` (expected `run` or `exec-host`)");
            ExitCode::from(EXIT_PERMANENT)
        }
    }
}

/// Map an engine error to its failure class: parse/compile errors are
/// permanent (the bytes will never run), VM errors are transient (the data
/// may change).
fn exit_code_for(err: &cortex_engine::EngineError) -> u8 {
    match err {
        cortex_engine::EngineError::Parse(_) | cortex_engine::EngineError::Compile(_) => {
            EXIT_PERMANENT
        }
        cortex_engine::EngineError::Vm(_) => 1,
    }
}

fn run_command(path: Option<&str>) -> ExitCode {
    let Some(path) = path else {
        eprintln!("usage: cortex-engine run <script.ctx>");
        return ExitCode::from(EXIT_PERMANENT);
    };
    let Ok(source) = std::fs::read_to_string(path) else {
        eprintln!("cannot read {path}");
        return ExitCode::from(EXIT_PERMANENT);
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
            ExitCode::from(exit_code_for(&err))
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
                return ExitCode::from(EXIT_PERMANENT);
            }
        }
    }
    let Some(path) = script_path else {
        eprintln!("usage: cortex-engine exec-host --script <script.ctx>");
        return ExitCode::from(EXIT_PERMANENT);
    };
    let Ok(source) = std::fs::read_to_string(&path) else {
        eprintln!("exec-host: cannot read {path}");
        return ExitCode::from(EXIT_PERMANENT);
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
            ExitCode::from(exit_code_for(&err))
        }
    }
}
