// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! CLI:
//!   cortex-engine run <script.ctx>          -- execute against the scripted in-memory environment
//!   cortex-engine exec-host --script <file> -- execute with native calls served by the parent
//!                                             process over the stdio host protocol (src/host.rs)
//!   cortex-engine replay <script.ctx> --trace <file>
//!                                           -- execute against a recorded host-call trace
//!                                              (src/trace.rs) instead of a live host
//!   [--record <trace.jsonl>]               -- with `run`/`exec-host`: append every native
//!                                             call (and the final result) to a JSONL
//!                                             trace for fixtures and bug reports
//!
//! Exit codes carry failure classes so callers (the cortex-daemon's
//! scheduler) can stop retrying permanent failures:
//!   0  success
//!   1  the script FAILED at runtime (VM error) -- retrying may help
//!   2  the script could not be parsed/compiled, or the CLI was misused --
//!      permanent: retrying the same bytes can never succeed

use std::io::{BufReader, BufWriter, Write};
use std::process::ExitCode;

use cortex_engine::host::HostNative;
use cortex_engine::natives::{MemoryNative, NativeEnv};
use cortex_engine::trace::{record_result, verify_result, RecordingNative, ReplayNative};

/// Exit code for parse/compile failures and CLI misuse: permanent.
const EXIT_PERMANENT: u8 = 2;

fn main() -> ExitCode {
    let mut args = std::env::args().skip(1);
    let command = match args.next() {
        Some(cmd) => cmd,
        None => {
            eprintln!("usage: cortex-engine run <script.ctx> | exec-host --script <script.ctx> | replay <script.ctx> --trace <file>");
            return ExitCode::from(EXIT_PERMANENT);
        }
    };
    match command.as_str() {
        "run" => run_command(args.next().as_deref(), args),
        "exec-host" => exec_host_command(args),
        "replay" => replay_command(args.next().as_deref(), args),
        "manifest" => manifest_command(args),
        other => {
            eprintln!(
                "unknown command `{other}` (expected `run`, `exec-host`, `replay`, or `manifest`)"
            );
            ExitCode::from(EXIT_PERMANENT)
        }
    }
}

/// `manifest`: print the task's native-call surface as a sorted JSON array
/// (the capability declaration hosts can enforce, e.g. the daemon's
/// `-allow-natives`). Exit 2 for anything that would not compile.
fn manifest_command(mut args: std::iter::Skip<std::env::Args>) -> ExitCode {
    let mut path: Option<String> = None;
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--script" => path = args.next(),
            // Positional form too: `manifest sync.ctx` == `manifest --script sync.ctx`.
            other if path.is_none() && !other.starts_with("--") => path = Some(other.to_string()),
            other => {
                eprintln!("manifest: unexpected argument `{other}`");
                return ExitCode::from(EXIT_PERMANENT);
            }
        }
    }
    let Some(path) = path else {
        eprintln!("usage: cortex-engine manifest <script.ctx>");
        return ExitCode::from(EXIT_PERMANENT);
    };
    let Ok(source) = std::fs::read_to_string(&path) else {
        eprintln!("manifest: cannot read {path}");
        return ExitCode::from(EXIT_PERMANENT);
    };
    match cortex_engine::parser::parse_task(&source)
        .map_err(cortex_engine::EngineError::Parse)
        .and_then(|task| {
            cortex_engine::compiler::manifest(&task).map_err(cortex_engine::EngineError::Compile)
        }) {
        Ok(natives) => {
            println!("{}", serde_json::json!(natives));
            ExitCode::SUCCESS
        }
        Err(err) => {
            eprintln!("manifest: {err}");
            ExitCode::from(exit_code_for(&err))
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

fn run_command(path: Option<&str>, mut args: std::iter::Skip<std::env::Args>) -> ExitCode {
    let Some(path) = path else {
        eprintln!("usage: cortex-engine run <script.ctx> [--record <trace.jsonl>]");
        return ExitCode::from(EXIT_PERMANENT);
    };
    let mut record_path: Option<String> = None;
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--record" => record_path = args.next(),
            other => {
                eprintln!("run: unexpected argument `{other}`");
                return ExitCode::from(EXIT_PERMANENT);
            }
        }
    }
    let Ok(source) = std::fs::read_to_string(path) else {
        eprintln!("cannot read {path}");
        return ExitCode::from(EXIT_PERMANENT);
    };

    if let Some(trace_path) = &record_path {
        let Ok(file) = std::fs::File::create(trace_path) else {
            eprintln!("cannot write trace {trace_path}");
            return ExitCode::from(EXIT_PERMANENT);
        };
        let mut env = RecordingNative::new(MemoryNative::default(), BufWriter::new(file));
        let outcome = cortex_engine::run_source(&source, &mut env);
        report_run_outcome(&outcome, env.inner());
        return finish_with_trace(&mut env, trace_path, &outcome);
    }

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
    let mut record_path: Option<String> = None;
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--script" => script_path = args.next(),
            "--record" => record_path = args.next(),
            other => {
                eprintln!("exec-host: unexpected argument `{other}`");
                return ExitCode::from(EXIT_PERMANENT);
            }
        }
    }
    let Some(path) = script_path else {
        eprintln!("usage: cortex-engine exec-host --script <script.ctx> [--record <trace.jsonl>]");
        return ExitCode::from(EXIT_PERMANENT);
    };
    let Ok(source) = std::fs::read_to_string(&path) else {
        eprintln!("exec-host: cannot read {path}");
        return ExitCode::from(EXIT_PERMANENT);
    };

    let stdin = std::io::stdin();
    let stdout = std::io::stdout();
    let host = HostNative::new(BufReader::new(stdin.lock()), stdout.lock());
    // stdout is protocol-only: make sure diagnostics never land there.
    let _ = std::io::stdout().flush();
    let outcome = match &record_path {
        Some(trace_path) => {
            let Ok(file) = std::fs::File::create(trace_path) else {
                eprintln!("exec-host: cannot write trace {trace_path}");
                return ExitCode::from(EXIT_PERMANENT);
            };
            let mut env = RecordingNative::new(host, BufWriter::new(file));
            let outcome = cortex_engine::run_source(&source, &mut env);
            finish_with_trace(&mut env, trace_path, &outcome)
        }
        None => {
            let mut env = host;
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
    };
    outcome
}

/// `replay`: execute against a recorded trace instead of a live host, then
/// verify the call sequence (and, when recorded, the final result) matches.
fn replay_command(path: Option<&str>, mut args: std::iter::Skip<std::env::Args>) -> ExitCode {
    let mut trace_path: Option<String> = None;
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--trace" => trace_path = args.next(),
            other => {
                eprintln!("replay: unexpected argument `{other}`");
                return ExitCode::from(EXIT_PERMANENT);
            }
        }
    }
    let (Some(path), Some(trace_path)) = (path, trace_path) else {
        eprintln!("usage: cortex-engine replay <script.ctx> --trace <trace.jsonl>");
        return ExitCode::from(EXIT_PERMANENT);
    };
    let Ok(source) = std::fs::read_to_string(path) else {
        eprintln!("replay: cannot read {path}");
        return ExitCode::from(EXIT_PERMANENT);
    };
    let Ok(file) = std::fs::File::open(&trace_path) else {
        eprintln!("replay: cannot read trace {trace_path}");
        return ExitCode::from(EXIT_PERMANENT);
    };
    let replay = ReplayNative::from_reader(BufReader::new(file));
    let Ok(mut env) = replay else {
        eprintln!("replay: malformed trace {trace_path}");
        return ExitCode::from(EXIT_PERMANENT);
    };
    // The marker is bookkeeping, not a native call: take it out before the
    // run so the unconsumed check sees only real calls.
    let recorded_marker = env.consume_result_marker();

    let outcome: Result<cortex_engine::value::Value, String> =
        match cortex_engine::run_source(&source, &mut env) {
            Ok(result) => Ok(result),
            // A native failure during replay IS the trace's recorded error
            // surfacing; keep its message so verify_result can match it.
            Err(cortex_engine::EngineError::Vm(cortex_engine::VmError::Native(message))) => {
                Err(message)
            }
            Err(other) => {
                eprintln!("replay: {other}");
                return ExitCode::from(exit_code_for(&other));
            }
        };

    if env.unconsumed() > 0 {
        eprintln!(
            "replay: {} recorded call(s) were never made -- the script diverged from its trace",
            env.unconsumed()
        );
        return ExitCode::from(1);
    }
    if let Err(message) = verify_result(recorded_marker.as_ref(), &outcome) {
        eprintln!("replay: {message}");
        return ExitCode::from(1);
    }
    match outcome {
        Ok(result) => {
            println!("ok: {result}");
            ExitCode::SUCCESS
        }
        Err(error) => {
            eprintln!("replay: {error}");
            ExitCode::from(1)
        }
    }
}

fn report_run_outcome(
    outcome: &Result<cortex_engine::value::Value, cortex_engine::EngineError>,
    env: &MemoryNative,
) {
    match outcome {
        Ok(result) => {
            println!("ok: {result}");
            println!("posts: {}", env.posts.len());
            println!("db.exec statements: {}", env.executed_sql.len());
        }
        Err(err) => eprintln!("{err}"),
    }
}

/// Flush the trace (appending the `__result` marker) and map the run's
/// outcome to the same exit codes an untraced run would produce.
fn finish_with_trace<E: NativeEnv, W: Write>(
    env: &mut RecordingNative<E, W>,
    trace_path: &str,
    outcome: &Result<cortex_engine::value::Value, cortex_engine::EngineError>,
) -> ExitCode {
    // Record the INNER native message (not the EngineError wrapper prose):
    // a replay reproduces failures as the trace's own recorded text, so
    // both sides of verify_result must speak the same form.
    let mapped = match outcome {
        Ok(value) => Ok(value.clone()),
        Err(cortex_engine::EngineError::Vm(cortex_engine::VmError::Native(message))) => {
            Err(message.clone())
        }
        Err(other) => Err(other.to_string()),
    };
    record_result(env.writer_mut(), &mapped);
    if let Err(err) = env.flush() {
        eprintln!("cannot finalize trace {trace_path}: {err}");
        return ExitCode::from(EXIT_PERMANENT);
    }
    match outcome {
        Ok(_) => ExitCode::SUCCESS,
        Err(err) => ExitCode::from(exit_code_for(err)),
    }
}
