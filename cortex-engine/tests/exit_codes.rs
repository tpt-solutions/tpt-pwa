// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! The CLI's exit codes carry failure classes: 1 = runtime (transient, the
//! daemon may retry), 2 = parse/compile or misuse (permanent, retrying the
//! same bytes can never succeed).

use std::process::Command;

fn run_cli(script: &str, filename: &str) -> (i32, String) {
    let dir = std::env::temp_dir().join(format!(
        "cortex-engine-exit-{}-{}",
        std::process::id(),
        filename
    ));
    std::fs::create_dir_all(&dir).expect("temp dir");
    let path = dir.join(filename);
    std::fs::write(&path, script).expect("write script");
    let output = Command::new(env!("CARGO_BIN_EXE_cortex-engine"))
        .args(["run", path.to_str().unwrap()])
        .output()
        .expect("spawn cortex-engine");
    let _ = std::fs::remove_dir_all(&dir);
    (
        output.status.code().unwrap_or(-1),
        String::from_utf8_lossy(&output.stderr).into_owned(),
    )
}

#[test]
fn parse_and_compile_failures_exit_2_permanent() {
    // Broken syntax: never going to run, no matter how often retried.
    let (code, stderr) = run_cli("task t() -> void { let = ; }", "parse-error.ctx");
    assert_eq!(code, 2, "parse error must exit 2, stderr: {stderr}");

    // Compilable syntax, but semantically rejected (non-native call).
    let (code, stderr) = run_cli("task t() -> void { helper(1); }", "compile-error.ctx");
    assert_eq!(code, 2, "compile error must exit 2, stderr: {stderr}");
}

#[test]
fn runtime_failures_exit_1_transient() {
    // Parses and compiles; fails at runtime on the data: retryable.
    let (code, stderr) = run_cli("task t() -> void { return 1 + true; }", "runtime-error.ctx");
    assert_eq!(code, 1, "runtime error must exit 1, stderr: {stderr}");
}

#[test]
fn success_exits_0() {
    let (code, _) = run_cli("task t() -> void { return 1 + 2; }", "ok.ctx");
    assert_eq!(code, 0);
}
