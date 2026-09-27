// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! End-to-end test of the stdio host protocol: spawn the actual
//! `cortex-engine exec-host` binary, play the host side (daemon), and verify
//! the script's native calls arrive as protocol requests and steer execution.

use std::io::{BufRead, BufReader, Write};
use std::process::{Child, Command, Stdio};

const SCRIPT: &str = r#"
task hostDriven() -> void {
    let rows = native.db.query("SELECT * FROM queue WHERE status = 'pending'");
    if native.net.isConnected() {
        for row in rows {
            native.http.post("https://api.example/sync", row);
            native.db.exec("UPDATE queue SET status='synced' WHERE id = ?", row.id);
        }
    }
}
"#;

/// Play the daemon: answer each native request, recording what the script did.
fn serve(mut child: Child, connected: bool) -> (Vec<String>, i32) {
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
                requests.push(format!("{method}:{}", request["params"]));
                let result: serde_json::Value = match method.as_str() {
                    "db.query" => serde_json::json!([{ "id": "41" }, { "id": "42" }]),
                    "net.isConnected" => serde_json::json!(connected),
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
    (requests, status.code().unwrap_or(-1))
}

#[test]
fn exec_host_serves_native_calls_from_parent_process() {
    let mut dir = std::env::temp_dir();
    dir.push(format!("cortex-exec-host-{}.ctx", std::process::id()));
    std::fs::write(&dir, SCRIPT).expect("write script fixture");

    let child = Command::new(env!("CARGO_BIN_EXE_cortex-engine"))
        .args(["exec-host", "--script"])
        .arg(&dir)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("spawn exec-host");
    let (requests, code) = serve(child, true);
    std::fs::remove_file(&dir).ok();

    assert_eq!(code, 0, "exec-host must exit 0 when the host answers");
    let methods: Vec<String> = requests
        .iter()
        .map(|r| r.split(':').next().unwrap().to_string())
        .collect();
    assert_eq!(
        methods,
        vec![
            "db.query",
            "net.isConnected",
            "http.post",
            "db.exec",
            "http.post",
            "db.exec"
        ],
        "script must drive the protocol per row: {requests:?}"
    );
    assert!(
        requests[2].contains("api.example/sync"),
        "post target comes from the script"
    );
    assert!(
        requests[5].contains("\"41\"") || requests[5].contains("\"42\""),
        "row id flows into db.exec bind: {requests:?}"
    );
}

#[test]
fn exec_host_skips_work_when_host_reports_offline() {
    let mut dir = std::env::temp_dir();
    dir.push(format!("cortex-exec-host-off-{}.ctx", std::process::id()));
    std::fs::write(&dir, SCRIPT).expect("write script fixture");

    let child = Command::new(env!("CARGO_BIN_EXE_cortex-engine"))
        .args(["exec-host", "--script"])
        .arg(&dir)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("spawn exec-host");
    let (requests, code) = serve(child, false);
    std::fs::remove_file(&dir).ok();

    assert_eq!(code, 0);
    let methods: Vec<String> = requests
        .iter()
        .map(|r| r.split(':').next().unwrap().to_string())
        .collect();
    assert_eq!(
        methods,
        vec!["db.query", "net.isConnected"],
        "offline must stop after the connectivity probe"
    );
}
