// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! End-to-end execution of the spec §6 `sync.ctx` script through the real
//! pipeline (lex -> parse -> compile -> VM), plus offline behavior. The
//! script's data comes from `native.outbox.entries()` (the daemon's injected
//! view of the task's pending rows); `native.db.*` is a separate real-SQL
//! surface and plays no part in the sync flow.

use cortex_engine::natives::{row, MemoryNative};
use cortex_engine::run_source;
use cortex_engine::value::Value;

const SYNC_CTX: &str = include_str!("../examples/sync.ctx");

fn entry(id: &str) -> Value {
    row(&[
        ("id", Value::Str(id.into())),
        ("status", Value::Str("pending".into())),
        ("endpoint", Value::Str("https://api.tpt/sync".into())),
    ])
}

#[test]
fn syncs_pending_rows_when_online() {
    let mut env = MemoryNative {
        connected: true,
        outbox: vec![entry("41"), entry("42")],
        ..MemoryNative::default()
    };
    let result = run_source(SYNC_CTX, &mut env).expect("script must run");
    assert_eq!(result, Value::Null);
    assert_eq!(env.posts.len(), 2);
    assert_eq!(env.posts[0].0, "https://api.tpt/sync");
    // The whole entry is the POST body (the endpoint travels inside it too).
    assert_eq!(env.posts[0].1, entry("41"));
    // No SQL: clearing entries is daemon bookkeeping, not a script effect.
    assert!(env.executed_sql.is_empty());
}

#[test]
fn skips_all_work_when_offline() {
    let mut env = MemoryNative {
        connected: false,
        outbox: vec![entry("41")],
        ..MemoryNative::default()
    };
    run_source(SYNC_CTX, &mut env).expect("script must run");
    assert!(env.posts.is_empty(), "offline must not post");
}

#[test]
fn empty_pending_queue_is_a_noop() {
    let mut env = MemoryNative {
        connected: true,
        ..MemoryNative::default()
    };
    run_source(SYNC_CTX, &mut env).expect("script must run");
    assert!(env.posts.is_empty());
}
