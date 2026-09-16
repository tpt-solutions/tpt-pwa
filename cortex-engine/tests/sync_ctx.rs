// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! End-to-end execution of the spec §6 `sync.ctx` script through the real
//! pipeline (lex -> parse -> compile -> VM), plus offline behavior.

use cortex_engine::natives::{row, MemoryNative};
use cortex_engine::run_source;
use cortex_engine::value::Value;

const SYNC_CTX: &str = include_str!("../examples/sync.ctx");

#[test]
fn syncs_pending_rows_when_online() {
    let mut env = MemoryNative {
        connected: true,
        rows: vec![
            row(&[
                ("id", Value::Str("41".into())),
                ("status", Value::Str("pending".into())),
            ]),
            row(&[
                ("id", Value::Str("42".into())),
                ("status", Value::Str("pending".into())),
            ]),
        ],
        ..MemoryNative::default()
    };
    let result = run_source(SYNC_CTX, &mut env).expect("script must run");
    assert_eq!(result, Value::Null);
    assert_eq!(env.posts.len(), 2);
    assert_eq!(env.posts[0].0, "https://api.tpt/sync");
    assert_eq!(env.executed_sql.len(), 2);
    assert_eq!(
        env.executed_sql[0].0,
        "UPDATE queue SET status = 'synced' WHERE id = ?"
    );
    assert_eq!(env.executed_sql[0].1, vec![Value::Str("41".into())]);
}

#[test]
fn skips_all_work_when_offline() {
    let mut env = MemoryNative {
        connected: false,
        rows: vec![row(&[("id", Value::Str("41".into()))])],
        ..MemoryNative::default()
    };
    run_source(SYNC_CTX, &mut env).expect("script must run");
    assert!(env.posts.is_empty(), "offline must not post");
    assert!(
        env.executed_sql.is_empty(),
        "offline must not mark rows synced"
    );
}

#[test]
fn empty_pending_queue_is_a_noop() {
    let mut env = MemoryNative {
        connected: true,
        ..MemoryNative::default()
    };
    run_source(SYNC_CTX, &mut env).expect("script must run");
    assert!(env.posts.is_empty());
    assert!(env.executed_sql.is_empty());
}
