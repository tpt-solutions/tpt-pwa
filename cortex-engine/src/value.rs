// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.

//! Runtime values. `Map` backs row objects returned by `native.db.query`,
//! which is what makes `item.id` member access work in task scripts.
//!
//! Lists and maps are reference-counted: the DSL has no assignment or
//! mutation, so sharing is invisible, and it removes the old quadratic
//! behaviour where every `LoadLocal` of a loop's iterator cloned the whole
//! vector (an O(n²) cost the instruction budget never counted).

use std::collections::BTreeMap;
use std::fmt;
use std::rc::Rc;

#[derive(Clone, Debug, PartialEq)]
pub enum Value {
    Null,
    Bool(bool),
    Int(i64),
    Float(f64),
    Str(String),
    List(Rc<Vec<Value>>),
    Map(Rc<BTreeMap<String, Value>>),
}

impl Value {
    pub fn truthy(&self) -> bool {
        match self {
            Value::Null => false,
            Value::Bool(b) => *b,
            Value::Int(i) => *i != 0,
            Value::Float(f) => *f != 0.0,
            Value::Str(s) => !s.is_empty(),
            Value::List(_) | Value::Map(_) => true,
        }
    }

    pub fn type_name(&self) -> &'static str {
        match self {
            Value::Null => "null",
            Value::Bool(_) => "bool",
            Value::Int(_) => "int",
            Value::Float(_) => "float",
            Value::Str(_) => "string",
            Value::List(_) => "list",
            Value::Map(_) => "map",
        }
    }

    /// `i` as a float only when the conversion is lossless enough to compare
    /// exactly; `None` means "compare through i64 instead" (|i| too large
    /// for f64's 53-bit mantissa would silently round otherwise).
    fn int_as_exact_float(i: i64) -> Option<f64> {
        let f = i as f64;
        if f as i64 == i {
            Some(f)
        } else {
            None
        }
    }

    /// Total ordering across int/float: NaN never compares (error upstream),
    /// mixed int/float compares exactly (no silent f64 rounding of large
    /// i64s), everything else delegates to the native ordering.
    pub fn total_cmp(&self, other: &Value) -> Option<std::cmp::Ordering> {
        match (self, other) {
            (Value::Int(a), Value::Int(b)) => Some(a.cmp(b)),
            (Value::Str(a), Value::Str(b)) => Some(a.cmp(b)),
            (Value::Float(a), Value::Float(b)) => a.partial_cmp(b),
            (Value::Int(a), Value::Float(b)) => {
                if b.is_nan() {
                    None
                } else {
                    match Value::int_as_exact_float(*a) {
                        Some(exact) => exact.partial_cmp(b),
                        // |a| beyond f64 precision: the sign of (a - b) is
                        // decided by whether b sits inside i64's range.
                        None => Some(cmp_big_int_float(*a, *b)),
                    }
                }
            }
            (Value::Float(a), Value::Int(b)) => {
                if a.is_nan() {
                    None
                } else {
                    match Value::int_as_exact_float(*b) {
                        Some(exact) => a.partial_cmp(&exact),
                        None => Some(cmp_big_int_float(*b, *a).reverse()),
                    }
                }
            }
            _ => None,
        }
    }
}

/// Ordering of an i64 magnitude so large that f64 cannot represent it
/// exactly against an arbitrary f64. At that magnitude |i| > 2^63 > f64 max
/// is impossible, but rounding IS: only the sign of the difference matters
/// and `i` has no fractional part, so compare through f64 with the rounding
/// direction taken into account by a strict threshold check.
fn cmp_big_int_float(i: i64, f: f64) -> std::cmp::Ordering {
    // i is beyond f64's exact-integer range: every f64 with |f| < 2^63 is
    // strictly between two representable i64s, so plain comparison against
    // the clamped bounds decides it.
    if f < i64::MIN as f64 {
        std::cmp::Ordering::Greater
    } else if f > i64::MAX as f64 {
        std::cmp::Ordering::Less
    } else {
        // f is an exact integer within i64 range.
        let fi = f as i64;
        i.cmp(&fi)
    }
}

impl fmt::Display for Value {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Value::Null => write!(f, "null"),
            Value::Bool(b) => write!(f, "{b}"),
            Value::Int(i) => write!(f, "{i}"),
            Value::Float(x) => write!(f, "{x}"),
            Value::Str(s) => write!(f, "{s}"),
            Value::List(items) => {
                write!(f, "[")?;
                for (i, item) in items.iter().enumerate() {
                    if i > 0 {
                        write!(f, ", ")?;
                    }
                    write!(f, "{item}")?;
                }
                write!(f, "]")
            }
            Value::Map(_) => write!(f, "map"),
        }
    }
}
