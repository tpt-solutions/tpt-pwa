// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! WebAssembly binding for the cortex-engine VM: the PWA's `.ctx`
//! playground runs the SAME lexer → parser → compiler → VM pipeline the
//! daemon executes, in a deterministic in-memory sandbox (the engine's
//! `MemoryNative`). No network, no filesystem — `http.post` and `db.exec`
//! are recorded and shown in the UI.
//!
//! Every method speaks JSON strings at the boundary (the engine's own
//! `value_to_json`/`json_to_value` do the marshalling), so the JS side needs
//! nothing beyond `JSON.parse`.

use wasm_bindgen::prelude::*;

use cortex_engine::host::{json_to_value, value_to_json};
use cortex_engine::natives::{MemoryNative, NativeEnv};

/// One sandbox instance: configurable fake rows + connectivity flag, and
/// every effect a run produces is recorded for the UI.
#[wasm_bindgen]
pub struct Playground {
    natives: MemoryNative,
}

#[wasm_bindgen]
impl Playground {
    #[wasm_bindgen(constructor)]
    pub fn new() -> Playground {
        Playground {
            natives: MemoryNative::default(),
        }
    }

    /// Configure the rows `native.db.query` answers with: a JSON array of
    /// objects (`[{"id": "41"}, …]`). Returns an error message for JSON
    /// that is not an array.
    pub fn set_rows(&mut self, json: &str) -> Result<(), JsError> {
        self.set_rows_inner(json).map_err(|e| JsError::new(&e))
    }

    fn set_rows_inner(&mut self, json: &str) -> Result<(), String> {
        let parsed: serde_json::Value =
            serde_json::from_str(json).map_err(|e| format!("rows must be JSON: {e}"))?;
        let rows = match parsed {
            serde_json::Value::Array(items) => items.iter().map(json_to_value).collect::<Vec<_>>(),
            _ => return Err("rows must be a JSON array of objects".to_string()),
        };
        self.natives.rows = rows;
        Ok(())
    }

    /// What `native.net.isConnected()` answers.
    pub fn set_connected(&mut self, connected: bool) {
        self.natives.connected = connected;
    }

    /// Parse, compile and run a `.ctx` script against the sandbox. The
    /// result is a JSON object:
    /// `{"ok":true,"result":<value>}` or
    /// `{"ok":false,"class":"permanent"|"transient","error":"…"}` — the same
    /// permanent/transient classes the CLI reports via exit codes 2/1.
    pub fn run(&mut self, source: &str) -> String {
        let outcome = cortex_engine::run_source(source, &mut self.natives);
        match outcome {
            Ok(result) => serde_json::json!({ "ok": true, "result": value_to_json(&result) }).to_string(),
            Err(err) => {
                let class = match &err {
                    cortex_engine::EngineError::Parse(_) | cortex_engine::EngineError::Compile(_) => "permanent",
                    cortex_engine::EngineError::Vm(_) => "transient",
                };
                serde_json::json!({ "ok": false, "class": class, "error": err.to_string() }).to_string()
            }
        }
    }

    /// The script's static native surface (the capability manifest, same
    /// form the daemon's `-allow-natives` checks): `["db.query", …]`.
    /// `Err` carries the parse/compile failure.
    pub fn manifest(&self, source: &str) -> Result<String, JsError> {
        self.manifest_inner(source).map_err(|e| JsError::new(&e))
    }

    fn manifest_inner(&self, source: &str) -> Result<String, String> {
        let task = cortex_engine::parser::parse_task(source).map_err(|e| e.to_string())?;
        let natives = cortex_engine::compiler::manifest(&task).map_err(|e| e.to_string())?;
        Ok(serde_json::json!(natives).to_string())
    }

    /// Effects recorded by `run`: `[[url, body], …]` in call order.
    pub fn posts(&self) -> String {
        let posts: Vec<serde_json::Value> = self
            .natives
            .posts
            .iter()
            .map(|(url, body)| serde_json::json!([url, value_to_json(body)]))
            .collect();
        serde_json::json!(posts).to_string()
    }

    /// SQL effects recorded by `run`: `[[sql, [params…]], …]` in call order.
    pub fn executed_sql(&self) -> String {
        let statements: Vec<serde_json::Value> = self
            .natives
            .executed_sql
            .iter()
            .map(|(sql, params)| {
                serde_json::json!([sql, params.iter().map(value_to_json).collect::<Vec<_>>()])
            })
            .collect();
        serde_json::json!(statements).to_string()
    }
}

impl Default for Playground {
    fn default() -> Self {
        Self::new()
    }
}

/// Conveniences over [`Playground`] used by tests (and usable from Rust
/// hosts): the wasm-bindgen methods take/return strings, so assert on
/// values instead of JSON prose.
#[cfg(test)]
mod tests {
    use super::*;
    use cortex_engine::value::Value;

    fn posts_of(playground: &Playground) -> Vec<(String, Value)> {
        playground.natives.posts.clone()
    }

    #[test]
    fn runs_scripts_against_the_sandbox() {
        let mut playground = Playground::new();
        playground
            .set_rows(r#"[{"id": "41"}, {"id": "42"}]"#)
            .expect("valid rows");
        playground.set_connected(true);

        let report = playground.run(
            r#"
            task t() -> void {
                let rows = native.db.query("SELECT id FROM queue");
                for row in rows {
                    native.http.post("https://api.example/sync", row);
                }
                return rows[1].id;
            }
        "#,
        );
        assert!(report.contains("\"ok\":true"), "run report: {report}");
        assert!(report.contains("\"42\""), "result: {report}");
        assert_eq!(posts_of(&playground).len(), 2, "one post per row");
    }

    #[test]
    fn error_classes_match_the_cli_contract() {
        let mut playground = Playground::new();
        // Parse failure: permanent.
        let report = playground.run("task t() -> void { return ); }");
        assert!(report.contains("\"class\":\"permanent\""), "got: {report}");
        // Runtime failure: transient.
        let report = playground.run("task t() -> void { return 1 / 0; }");
        assert!(report.contains("\"class\":\"transient\""), "got: {report}");
        assert!(report.contains("division by zero"), "got: {report}");
    }

    #[test]
    fn manifest_and_validation_errors_surface() {
        let playground = Playground::new();
        let manifest = playground
            .manifest_inner("task t() -> void { native.http.post(\"x\", 1); }")
            .expect("manifests");
        assert!(manifest.contains("http.post"), "got: {manifest}");
        // A script that would not compile yields the compile error instead.
        let err = playground
            .manifest_inner("task t() -> void { let f = native.db.dropAll; }")
            .expect_err("unknown native must fail");
        assert!(err.contains("unknown native"), "got: {err}");

        let mut playground = Playground::new();
        assert!(playground.set_rows_inner("not json").is_err());
    }

    #[test]
    fn row_values_round_trip_through_json() {
        let mut playground = Playground::new();
        playground
            .set_rows(r#"[{"n": 7, "f": 1.5, "s": "x", "b": true, "nested": {"k": [1]}}]"#)
            .expect("valid rows");
        let report = playground.run(
            r#"
            task t() -> void {
                let rows = native.db.query("s");
                let row = rows[0];
                return [row.n, row.nested.k[0]];
            }
        "#,
        );
        assert!(report.contains("\"ok\":true"), "got: {report}");
        assert!(report.contains("[7,1]"), "got: {report}");
        assert_eq!(playground.executed_sql(), "[]");
    }
}
