mod util;

use util::{clamp, double};

struct Counter {
    value: i32,
}

impl Counter {
    fn new() -> Counter {
        Counter { value: 0 }
    }

    fn bump(&mut self) {
        self.value = clamp(double(self.value) + 1, 0, 100);
    }
}

fn run_all(times: i32) -> i32 {
    let mut c = Counter::new();
    for _ in 0..times {
        c.bump();
    }
    c.value
}

fn main() {
    println!("{}", run_all(5));
}
