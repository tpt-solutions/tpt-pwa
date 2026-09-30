// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! End-to-end language tests: source -> parse -> compile -> run for every
//! feature of the `.ctx` language (null, arithmetic with `* / %` and unary
//! minus, assignment, `while`, list/map literals, functions).

use cortex_engine::natives::{MemoryNative, NativeRegistry};
use cortex_engine::value::Value;
use cortex_engine::{EngineError, Vm, VmError};

fn run(source: &str) -> Result<Value, EngineError> {
    let task = cortex_engine::parser::parse_task(source)?;
    let program = cortex_engine::compiler::compile(&task)?;
    let registry = NativeRegistry::standard();
    let mut env = MemoryNative::default();
    Ok(Vm::new(program, &mut env, &registry).run()?)
}

fn run_list(source: &str) -> Vec<Value> {
    match run(source).expect("runs") {
        Value::List(items) => (*items).clone(),
        other => panic!("expected list, got {other:?}"),
    }
}

#[test]
fn null_literal_round_trips() {
    assert_eq!(
        run("task t() -> void { return null; }").unwrap(),
        Value::Null
    );
    // null is falsy and equals itself.
    assert_eq!(
        run("task t() -> void { return !null && null == null; }").unwrap(),
        Value::Bool(true)
    );
}

#[test]
fn multiplicative_operators_follow_school_arithmetic() {
    assert_eq!(
        run("task t() -> void { return 7 * 6; }").unwrap(),
        Value::Int(42)
    );
    // Integer division truncates toward zero, like the VM's Rust core.
    assert_eq!(
        run("task t() -> void { return 7 / 2; }").unwrap(),
        Value::Int(3)
    );
    assert_eq!(
        run("task t() -> void { return 0 - 7 / 2; }").unwrap(),
        Value::Int(-3)
    );
    assert_eq!(
        run("task t() -> void { return 7 % 3; }").unwrap(),
        Value::Int(1)
    );
    // Mixed int/float promotes; * / % bind tighter than + -.
    assert_eq!(
        run("task t() -> void { return 7.0 / 2; }").unwrap(),
        Value::Float(3.5)
    );
    assert_eq!(
        run("task t() -> void { return 1 + 2 * 3; }").unwrap(),
        Value::Int(7)
    );
    assert_eq!(
        run("task t() -> void { return (1 + 2) * 3; }").unwrap(),
        Value::Int(9)
    );
}

#[test]
fn division_by_zero_and_overflow_are_data_errors() {
    assert_eq!(
        run("task t() -> void { return 1 / 0; }").unwrap_err(),
        EngineError::Vm(VmError::DivisionByZero)
    );
    assert_eq!(
        run("task t() -> void { return 1 % 0; }").unwrap_err(),
        EngineError::Vm(VmError::DivisionByZero)
    );
    // i64::MIN / -1 overflows the result range: an error, never a panic.
    assert_eq!(
        run("task t() -> void { return (0 - 9223372036854775807 - 1) / (0 - 1); }").unwrap_err(),
        EngineError::Vm(VmError::Overflow)
    );
    assert_eq!(
        run("task t() -> void { return 9223372036854775807 * 2; }").unwrap_err(),
        EngineError::Vm(VmError::Overflow)
    );
}

#[test]
fn unary_minus_negates_numbers_only() {
    assert_eq!(
        run("task t() -> void { return -5; }").unwrap(),
        Value::Int(-5)
    );
    assert_eq!(
        run("task t() -> void { let x = 3; return -x * 2; }").unwrap(),
        Value::Int(-6)
    );
    assert_eq!(
        run("task t() -> void { return -1.5; }").unwrap(),
        Value::Float(-1.5)
    );
    // Negating i64::MIN overflows.
    assert_eq!(
        run("task t() -> void { return -(0 - 9223372036854775807 - 1); }").unwrap_err(),
        EngineError::Vm(VmError::Overflow)
    );
    // Negating a non-number is a type error, not a panic.
    assert!(run("task t() -> void { return -\"x\"; }").is_err());
}

#[test]
fn assignment_updates_existing_locals_only() {
    assert_eq!(
        run("task t() -> void { let x = 1; x = x + 1; x = x * 10; return x; }").unwrap(),
        Value::Int(20)
    );
    // Assigning to an undeclared name is a compile-time error (a typo, not
    // a declaration) -- `let` remains the only way to introduce a name.
    let err = run("task t() -> void { x = 1; }").unwrap_err();
    assert!(matches!(err, EngineError::Compile(_)));
}

#[test]
fn while_loops_iterate_until_the_condition_fails() {
    let source = r#"
        task t() -> void {
            let i = 0;
            let sum = 0;
            while i < 5 {
                sum = sum + i;
                i = i + 1;
            }
            return sum;
        }
    "#;
    assert_eq!(run(source).unwrap(), Value::Int(10));
}

#[test]
fn a_hostile_infinite_while_hits_the_budget_not_a_hang() {
    let source = "task t() -> void { while true { } }";
    let task = cortex_engine::parser::parse_task(source).expect("parses");
    let program = cortex_engine::compiler::compile(&task).expect("compiles");
    let registry = NativeRegistry::standard();
    let mut env = MemoryNative::default();
    let mut vm = Vm::new(program, &mut env, &registry).with_budget(1_000);
    assert_eq!(vm.run().unwrap_err(), VmError::BudgetExhausted);
}

#[test]
fn list_and_map_literals_build_real_containers() {
    assert_eq!(
        run("task t() -> void { return [1, 2, 3][1]; }").unwrap(),
        Value::Int(2)
    );
    assert_eq!(
        run("task t() -> void { return {\"a\": 1, \"b\": 2}[\"b\"]; }").unwrap(),
        Value::Int(2)
    );
    // Nested composition, member and index access together.
    assert_eq!(
        run(
            "task t() -> void { let data = {\"rows\": [{\"id\": 7}, {\"id\": 9}]}; return data.rows[1].id; }"
        )
        .unwrap(),
        Value::Int(9)
    );
    // Duplicate keys keep the last value, like JSON object parsing.
    assert_eq!(
        run("task t() -> void { return {\"k\": 1, \"k\": 2}[\"k\"]; }").unwrap(),
        Value::Int(2)
    );
    // Empty containers work.
    assert_eq!(
        run_list("task t() -> void { return []; }"),
        Vec::<Value>::new()
    );
}

#[test]
fn map_literal_keys_must_be_strings() {
    let err = run("task t() -> void { return {1: \"x\"}[\"1\"]; }").unwrap_err();
    assert!(
        matches!(err, EngineError::Vm(VmError::TypeMismatch { .. })),
        "got: {err:?}"
    );
}

#[test]
fn list_iteration_and_len_work_on_literals() {
    let source = r#"
        task t() -> void {
            let total = 0;
            for price in [2, 4, 6] {
                total = total + price;
            }
            return total;
        }
    "#;
    assert_eq!(run(source).unwrap(), Value::Int(12));
}

#[test]
fn functions_compute_recursively() {
    let source = r#"
        task t() -> void {
            fn fib(n) {
                if n < 2 {
                    return n;
                }
                return fib(n - 1) + fib(n - 2);
            }
            return fib(10);
        }
    "#;
    assert_eq!(run(source).unwrap(), Value::Int(55));
}

#[test]
fn functions_resolve_in_declaration_order_and_mutually() {
    let source = r#"
        task t() -> void {
            return isEven(10) && isOdd(7);
            fn isEven(n) {
                if n == 0 { return true; }
                return isOdd(n - 1);
            }
            fn isOdd(n) {
                if n == 0 { return false; }
                return isEven(n - 1);
            }
        }
    "#;
    assert_eq!(run(source).unwrap(), Value::Bool(true));
}

#[test]
fn function_locals_are_isolated_from_the_caller() {
    let source = r#"
        task t() -> void {
            let x = 1;
            fn bump(x) {
                x = x + 100;
                return x;
            }
            let y = bump(5);
            return [x, y];
        }
    "#;
    assert_eq!(run_list(source), vec![Value::Int(1), Value::Int(105)]);
}

#[test]
fn functions_implicitly_return_null_and_support_early_exit() {
    let source = r#"
        task t() -> void {
            fn findPositive(list) {
                for item in list {
                    if item > 0 {
                        return item;
                    }
                }
            }
            let nothing = findPositive([0, -1]);
            let found = findPositive([0, 3, 5]);
            return [nothing, found];
        }
    "#;
    assert_eq!(run_list(source), vec![Value::Null, Value::Int(3)]);
}

#[test]
fn functions_can_call_natives() {
    let source = r#"
        task t() -> void {
            fn offline() {
                return !native.net.isConnected();
            }
            if offline() {
                return "queued";
            }
            return "sent";
        }
    "#;
    // MemoryNative defaults to connected = false.
    assert_eq!(run(source).unwrap(), Value::Str("queued".into()));
}

#[test]
fn deep_recursion_is_a_bounded_error_not_a_crash() {
    let source = r#"
        task t() -> void {
            fn f(n) {
                return f(n);
            }
            return f(1);
        }
    "#;
    assert_eq!(
        run(source).unwrap_err(),
        EngineError::Vm(VmError::CallDepthExhausted)
    );
}

#[test]
fn function_misuse_is_rejected_at_compile_time() {
    // Unknown function.
    let err = run("task t() -> void { return nope(1); }").unwrap_err();
    assert!(matches!(err, EngineError::Compile(_)), "got: {err:?}");
    // Arity mismatch.
    let err = run("task t() -> void { fn f(a) { return a; } return f(1, 2); }").unwrap_err();
    assert!(
        matches!(err, EngineError::Compile(_)),
        "arity must be a compile error, got: {err:?}"
    );
    // Duplicate function names.
    let err = run("task t() -> void { fn f() { } fn f() { } return 1; }").unwrap_err();
    assert!(matches!(err, EngineError::Compile(_)), "got: {err:?}");
    // Duplicate parameter names.
    let err = run("task t() -> void { fn f(a, a) { return a; } return f(1, 2); }").unwrap_err();
    assert!(matches!(err, EngineError::Compile(_)), "got: {err:?}");
    // Nested functions are not part of the language.
    let err = run("task t() -> void { fn outer() { fn inner() { } } return 1; }").unwrap_err();
    assert!(matches!(err, EngineError::Parse(_)), "got: {err:?}");
}

#[test]
fn outbox_entries_is_a_first_class_native() {
    let source = r#"
        task t() -> void {
            let pending = native.outbox.entries();
            let count = 0;
            for item in pending {
                count = count + 1;
            }
            return count;
        }
    "#;
    // The wasm playground and the daemon host both answer outbox.entries;
    // through MemoryNative it is its own row set (empty here).
    assert_eq!(run(source).unwrap(), Value::Int(0));
}
