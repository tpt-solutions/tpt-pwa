// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! Deterministic trace recording and replay for host-call sequences.
//!
//! A trace is one JSON object per line (JSONL), mirroring the stdio host
//! protocol's method names:
//!
//! ```text
//!   {"method":"db.query","params":["SELECT …"],"result":[…]}
//!   {"method":"net.isConnected","params":[],"result":true}
//!   {"method":"http.post","params":[…],"error":"connect refused"}
//!   {"method":"__result","result":42}
//! ```
//!
//! `RecordingNative` wraps a real environment (the in-memory double for
//! fixtures, or [`crate::host::HostNative`] for a live daemon session) and
//! appends one line per call — live, so even a crashed session leaves a
//! usable trace for a bug report. `ReplayNative` answers from a trace,
//! failing loudly on any divergence (wrong method, wrong params, exhausted
//! trace), which is what makes a traced session a portable regression
//! fixture: the same script bytes must make exactly the same calls and get
//! exactly the same answers.

use std::collections::VecDeque;
use std::io::{BufRead, Write};

use crate::host::{json_to_value, value_to_json};
use crate::natives::NativeEnv;
use crate::value::Value;

/// Final-entry marker: the script's overall result (or error) so a replay
/// can verify not just the calls but the outcome.
pub const RESULT_MARKER: &str = "__result";

/// Method names, shared with the stdio host protocol (src/host.rs).
pub mod method {
    pub const DB_QUERY: &str = "db.query";
    pub const DB_EXEC: &str = "db.exec";
    pub const NET_IS_CONNECTED: &str = "net.isConnected";
    pub const HTTP_POST: &str = "http.post";
    pub const OUTBOX_ENTRIES: &str = "outbox.entries";
}

#[derive(Clone, Debug, PartialEq)]
pub struct TraceEntry {
    pub method: String,
    pub params: Vec<Value>,
    pub result: Option<Value>,
    pub error: Option<String>,
}

impl TraceEntry {
    pub fn to_json(&self) -> serde_json::Value {
        let mut json = serde_json::json!({
            "method": self.method,
            "params": self.params.iter().map(value_to_json).collect::<Vec<_>>(),
        });
        if let Some(result) = &self.result {
            json["result"] = value_to_json(result);
        }
        if let Some(error) = &self.error {
            json["error"] = serde_json::Value::String(error.clone());
        }
        json
    }

    pub fn from_json(json: &serde_json::Value) -> Result<TraceEntry, String> {
        let method = json
            .get("method")
            .and_then(serde_json::Value::as_str)
            .ok_or("trace entry without a method")?
            .to_string();
        let params = match json.get("params") {
            Some(serde_json::Value::Array(items)) => items.iter().map(json_to_value).collect(),
            Some(_) => return Err(format!("trace entry `{method}` has non-array params")),
            None => return Err(format!("trace entry `{method}` has no params")),
        };
        Ok(TraceEntry {
            method,
            params,
            result: json.get("result").map(json_to_value),
            error: json
                .get("error")
                .and_then(serde_json::Value::as_str)
                .map(str::to_string),
        })
    }
}

/// Append one entry as a JSONL line; best-effort (a full disk must not turn
/// a recording session into a crash — the run's outcome still propagates).
fn write_entry<W: Write>(writer: &mut W, entry: &TraceEntry) {
    let _ = writeln!(writer, "{}", entry.to_json());
}

/// Wraps any [`NativeEnv`] and appends every call to a JSONL writer.
pub struct RecordingNative<E, W>
where
    E: NativeEnv,
    W: Write,
{
    inner: E,
    writer: W,
}

impl<E, W> RecordingNative<E, W>
where
    E: NativeEnv,
    W: Write,
{
    pub fn new(inner: E, writer: W) -> Self {
        RecordingNative { inner, writer }
    }

    /// The wrapped environment (e.g. to inspect `MemoryNative::posts` after
    /// a recorded run).
    pub fn inner(&self) -> &E {
        &self.inner
    }

    /// Direct writer access (the CLI appends the final `__result` entry).
    pub fn writer_mut(&mut self) -> &mut W {
        &mut self.writer
    }

    pub fn flush(&mut self) -> std::io::Result<()> {
        self.writer.flush()
    }

    fn record(
        &mut self,
        method: &str,
        params: &[Value],
        outcome: Result<Value, String>,
    ) -> Result<Value, String> {
        let entry = TraceEntry {
            method: method.to_string(),
            params: params.to_vec(),
            result: outcome.as_ref().ok().cloned(),
            error: outcome.as_ref().err().cloned(),
        };
        write_entry(&mut self.writer, &entry);
        outcome
    }
}

impl<E, W> NativeEnv for RecordingNative<E, W>
where
    E: NativeEnv,
    W: Write,
{
    fn db_query(&mut self, sql: &str, params: &[Value]) -> Result<Vec<Value>, String> {
        let mut args = vec![Value::Str(sql.to_string())];
        args.extend(params.iter().cloned());
        let outcome = self
            .inner
            .db_query(sql, params)
            .map(|rows| Value::List(std::rc::Rc::new(rows)));
        match self.record(method::DB_QUERY, &args, outcome)? {
            Value::List(rows) => Ok((*rows).clone()),
            _ => Err("recorder stored a non-list db.query result".to_string()),
        }
    }

    fn db_exec(&mut self, sql: &str, params: &[Value]) -> Result<Value, String> {
        let mut args = vec![Value::Str(sql.to_string())];
        args.extend(params.iter().cloned());
        let outcome = self.inner.db_exec(sql, params);
        self.record(method::DB_EXEC, &args, outcome)
    }

    fn net_is_connected(&mut self) -> Result<bool, String> {
        let outcome = self.inner.net_is_connected().map(Value::Bool);
        match self.record(method::NET_IS_CONNECTED, &[], outcome)? {
            Value::Bool(connected) => Ok(connected),
            _ => Err("recorder stored a non-bool net result".to_string()),
        }
    }

    fn http_post(&mut self, url: &str, body: &Value) -> Result<Value, String> {
        let outcome = self.inner.http_post(url, body);
        self.record(
            method::HTTP_POST,
            &[Value::Str(url.to_string()), body.clone()],
            outcome,
        )
    }

    fn outbox_entries(&mut self) -> Result<Vec<Value>, String> {
        let outcome = self
            .inner
            .outbox_entries()
            .map(|rows| Value::List(std::rc::Rc::new(rows)));
        match self.record(method::OUTBOX_ENTRIES, &[], outcome)? {
            Value::List(rows) => Ok((*rows).clone()),
            _ => Err("recorder stored a non-list outbox result".to_string()),
        }
    }
}

/// Answers native calls from a recorded trace. Divergence of any kind is an
/// error carrying the offending call, so a fixture that no longer replays
/// names the exact point the script and its trace disagree.
pub struct ReplayNative {
    entries: VecDeque<TraceEntry>,
}

impl ReplayNative {
    pub fn from_reader<R: BufRead>(reader: R) -> Result<Self, String> {
        let mut entries = VecDeque::new();
        for (index, line) in reader.lines().enumerate() {
            let line = line.map_err(|e| format!("read trace: {e}"))?;
            if line.trim().is_empty() {
                continue;
            }
            let json: serde_json::Value = serde_json::from_str(&line)
                .map_err(|e| format!("trace line {}: {e}", index + 1))?;
            entries.push_back(
                TraceEntry::from_json(&json)
                    .map_err(|e| format!("trace line {}: {e}", index + 1))?,
            );
        }
        Ok(ReplayNative { entries })
    }

    /// Entries not consumed by the replay: a script that makes FEWER calls
    /// than recorded diverged just as surely as one that makes more.
    pub fn unconsumed(&self) -> usize {
        self.entries.len()
    }

    /// Pop the trailing `__result` bookkeeping entry, when present, so the
    /// unconsumed check sees only real native calls. Returns it for
    /// `verify_result`.
    pub fn consume_result_marker(&mut self) -> Option<TraceEntry> {
        if self
            .entries
            .back()
            .is_some_and(|e| e.method == RESULT_MARKER)
        {
            self.entries.pop_back()
        } else {
            None
        }
    }

    /// The recorded `__result` entry, when the trace carries one.
    pub fn recorded_result(&self) -> Option<&TraceEntry> {
        self.entries.back().filter(|e| e.method == RESULT_MARKER)
    }

    fn next_answer(
        &mut self,
        method: &str,
        params: &[Value],
    ) -> Result<Result<Value, String>, String> {
        let entry = self.entries.pop_front().ok_or_else(|| {
            format!("trace exhausted at `{method}`: the script made more calls than recorded")
        })?;
        if entry.method != method {
            return Err(format!(
                "replay mismatch: script called `{method}` but the trace recorded `{}`",
                entry.method
            ));
        }
        let recorded: Vec<serde_json::Value> = entry.params.iter().map(value_to_json).collect();
        let actual: Vec<serde_json::Value> = params.iter().map(value_to_json).collect();
        if recorded != actual {
            return Err(format!(
                "replay mismatch at `{method}`: params diverged (trace: {recorded:?}, script: {actual:?})"
            ));
        }
        if let Some(error) = entry.error.clone() {
            return Ok(Err(error));
        }
        Ok(Ok(entry.result.clone().ok_or_else(|| {
            format!("trace entry `{method}` carries neither result nor error")
        })?))
    }
}

impl NativeEnv for ReplayNative {
    fn db_query(&mut self, sql: &str, params: &[Value]) -> Result<Vec<Value>, String> {
        let mut args = vec![Value::Str(sql.to_string())];
        args.extend(params.iter().cloned());
        match self.next_answer(method::DB_QUERY, &args)? {
            Ok(Value::List(rows)) => Ok((*rows).clone()),
            Ok(other) => Err(format!(
                "trace db.query result is {}, expected list",
                other.type_name()
            )),
            Err(message) => Err(message),
        }
    }

    fn db_exec(&mut self, sql: &str, params: &[Value]) -> Result<Value, String> {
        let mut args = vec![Value::Str(sql.to_string())];
        args.extend(params.iter().cloned());
        match self.next_answer(method::DB_EXEC, &args)? {
            Ok(value) => Ok(value),
            Err(message) => Err(message),
        }
    }

    fn net_is_connected(&mut self) -> Result<bool, String> {
        match self.next_answer(method::NET_IS_CONNECTED, &[])? {
            Ok(Value::Bool(connected)) => Ok(connected),
            Ok(other) => Err(format!(
                "trace net result is {}, expected bool",
                other.type_name()
            )),
            Err(message) => Err(message),
        }
    }

    fn http_post(&mut self, url: &str, body: &Value) -> Result<Value, String> {
        match self.next_answer(
            method::HTTP_POST,
            &[Value::Str(url.to_string()), body.clone()],
        )? {
            Ok(value) => Ok(value),
            Err(message) => Err(message),
        }
    }

    fn outbox_entries(&mut self) -> Result<Vec<Value>, String> {
        match self.next_answer(method::OUTBOX_ENTRIES, &[])? {
            Ok(Value::List(rows)) => Ok((*rows).clone()),
            Ok(other) => Err(format!(
                "trace outbox result is {}, expected list",
                other.type_name()
            )),
            Err(message) => Err(message),
        }
    }
}

/// Append the script's overall outcome to a trace (the `__result` line) so
/// a replay can verify the end-to-end result, not just the call sequence.
pub fn record_result<W: Write>(writer: &mut W, outcome: &Result<Value, String>) {
    let entry = TraceEntry {
        method: RESULT_MARKER.to_string(),
        params: Vec::new(),
        result: outcome.as_ref().ok().cloned(),
        error: outcome.as_ref().err().cloned(),
    };
    write_entry(writer, &entry);
}

/// Verify a replay's outcome against the trace's recorded `__result` entry.
/// `Ok(())` on match; a description of the divergence otherwise.
pub fn verify_result(
    recorded: Option<&TraceEntry>,
    outcome: &Result<Value, String>,
) -> Result<(), String> {
    let Some(entry) = recorded else {
        return Ok(()); // trace without a marker: calls only, nothing to check
    };
    if entry.method != RESULT_MARKER {
        return Ok(());
    }
    match (&entry.result, &entry.error, outcome) {
        (Some(recorded), _, Ok(actual)) => {
            if value_to_json(recorded) == value_to_json(actual) {
                Ok(())
            } else {
                Err(format!(
                    "replay result mismatch: trace recorded {recorded}, script returned {actual}"
                ))
            }
        }
        (_, Some(recorded_error), Err(actual_error)) => {
            // Replays are deterministic: the replayed failure is the trace's
            // own recorded message, so exact comparison is the check.
            if recorded_error == actual_error {
                Ok(())
            } else {
                Err(format!(
                    "replay error mismatch: trace recorded `{recorded_error}`, replay failed with `{actual_error}`"
                ))
            }
        }
        (Some(recorded), _, Err(actual)) => Err(format!(
            "replay result mismatch: trace recorded {recorded}, replay failed with `{actual}`"
        )),
        (_, Some(recorded_error), Ok(actual)) => Err(format!(
            "replay result mismatch: trace recorded error `{recorded_error}`, replay returned {actual}"
        )),
        (None, None, _) => Ok(()),
    }
}
