//! Fault points for tests. Builds with the `faults` feature read
//! `MAILARKIV_SEARCH_FAULTS`, a comma-separated list of `point=action`.
//! Other builds have no faults, and `at` is always false.

/// Where a fault can happen.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Point {
    /// After `commit` returns, before the answer is sent.
    AfterCommit,
    /// After the query is built, before collection.
    DuringSearch,
    /// Inside a maintenance run, before the first merge.
    DuringMerge,
    /// Before an index opens.
    OnOpen,
}

#[cfg(not(feature = "faults"))]
pub fn init() -> Result<(), ()> {
    Ok(())
}

/// Whether the operation at point must answer its failure code.
#[cfg(not(feature = "faults"))]
#[inline(always)]
pub fn at(_point: Point) -> bool {
    false
}

#[cfg(feature = "faults")]
pub use enabled::{at, init};

#[cfg(feature = "faults")]
mod enabled {
    use std::sync::OnceLock;

    use super::Point;

    #[derive(Clone, Copy, PartialEq, Eq)]
    enum Action {
        /// Ends the process as a crash does.
        Exit,
        /// The operation answers its failure code.
        Fail,
        /// Blocks for good, so the server's time limit fires.
        Hang,
        /// Panics, which aborts the process after the panic line.
        Panic,
    }

    static FAULTS: OnceLock<Vec<(Point, Action)>> = OnceLock::new();

    /// Reads the fault list. A mistake in it stops the process at startup,
    /// because a fault that never fires would let a test pass without
    /// testing anything.
    pub fn init() -> Result<(), ()> {
        let setting = std::env::var("MAILARKIV_SEARCH_FAULTS").unwrap_or_default();
        let faults = parse(&setting)?;
        FAULTS.set(faults).map_err(|_| ())
    }

    fn parse(setting: &str) -> Result<Vec<(Point, Action)>, ()> {
        let mut faults = Vec::new();
        if setting.is_empty() {
            return Ok(faults);
        }
        for entry in setting.split(',') {
            let (point, action) = entry.split_once('=').ok_or(())?;
            let point = match point {
                "after-commit" => Point::AfterCommit,
                "during-search" => Point::DuringSearch,
                "during-merge" => Point::DuringMerge,
                "on-open" => Point::OnOpen,
                _ => return Err(()),
            };
            let action = match action {
                "exit" => Action::Exit,
                "fail" => Action::Fail,
                "hang" => Action::Hang,
                "panic" => Action::Panic,
                _ => return Err(()),
            };
            if faults.iter().any(|&(known, _)| known == point) {
                return Err(());
            }
            faults.push((point, action));
        }
        Ok(faults)
    }

    /// Does what the fault list says for point. True means the operation
    /// must answer its failure code.
    pub fn at(point: Point) -> bool {
        let action = FAULTS
            .get()
            .and_then(|faults| faults.iter().find(|&&(known, _)| known == point))
            .map(|&(_, action)| action);
        match action {
            None => false,
            Some(Action::Fail) => true,
            Some(Action::Exit) => std::process::abort(),
            Some(Action::Hang) => loop {
                std::thread::park();
            },
            Some(Action::Panic) => panic!("fault at {point:?}"),
        }
    }
}
