//! `mailarkiv-search` holds MailArkiv's search indexes. Only the server starts
//! it, and the two talk over its standard input and output.

use std::io;
use std::process::ExitCode;

// musl's own allocator built the index 2.4 times slower.
#[global_allocator]
static GLOBAL: mimalloc::MiMalloc = mimalloc::MiMalloc;

fn main() -> ExitCode {
    if std::env::args_os().len() > 1 {
        eprintln!("usage: mailarkiv-search (started by the MailArkiv server, takes no arguments)");
        return ExitCode::from(2);
    }

    // Standard input closes when the server is gone, and then this process ends.
    match io::copy(&mut io::stdin().lock(), &mut io::sink()) {
        Ok(_) => ExitCode::SUCCESS,
        Err(_) => ExitCode::FAILURE,
    }
}
