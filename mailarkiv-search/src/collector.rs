//! A collector that stops a search at its deadline. Tantivy has no query
//! time limit, and its own segment loop can't be stopped from inside, so
//! this one drives each segment's scorer itself.

use std::time::Instant;

use tantivy::collector::{Collector, SegmentCollector};
use tantivy::query::Weight;
use tantivy::schema::Schema;
use tantivy::{COLLECT_BLOCK_BUFFER_LEN, DocId, DocSet, SegmentOrdinal, SegmentReader, TERMINATED};

/// How many documents pass between two looks at the clock. A multiple of
/// the block length.
const CHECK_EVERY: usize = 4_096;

/// Collects with inner until the deadline. Its fruit is None when the
/// deadline passed first.
pub struct Deadline<C, K = fn() -> Instant> {
    inner: C,
    at: Instant,
    now: K,
}

impl<C> Deadline<C> {
    pub fn new(inner: C, at: Instant) -> Deadline<C> {
        Deadline {
            inner,
            at,
            now: Instant::now,
        }
    }
}

impl<C, K> Deadline<C, K>
where
    K: Fn() -> Instant,
{
    fn passed(&self) -> bool {
        (self.now)() >= self.at
    }
}

impl<C, K> Collector for Deadline<C, K>
where
    C: Collector,
    K: Fn() -> Instant + Send + Sync,
{
    type Fruit = Option<C::Fruit>;
    type Child = Option<C::Child>;

    fn check_schema(&self, schema: &Schema) -> tantivy::Result<()> {
        self.inner.check_schema(schema)
    }

    fn for_segment(
        &self,
        segment_local_id: SegmentOrdinal,
        segment: &SegmentReader,
    ) -> tantivy::Result<Self::Child> {
        self.inner.for_segment(segment_local_id, segment).map(Some)
    }

    fn requires_scoring(&self) -> bool {
        self.inner.requires_scoring()
    }

    fn merge_fruits(
        &self,
        segment_fruits: Vec<Option<<C::Child as SegmentCollector>::Fruit>>,
    ) -> tantivy::Result<Self::Fruit> {
        let Some(fruits) = segment_fruits.into_iter().collect::<Option<Vec<_>>>() else {
            return Ok(None);
        };
        self.inner.merge_fruits(fruits).map(Some)
    }

    fn collect_segment(
        &self,
        weight: &dyn Weight,
        segment_ord: u32,
        reader: &SegmentReader,
    ) -> tantivy::Result<Option<<C::Child as SegmentCollector>::Fruit>> {
        if self.passed() {
            return Ok(None);
        }
        let mut child = self.inner.for_segment(segment_ord, reader)?;
        let mut scorer = weight.scorer(reader, 1.0)?;
        let scoring = self.inner.requires_scoring();
        let alive = reader.alive_bitset();
        let mut buffer = [0; COLLECT_BLOCK_BUFFER_LEN];
        let mut since_check = 0;
        loop {
            let filled = if scoring {
                // One document at a time, so each gets its score.
                let doc = scorer.doc();
                if doc == TERMINATED {
                    0
                } else {
                    if alive.is_none_or(|alive| alive.is_alive(doc)) {
                        child.collect(doc, scorer.score());
                    }
                    scorer.advance();
                    1
                }
            } else {
                let filled = scorer.fill_buffer(&mut buffer);
                collect_block(&mut child, &buffer[..filled], alive);
                filled
            };
            if filled == 0 {
                return Ok(Some(child.harvest()));
            }
            since_check += filled;
            if since_check >= CHECK_EVERY {
                since_check = 0;
                if self.passed() {
                    return Ok(None);
                }
            }
        }
    }
}

fn collect_block(
    child: &mut impl SegmentCollector,
    docs: &[DocId],
    alive: Option<&tantivy::fastfield::AliveBitSet>,
) {
    match alive {
        None => child.collect_block(docs),
        Some(alive) => {
            for &doc in docs.iter().filter(|&&doc| alive.is_alive(doc)) {
                child.collect(doc, 0.0);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::time::{Duration, Instant};

    use tantivy::collector::{Collector, Count, SegmentCollector};
    use tantivy::query::AllQuery;
    use tantivy::{DocId, Index, Score, SegmentOrdinal, SegmentReader, TantivyDocument};

    use super::Deadline;
    use crate::{analysis, schema};

    /// Counts every document it's given, also when its segment is dropped.
    #[derive(Clone, Default)]
    struct Seen(Arc<AtomicUsize>);

    impl Collector for Seen {
        type Fruit = ();
        type Child = Seen;

        fn for_segment(&self, _: SegmentOrdinal, _: &SegmentReader) -> tantivy::Result<Seen> {
            Ok(self.clone())
        }

        fn requires_scoring(&self) -> bool {
            false
        }

        fn merge_fruits(&self, _: Vec<()>) -> tantivy::Result<()> {
            Ok(())
        }
    }

    impl SegmentCollector for Seen {
        type Fruit = ();

        fn collect(&mut self, _: DocId, _: Score) {
            self.0.fetch_add(1, Ordering::Relaxed);
        }

        fn harvest(self) {}
    }

    /// One segment of 10,000 documents.
    fn index() -> Index {
        let index = Index::create_in_ram(schema::schema());
        analysis::register(&index);
        let fields = schema::Fields::of(&index.schema());
        let mut writer = index.writer_with_num_threads(1, 50_000_000).unwrap();
        for id in 0..10_000 {
            let mut document = TantivyDocument::default();
            document.add_u64(fields.id, id);
            writer.add_document(document).unwrap();
        }
        writer.commit().unwrap();
        index
    }

    /// A clock at start for its first `fine` readings and an hour later
    /// after them.
    fn clock(start: Instant, fine: usize) -> impl Fn() -> Instant + Send + Sync {
        let readings = AtomicUsize::new(0);
        move || {
            if readings.fetch_add(1, Ordering::Relaxed) < fine {
                start
            } else {
                start + Duration::from_secs(3600)
            }
        }
    }

    /// How many documents a search over index saw before the clock's reading
    /// number fine + 1 stopped it, and whether it finished.
    fn run(index: &Index, fine: usize) -> (usize, bool) {
        let start = Instant::now();
        let seen = Seen::default();
        let collector = Deadline {
            inner: seen.clone(),
            at: start + Duration::from_secs(10),
            now: clock(start, fine),
        };
        let searcher = index.reader().unwrap().searcher();
        assert_eq!(searcher.segment_readers().len(), 1);
        let finished = searcher.search(&AllQuery, &collector).unwrap().is_some();
        (seen.0.load(Ordering::Relaxed), finished)
    }

    #[test]
    fn checks_before_the_segment_and_every_4096_documents() {
        let index = index();
        assert_eq!(run(&index, 0), (0, false));
        assert_eq!(run(&index, 1), (4_096, false));
        assert_eq!(run(&index, 2), (8_192, false));
        assert_eq!(run(&index, 3), (10_000, true));
    }

    #[test]
    fn passes_the_inner_fruit_through() {
        let index = index();
        let searcher = index.reader().unwrap().searcher();
        let collector = Deadline::new(Count, Instant::now() + Duration::from_secs(10));
        assert_eq!(
            searcher.search(&AllQuery, &collector).unwrap(),
            Some(10_000)
        );
    }
}
