// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! Host-protocol native environment: lets an *external* process (today the
//! Go cortex-daemon, per docs in cortex-daemon/README.md) supply the effects
//! for `native.*` calls. The VM runs here; every `db.query`, `net.isConnected`
//! etc. travels as one line-delimited JSON request over stdout and blocks on
//! the matching response line on stdin:
//!
//! ```text
//!   engine -> host: {"id":1,"method":"http.post","params":["https://…",{…}]}
//!   host   -> engine: {"id":1,"result":200}   |   {"id":1,"error":"…"}
//! ```
//!
//! stdout carries ONLY protocol lines (diagnostics go to stderr), so the
//! peer can speak this from any language.

use std::io::{BufRead, Write};
use std::rc::Rc;

use crate::natives::NativeEnv;
use crate::value::Value;

/// Convert a script [`Value`] into its JSON representation.
pub fn value_to_json(value: &Value) -> serde_json::Value {
    match value {
        Value::Null => serde_json::Value::Null,
        Value::Bool(b) => serde_json::Value::Bool(*b),
        Value::Int(i) => serde_json::Value::from(*i),
        Value::Float(f) => serde_json::Value::from(*f),
        Value::Str(s) => serde_json::Value::from(s.as_str()),
        Value::List(items) => serde_json::Value::Array(items.iter().map(value_to_json).collect()),
        Value::Map(map) => serde_json::Value::Object(
            map.iter()
                .map(|(k, v)| (k.clone(), value_to_json(v)))
                .collect(),
        ),
    }
}

/// Convert a JSON value from the host into a script [`Value`].
pub fn json_to_value(json: &serde_json::Value) -> Value {
    match json {
        serde_json::Value::Null => Value::Null,
        serde_json::Value::Bool(b) => Value::Bool(*b),
        serde_json::Value::Number(n) => match n.as_i64() {
            Some(i) => Value::Int(i),
            None => Value::Float(n.as_f64().unwrap_or(f64::NAN)),
        },
        serde_json::Value::String(s) => Value::Str(s.clone()),
        serde_json::Value::Array(items) => {
            Value::List(Rc::new(items.iter().map(json_to_value).collect()))
        }
        serde_json::Value::Object(map) => Value::Map(Rc::new(
            map.iter()
                .map(|(k, v)| (k.clone(), json_to_value(v)))
                .collect(),
        )),
    }
}

pub struct HostNative<In: BufRead, Out: Write> {
    reader: In,
    writer: Out,
    next_id: u64,
}

impl<In: BufRead, Out: Write> HostNative<In, Out> {
    pub fn new(reader: In, writer: Out) -> Self {
        HostNative {
            reader,
            writer,
            next_id: 1,
        }
    }

    /// One round trip: write the request line, read the response line.
    fn call(&mut self, method: &str, params: &[Value]) -> Result<Value, String> {
        let id = self.next_id;
        self.next_id += 1;
        let request = serde_json::json!({
            "id": id,
            "method": method,
            "params": params.iter().map(value_to_json).collect::<Vec<_>>(),
        });
        writeln!(self.writer, "{request}").map_err(|e| format!("write to host: {e}"))?;
        self.writer
            .flush()
            .map_err(|e| format!("flush to host: {e}"))?;

        let mut line = String::new();
        let bytes = self
            .reader
            .read_line(&mut line)
            .map_err(|e| format!("read from host: {e}"))?;
        if bytes == 0 {
            return Err(format!("host closed the protocol stream during `{method}`"));
        }
        let response: serde_json::Value = serde_json::from_str(line.trim())
            .map_err(|e| format!("host sent invalid JSON: {e}"))?;
        let response_id = response
            .get("id")
            .and_then(serde_json::Value::as_u64)
            .unwrap_or(0);
        if response_id != id {
            return Err(format!(
                "host response id {response_id} does not match request id {id}"
            ));
        }
        if let Some(error) = response.get("error") {
            return Err(error.as_str().unwrap_or("unknown host error").to_string());
        }
        match response.get("result") {
            Some(result) => Ok(json_to_value(result)),
            None => Err("host response carries neither result nor error".to_string()),
        }
    }
}

impl<In: BufRead, Out: Write> NativeEnv for HostNative<In, Out> {
    fn db_query(&mut self, sql: &str, params: &[Value]) -> Result<Vec<Value>, String> {
        let mut args = vec![Value::Str(sql.to_string())];
        args.extend(params.iter().cloned());
        match self.call("db.query", &args)? {
            Value::List(rows) => Ok((*rows).clone()),
            other => Err(format!(
                "db.query returned {}, expected list",
                other.type_name()
            )),
        }
    }

    fn db_exec(&mut self, sql: &str, params: &[Value]) -> Result<Value, String> {
        let mut args = vec![Value::Str(sql.to_string())];
        args.extend(params.iter().cloned());
        self.call("db.exec", &args)
    }

    fn net_is_connected(&mut self) -> Result<bool, String> {
        match self.call("net.isConnected", &[])? {
            Value::Bool(connected) => Ok(connected),
            other => Err(format!(
                "net.isConnected returned {}, expected bool",
                other.type_name()
            )),
        }
    }

    fn outbox_entries(&mut self) -> Result<Vec<Value>, String> {
        match self.call("outbox.entries", &[])? {
            Value::List(rows) => Ok((*rows).clone()),
            other => Err(format!(
                "outbox.entries returned {}, expected list",
                other.type_name()
            )),
        }
    }

    fn http_post(&mut self, url: &str, body: &Value) -> Result<Value, String> {
        self.call("http.post", &[Value::Str(url.to_string()), body.clone()])
    }
}
