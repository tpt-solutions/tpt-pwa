// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! Every recipe in examples/recipes/ must parse, compile, and execute
//! cleanly against the scripted in-memory environment. This keeps the
//! cookbook honest: a recipe that no longer compiles breaks the build.

use cortex_engine::natives::{row, MemoryNative, NativeRegistry};
use cortex_engine::value::Value;
use cortex_engine::{compiler, parser, vm::Vm};

const RECIPES: &[(&str, &str)] = &[
    (
        "retry-upload.ctx",
        include_str!("../../examples/recipes/retry-upload.ctx"),
    ),
    (
        "periodic-fetch.ctx",
        include_str!("../../examples/recipes/periodic-fetch.ctx"),
    ),
    (
        "batch-sync.ctx",
        include_str!("../../examples/recipes/batch-sync.ctx"),
    ),
];

#[test]
fn every_recipe_parses_compiles_and_runs() {
    for (name, source) in RECIPES {
        let task = parser::parse_task(source).unwrap_or_else(|e| panic!("{name}: parse: {e}"));
        let program = compiler::compile(&task).unwrap_or_else(|e| panic!("{name}: compile: {e}"));
        let registry = NativeRegistry::standard();
        let mut env = MemoryNative::default();
        Vm::new(program, &mut env, &registry)
            .run()
            .unwrap_or_else(|e| panic!("{name}: run: {e}"));
    }
}

#[test]
fn retry_upload_posts_only_while_connected_and_marks_rows() {
    let (_, source) = RECIPES[0];
    let task = parser::parse_task(source).expect("parses");
    let program = compiler::compile(&task).expect("compiles");
    let registry = NativeRegistry::standard();

    // Offline: nothing posts.
    let mut env = MemoryNative {
        connected: false,
        rows: vec![row(&[
            ("id", Value::Int(1)),
            ("body", Value::Str("x".into())),
        ])],
        ..MemoryNative::default()
    };
    Vm::new(program.clone(), &mut env, &registry).run().unwrap();
    assert!(env.posts.is_empty(), "offline run must not post");

    // Online: one post per pending row, then the status update.
    let mut env = MemoryNative {
        connected: true,
        rows: vec![
            row(&[("id", Value::Int(1)), ("body", Value::Str("x".into()))]),
            row(&[("id", Value::Int(2)), ("body", Value::Str("y".into()))]),
        ],
        ..MemoryNative::default()
    };
    Vm::new(program, &mut env, &registry).run().unwrap();
    assert_eq!(env.posts.len(), 2, "every pending row is posted");
    assert_eq!(env.executed_sql.len(), 2, "every row is marked uploaded");
}
