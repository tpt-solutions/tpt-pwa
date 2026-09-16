// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! Safety tests backing the Phase 4 guarantee (docs/formal-verification.md):
//! adversarial bytecode and hostile scripts must produce *errors*, never
//! panics. These run with `cargo test` and are the executable subset of the
//! verification story; model checking (Kani) is the planned next step.

use cortex_engine::bytecode::{Instr, NativeId, Program};
use cortex_engine::natives::{MemoryNative, NativeRegistry};
use cortex_engine::value::Value;
use cortex_engine::Vm;

/// Deterministic xorshift64* so failures are reproducible.
struct Rng(u64);

impl Rng {
    fn next(&mut self) -> u64 {
        let mut x = self.0;
        x ^= x >> 12;
        x ^= x << 25;
        x ^= x >> 27;
        self.0 = x;
        x.wrapping_mul(0x2545F4914F6CDD1D)
    }

    fn below(&mut self, n: u64) -> u64 {
        self.next() % n
    }
}

fn adversarial_programs(count: usize, max_instrs: usize) -> Vec<Program> {
    let native_ids = [
        NativeId::DbQuery,
        NativeId::DbExec,
        NativeId::NetIsConnected,
        NativeId::HttpPost,
    ];
    let instr_pool = [
        Instr::Const(0),
        Instr::Const(9), // out-of-range constant
        Instr::LoadLocal(0),
        Instr::LoadLocal(99), // out-of-range local
        Instr::StoreLocal(0),
        Instr::StoreLocal(99),
        Instr::Pop,
        Instr::Add,
        Instr::Sub,
        Instr::Not,
        Instr::Eq,
        Instr::Less,
        Instr::MemberGet,
        Instr::ListLen,
        Instr::ListGet,
        Instr::JumpIfFalse(1),
        Instr::Return,
    ];
    let mut rng = Rng(0x9E3779B97F4A7C15);
    (0..count)
        .map(|_| {
            let len = rng.below(max_instrs as u64) as usize;
            let code = (0..len)
                .map(|_| match rng.below(4) {
                    0 => Instr::CallNative {
                        native: native_ids[rng.below(4) as usize],
                        argc: rng.below(5) as u8,
                    },
                    1 => Instr::Jump(rng.below(len as u64 + 1) as u16),
                    2 => Instr::JumpIfFalse(rng.below(len as u64 + 1) as u16),
                    _ => instr_pool[rng.below(instr_pool.len() as u64) as usize].clone(),
                })
                .collect();
            Program {
                constants: vec![Value::Int(1), Value::Str("s".into()), Value::Bool(true)],
                code,
                locals: 1,
            }
        })
        .collect()
}

#[test]
fn adversarial_bytecode_never_panics() {
    let registry = NativeRegistry::standard();
    for program in adversarial_programs(2_000, 64) {
        let mut env = MemoryNative::default();
        let mut vm = Vm::new(program.clone(), &mut env, &registry).with_budget(10_000);
        // The only contract: this returns. Ok or Err are both fine.
        let _ = vm.run();
    }
}

#[test]
fn runaway_loop_hits_budget_instead_of_hanging() {
    let program = Program {
        constants: vec![Value::Bool(true)],
        code: vec![
            Instr::Const(0),
            Instr::JumpIfFalse(3),
            Instr::Jump(0),
            Instr::Return,
        ],
        locals: 0,
    };
    let mut env = MemoryNative::default();
    let registry = NativeRegistry::standard();
    let mut vm = Vm::new(program, &mut env, &registry).with_budget(1_000);
    let err = vm.run().expect_err("runaway loop must be stopped");
    assert_eq!(err, cortex_engine::VmError::BudgetExhausted);
}

#[test]
fn stack_is_bounded_by_budget() {
    // Endless pushes: Const, Const, Const, ... -> must fail, not OOM.
    let code: Vec<Instr> = (0..200_000).map(|_| Instr::Const(0)).collect();
    let program = Program {
        constants: vec![Value::Int(1)],
        code,
        locals: 0,
    };
    let mut env = MemoryNative::default();
    let registry = NativeRegistry::standard();
    let mut vm = Vm::new(program, &mut env, &registry);
    let err = vm.run().expect_err("unbalanced program must fail");
    assert_eq!(
        err,
        cortex_engine::VmError::BadProgram("fell off the end without Return")
    );
}

#[test]
fn hostile_native_implementations_return_errors_not_panics() {
    struct FailingNative;
    impl cortex_engine::natives::NativeEnv for FailingNative {
        fn db_query(&mut self, _: &str, _: &[Value]) -> Result<Vec<Value>, String> {
            Err("db unavailable".into())
        }
        fn db_exec(&mut self, _: &str, _: &[Value]) -> Result<Value, String> {
            Err("db unavailable".into())
        }
        fn net_is_connected(&mut self) -> Result<bool, String> {
            Err("netd unreachable".into())
        }
        fn http_post(&mut self, _: &str, _: &Value) -> Result<Value, String> {
            Err("tls handshake failed".into())
        }
    }
    let source = r#"
        task allFail() -> void {
            if native.net.isConnected() {
                let rows = native.db.query("SELECT 1");
            }
        }
    "#;
    let task = cortex_engine::parser::parse_task(source).expect("parses");
    let program = cortex_engine::compiler::compile(&task).expect("compiles");
    let registry = NativeRegistry::standard();
    let mut env = FailingNative;
    let mut vm = Vm::new(program, &mut env, &registry);
    let err = vm
        .run()
        .expect_err("native failure must surface as VmError");
    assert!(matches!(err, cortex_engine::VmError::Native(_)));
}
