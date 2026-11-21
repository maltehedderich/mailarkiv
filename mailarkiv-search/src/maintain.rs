//! Merging by request. Writers use `NoMergePolicy`, so merges run only when
//! the server asks, and each failure comes back in the answer instead of only
//! in Tantivy's log.
//!
//! An index's writer thread starts a run's merges, the one maintenance thread
//! waits for them, and the writer thread then collects garbage and answers.
//! Indexing goes on while merges run.

use std::sync::mpsc::{Receiver, Sender};

use tantivy::indexer::{LogMergePolicy, MergePolicy};
use tantivy::{FutureResult, Index, IndexWriter, SegmentMeta};

use crate::index::Job;
use crate::protocol::{Answer, Done, Outgoing};

/// A maintenance run whose merges have started.
pub struct Run {
    id: u64,
    merges: Vec<FutureResult<Option<SegmentMeta>>>,
    /// The writer thread that started the run.
    writer: Sender<Job>,
}

impl Run {
    pub fn new(
        id: u64,
        merges: Vec<FutureResult<Option<SegmentMeta>>>,
        writer: Sender<Job>,
    ) -> Run {
        Run { id, merges, writer }
    }
}

/// A run whose merges have ended.
pub struct Merged {
    pub id: u64,
    pub merges: u64,
    pub failed: u64,
}

pub fn answer(id: u64, merges: u64, failed: u64) -> Answer {
    Answer::ok(id, Done::Maintained { merges, failed })
}

/// Starts a merge for each candidate that `LogMergePolicy`'s defaults find
/// among the committed segments. A segment a failed merge left in place is a
/// candidate again in the next run.
pub fn start(
    index: &Index,
    writer: &mut IndexWriter,
) -> tantivy::Result<Vec<FutureResult<Option<SegmentMeta>>>> {
    let segments = index.searchable_segment_metas()?;
    Ok(LogMergePolicy::default()
        .compute_merge_candidates(&segments)
        .into_iter()
        .map(|candidate| writer.merge(&candidate.0))
        .collect())
}

/// The maintenance thread: waits for each run's merges, then sends the run
/// back to its writer thread.
pub fn run(runs: Receiver<Run>, answers: &Sender<Outgoing>) {
    for run in runs {
        let merges = run.merges.len() as u64;
        let failed = run
            .merges
            .into_iter()
            .map(FutureResult::wait)
            .filter(Result::is_err)
            .count() as u64;
        let merged = Merged {
            id: run.id,
            merges,
            failed,
        };
        if let Err(unsent) = run.writer.send(Job::CollectGarbage(merged)) {
            // The writer thread is gone, so nothing is left to collect.
            let Job::CollectGarbage(merged) = unsent.0 else {
                unreachable!("only CollectGarbage was sent")
            };
            if answers
                .send(answer(merged.id, merged.merges, merged.failed).into())
                .is_err()
            {
                return;
            }
        }
    }
}
