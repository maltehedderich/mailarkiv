//! What the program writes besides its answers. Panic messages and library
//! log messages can quote mail or search words, so neither reaches the
//! server: a panic writes only its location on standard error, and a
//! library warning or error becomes a log frame with its level and target.

use std::sync::mpsc::Sender;

use log::{Level, LevelFilter, Metadata, Record};

use crate::protocol::{Log, LogLevel, Outgoing};

/// Replaces the panic message with one line the server matches:
/// `mailarkiv-search panic at <file>:<line>`. The abort follows, because
/// the build sets `panic = "abort"`.
pub fn catch_panics() {
    std::panic::set_hook(Box::new(|info| match info.location() {
        Some(location) => eprintln!(
            "mailarkiv-search panic at {}:{}",
            location.file(),
            location.line()
        ),
        None => eprintln!("mailarkiv-search panic"),
    }));
}

/// Sends library warnings and errors to frames. Lower levels are dropped
/// before they're formatted.
pub fn forward_logs(frames: Sender<Outgoing>) {
    // The logger lives as long as the process, as the log crate requires.
    if log::set_logger(Box::leak(Box::new(FrameLogger { frames }))).is_ok() {
        log::set_max_level(LevelFilter::Warn);
    }
}

struct FrameLogger {
    frames: Sender<Outgoing>,
}

impl log::Log for FrameLogger {
    fn enabled(&self, metadata: &Metadata) -> bool {
        metadata.level() <= Level::Warn
    }

    fn log(&self, record: &Record) {
        let level = match record.level() {
            Level::Error => LogLevel::Error,
            Level::Warn => LogLevel::Warn,
            _ => return,
        };
        // A failed send means the writer thread is gone with the server.
        let _ = self.frames.send(Outgoing::Log(Log {
            kind: "log",
            level,
            target: target(record.target()),
        }));
    }

    fn flush(&self) {}
}

/// The record's target when it's a module path, as it is unless a caller
/// set another. Anything else could be text, so it becomes "unknown".
fn target(target: &str) -> String {
    let module_path = !target.is_empty()
        && target.len() <= 128
        && target
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '_' || c == ':');
    if module_path { target } else { "unknown" }.to_string()
}

#[cfg(test)]
mod tests {
    use super::target;

    #[test]
    fn target_keeps_only_module_paths() {
        assert_eq!(
            target("tantivy::directory::managed_directory"),
            "tantivy::directory::managed_directory"
        );
        assert_eq!(target("Quartalsbericht Müller"), "unknown");
        assert_eq!(target(""), "unknown");
        assert_eq!(target(&"a".repeat(129)), "unknown");
    }
}
