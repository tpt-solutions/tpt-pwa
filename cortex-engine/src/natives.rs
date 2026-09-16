// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! Native bindings (spec §6): `native.db`, `native.net`, `native.http`.
//! The VM stays pure -- every effect flows through this trait, which the
//! cortex-daemon implements with real OS access. Test doubles live here too,
//! so scripts can be executed deterministically in tests.

use crate::bytecode::NativeId;
use crate::value::Value;

/// Host-provided effectful operations. Errors propagate into the VM as
/// `VmError::Native` -- they must never panic.
pub trait NativeEnv {
    fn db_query(&mut self, sql: &str, params: &[Value]) -> Result<Vec<Value>, String>;
    fn db_exec(&mut self, sql: &str, params: &[Value]) -> Result<Value, String>;
    fn net_is_connected(&mut self) -> Result<bool, String>;
    fn http_post(&mut self, url: &str, body: &Value) -> Result<Value, String>;
}

/// Compile-time registry: validates native arity (as a maximum -- trailing
/// params are optional, e.g. `db.query(sql)` without bindings) so bad scripts
/// fail before execution.
#[derive(Clone, Debug)]
pub struct NativeRegistry {
    max_arity: [u8; 4],
}

impl NativeRegistry {
    pub fn standard() -> Self {
        NativeRegistry {
            max_arity: [2, 2, 0, 2],
        } // db.query, db.exec, net.isConnected, http.post
    }

    /// Whether a call with `argc` arguments is well-formed.
    pub fn accepts(&self, id: NativeId, argc: u8) -> bool {
        argc <= self.max_arity[id as usize]
    }
}

/// Deterministic in-memory environment for tests and the CLI: `db.query`
/// answers from a fixed row set, `db.exec` records SQL, `http.post` records
/// (url, body) pairs, `net.isConnected` returns a fixed flag.
#[derive(Default)]
pub struct MemoryNative {
    pub connected: bool,
    pub rows: Vec<Value>,
    pub executed_sql: Vec<(String, Vec<Value>)>,
    pub posts: Vec<(String, Value)>,
}

impl NativeEnv for MemoryNative {
    fn db_query(&mut self, _sql: &str, _params: &[Value]) -> Result<Vec<Value>, String> {
        Ok(self.rows.clone())
    }

    fn db_exec(&mut self, sql: &str, params: &[Value]) -> Result<Value, String> {
        self.executed_sql.push((sql.to_string(), params.to_vec()));
        Ok(Value::Int(self.executed_sql.len() as i64))
    }

    fn net_is_connected(&mut self) -> Result<bool, String> {
        Ok(self.connected)
    }

    fn http_post(&mut self, url: &str, body: &Value) -> Result<Value, String> {
        self.posts.push((url.to_string(), body.clone()));
        Ok(Value::Int(self.posts.len() as i64))
    }
}

/// Map helper used by hosts to build row values.
pub fn row(pairs: &[(&str, Value)]) -> Value {
    Value::Map(
        pairs
            .iter()
            .map(|(k, v)| (k.to_string(), v.clone()))
            .collect(),
    )
}
