// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! Deterministic replay: a recorded host-call trace must reproduce the same
//! script behavior without the original host, and any divergence (different
//! calls, different params, missing/extra calls, different result) must be a
//! loud error. CLI-level tests record through a real `exec-host` session and
//! replay it with no host attached.

use std::io::{BufRead, BufReader, Write};
use std::process::{Child, Command, Stdio};

use cortex_engine::natives::{row, MemoryNative, NativeRegistry};
use cortex_engine::trace::{record_result, verify_result, RecordingNative, ReplayNative};
use cortex_engine::value::Value;
use cortex_engine::{EngineError, Vm, VmError};

const SCRIPT: &str = r#"
    task t() -> void {
        let rows = native.db.query("SELECT id FROM queue");
        if native.net.isConnected() {
            for row in rows {
                native.http.post("https://api.example/sync", row);
            }
        }
        let count = 0;
        for row in rows {
            count = count + 1;
        }
        return count;
    }
"#;

fn run_against(env: &mut dyn cortex_engine::NativeEnv, source: &str) -> Result<Value, EngineError> {
    let task = cortex_engine::parser::parse_task(source).expect("parses");
    let program = cortex_engine::compiler::compile(&task).expect("compiles");
    let registry = NativeRegistry::standard();
    Vm::new(program, env, &registry)
        .run()
        .map_err(EngineError::Vm)
}

fn recorded_script() -> Vec<u8> {
    let env = MemoryNative {
        connected: true,
        rows: vec![row(&[("id", Value::Str("41".into()))])],
        ..MemoryNative::default()
    };
    let mut recording = Vec::new();
    {
        let mut recorder = RecordingNative::new(env, &mut recording);
        let outcome = run_against(&mut recorder, SCRIPT);
        record_result(recorder.writer_mut(), &outcome.map_err(|e| e.to_string()));
    }
    recording
}

#[test]
fn a_recorded_trace_replays_the_identical_run() {
    let trace = recorded_script();

    let mut env = ReplayNative::from_reader(BufReader::new(&trace[..])).expect("parses");
    env.consume_result_marker(); // bookkeeping, not a call
    let result = run_against(&mut env, SCRIPT).expect("replays");
    assert_eq!(result, Value::Int(1));
    assert_eq!(env.unconsumed(), 0, "every recorded call must be consumed");
}

#[test]
fn diverging_params_are_named_by_the_replay() {
    let trace = recorded_script();
    let diverging = SCRIPT.replace("SELECT id FROM queue", "SELECT id FROM other");

    let mut env = ReplayNative::from_reader(BufReader::new(&trace[..])).expect("parses");
    let err = run_against(&mut env, &diverging).expect_err("params must match the trace");
    match err {
        EngineError::Vm(VmError::Native(message)) => {
            assert!(message.contains("replay mismatch"), "got: {message}");
            assert!(message.contains("params diverged"), "got: {message}");
        }
        other => panic!("expected a native replay error, got {other:?}"),
    }
}

#[test]
fn an_extra_call_exhausts_the_trace_and_a_missing_call_leaves_unconsumed() {
    let trace = recorded_script();

    // More calls than recorded: the trace runs dry mid-script.
    let mut env = ReplayNative::from_reader(BufReader::new(&trace[..])).expect("parses");
    // Reuse the recorded trace against a script with one extra post:
    // drop the trace's result marker first (this fixture has one).
    env.consume_result_marker();
    let longer = SCRIPT.replace(
        "return count;",
        "native.http.post(\"https://extra\", 1); return count;",
    );
    let err = run_against(&mut env, &longer).expect_err("trace must run dry");
    assert!(
        matches!(err, EngineError::Vm(VmError::Native(ref m)) if m.contains("trace exhausted"))
    );

    // Fewer calls than recorded: entries remain unconsumed.
    let mut env = ReplayNative::from_reader(BufReader::new(&trace[..])).expect("parses");
    env.consume_result_marker();
    let shorter = "task t() -> void { let ok = native.net.isConnected(); }";
    let _ = run_against(&mut env, shorter);
    assert!(env.unconsumed() > 0, "unmade calls must be reported");
}

#[test]
fn recorded_errors_replay_as_the_same_failure() {
    struct FailingNative;
    impl cortex_engine::natives::NativeEnv for FailingNative {
        fn db_query(&mut self, _: &str, _: &[Value]) -> Result<Vec<Value>, String> {
            Err("db unavailable".into())
        }
        fn db_exec(&mut self, _: &str, _: &[Value]) -> Result<Value, String> {
            Err("db unavailable".into())
        }
        fn net_is_connected(&mut self) -> Result<bool, String> {
            Ok(true)
        }
        fn http_post(&mut self, _: &str, _: &Value) -> Result<Value, String> {
            Err("tls handshake failed".into())
        }
    }
    let mut recording = Vec::new();
    {
        let mut recorder = RecordingNative::new(FailingNative, &mut recording);
        let outcome = run_against(
            &mut recorder,
            "task t() -> void { let r = native.db.query(\"s\"); }",
        );
        assert!(outcome.is_err(), "the recorded run itself failed");
        // Record exactly what the CLI records: the inner native message.
        let mapped = match &outcome {
            Ok(value) => Ok(value.clone()),
            Err(EngineError::Vm(VmError::Native(message))) => Err(message.clone()),
            Err(other) => Err(other.to_string()),
        };
        record_result(recorder.writer_mut(), &mapped);
    }

    let mut env = ReplayNative::from_reader(BufReader::new(&recording[..])).expect("parses");
    let marker = env.consume_result_marker();
    let err = run_against(
        &mut env,
        "task t() -> void { let r = native.db.query(\"s\"); }",
    )
    .expect_err("the replay fails the same way");
    assert!(
        matches!(&err, EngineError::Vm(VmError::Native(m)) if m.contains("db unavailable")),
        "got: {err:?}"
    );
    // And the recorded outcome matches what the replay produced.
    let replayed: Result<Value, String> = Err("db unavailable".into());
    assert!(verify_result(marker.as_ref(), &replayed).is_ok());
}

#[test]
fn verify_result_flags_value_and_class_divergence() {
    let entry = |result: Option<Value>, error: Option<&str>| cortex_engine::trace::TraceEntry {
        method: cortex_engine::trace::RESULT_MARKER.into(),
        params: Vec::new(),
        result,
        error: error.map(str::to_string),
    };
    // Same value: ok.
    assert!(verify_result(Some(&entry(Some(Value::Int(1)), None)), &Ok(Value::Int(1))).is_ok());
    // Different value: named.
    let mismatch =
        verify_result(Some(&entry(Some(Value::Int(1)), None)), &Ok(Value::Int(2))).unwrap_err();
    assert!(mismatch.contains("result mismatch"), "got: {mismatch}");
    // Ok vs error: named.
    let mismatch =
        verify_result(Some(&entry(Some(Value::Int(1)), None)), &Err("boom".into())).unwrap_err();
    assert!(mismatch.contains("replay failed"), "got: {mismatch}");
    // No marker: nothing to check.
    assert!(verify_result(None, &Err("anything".into())).is_ok());
}

// ---- CLI level: record a real exec-host session, replay it hostless ----

fn serve_and_collect(mut child: Child, trace_path: &str) -> (Vec<String>, i32) {
    let mut stdin = child.stdin.take().expect("stdin piped");
    let stdout = child.stdout.take().expect("stdout piped");
    let mut requests: Vec<String> = Vec::new();
    let mut reader = BufReader::new(stdout);
    let mut line = String::new();

    loop {
        line.clear();
        match reader.read_line(&mut line) {
            Ok(0) | Err(_) => break,
            Ok(_) => {
                let request: serde_json::Value =
                    serde_json::from_str(line.trim()).expect("protocol line is JSON");
                let id = request["id"].as_u64().expect("request id");
                let method = request["method"].as_str().expect("method").to_string();
                requests.push(method.clone());
                let result: serde_json::Value = match method.as_str() {
                    "db.query" => serde_json::json!([{ "id": "41" }]),
                    "net.isConnected" => serde_json::json!(true),
                    "http.post" => serde_json::json!(1),
                    "db.exec" => serde_json::json!(1),
                    other => panic!("unexpected native call {other}"),
                };
                let response = serde_json::json!({ "id": id, "result": result });
                writeln!(stdin, "{response}").expect("write response");
            }
        }
    }
    let status = child.wait().expect("child exits");
    let _ = trace_path;
    (requests, status.code().unwrap_or(-1))
}

fn unique_path(name: &str) -> std::path::PathBuf {
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.subsec_nanos())
        .unwrap_or(0);
    let mut dir = std::env::temp_dir();
    dir.push(format!("{name}-{}-{nanos}", std::process::id()));
    dir
}

#[test]
fn exec_host_recording_replays_without_a_host() {
    let script_path = unique_path("cortex-trace-script");
    let trace_path = unique_path("cortex-trace");
    std::fs::write(&script_path, SCRIPT).expect("write script fixture");

    // 1. Record a real host session.
    let child = Command::new(env!("CARGO_BIN_EXE_cortex-engine"))
        .args(["exec-host", "--script"])
        .arg(&script_path)
        .arg("--record")
        .arg(&trace_path)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("spawn exec-host");
    let (requests, code) = serve_and_collect(child, trace_path.to_str().unwrap());
    assert_eq!(code, 0, "the recorded session must succeed");
    assert_eq!(requests, vec!["db.query", "net.isConnected", "http.post"]);

    // 2. Replay with NO host attached: same script, same outcome.
    let output = Command::new(env!("CARGO_BIN_EXE_cortex-engine"))
        .args(["replay"])
        .arg(&script_path)
        .arg("--trace")
        .arg(&trace_path)
        .output()
        .expect("spawn replay");
    assert!(
        output.status.success(),
        "replay must succeed: {}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(String::from_utf8_lossy(&output.stdout).contains("ok: 1"));

    // 3. A script that diverges from its trace is rejected.
    let divergent = SCRIPT.replace("api.example/sync", "api.other/sync");
    std::fs::write(&script_path, divergent).expect("rewrite script fixture");
    let output = Command::new(env!("CARGO_BIN_EXE_cortex-engine"))
        .args(["replay"])
        .arg(&script_path)
        .arg("--trace")
        .arg(&trace_path)
        .output()
        .expect("spawn replay");
    assert!(
        !output.status.success(),
        "a divergent script must fail replay"
    );
    assert!(String::from_utf8_lossy(&output.stderr).contains("replay mismatch"));

    std::fs::remove_file(&script_path).ok();
    std::fs::remove_file(&trace_path).ok();
}

#[test]
fn run_record_round_trips_through_replay() {
    let script_path = unique_path("cortex-run-trace-script");
    let trace_path = unique_path("cortex-run-trace");
    std::fs::write(&script_path, SCRIPT).expect("write script fixture");

    let output = Command::new(env!("CARGO_BIN_EXE_cortex-engine"))
        .args(["run"])
        .arg(&script_path)
        .arg("--record")
        .arg(&trace_path)
        .output()
        .expect("spawn run");
    assert!(
        output.status.success(),
        "`run --record` must succeed: {}",
        String::from_utf8_lossy(&output.stderr)
    );

    // The in-memory double defaults to offline: the trace must show the
    // short-circuit (no http.post), and replay agrees.
    let trace = std::fs::read_to_string(&trace_path).expect("read trace");
    assert!(trace.contains("db.query"));
    assert!(trace.contains("net.isConnected"));
    assert!(
        !trace.contains("http.post"),
        "offline run must not post: {trace}"
    );
    assert!(trace.contains(cortex_engine::trace::RESULT_MARKER));

    let output = Command::new(env!("CARGO_BIN_EXE_cortex-engine"))
        .args(["replay"])
        .arg(&script_path)
        .arg("--trace")
        .arg(&trace_path)
        .output()
        .expect("spawn replay");
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );

    std::fs::remove_file(&script_path).ok();
    std::fs::remove_file(&trace_path).ok();
}
