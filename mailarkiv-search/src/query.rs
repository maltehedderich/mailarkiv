//! Turning a search request into a query and running it.

use std::ops::Bound;

use tantivy::collector::sort_key::SortByStaticFastValue;
use tantivy::collector::{Count, TopDocs};
use tantivy::query::{BooleanQuery, Query, RangeQuery};
use tantivy::schema::Field;
use tantivy::{Order, Term};

use crate::analysis;
use crate::index::OpenIndex;
use crate::protocol::{Done, Search};
use crate::schema::{FIRST_ARCHIVED_AT, ID, LIST_DATE};

/// Runs search on index. Every word must match.
pub fn search(index: &OpenIndex, search: &Search) -> Done {
    let words = analysis::words(&search.words);
    if words.is_empty() {
        return Done::NoTerms { no_terms: true };
    }
    let query = BooleanQuery::intersection(
        words
            .iter()
            .map(|word| starts_with(index.fields.all, word))
            .collect(),
    );
    // Newest first, by list date, then archive time, then ID.
    let order = TopDocs::with_limit(search.limit as usize)
        .and_offset(search.offset as usize)
        .order_by((
            (
                SortByStaticFastValue::<i64>::for_field(LIST_DATE),
                Order::Desc,
            ),
            (
                SortByStaticFastValue::<i64>::for_field(FIRST_ARCHIVED_AT),
                Order::Desc,
            ),
            (SortByStaticFastValue::<u64>::for_field(ID), Order::Desc),
        ));
    let searcher = index.reader.searcher();
    // An index that can't be read ends the process with this fixed text
    // rather than Tantivy's, which can quote words. The server restarts it
    // and counts the crash.
    let Ok((total, hits)) = searcher.search(&query, &(Count, order)) else {
        panic!("reading the index failed");
    };
    Done::Found {
        total: total as u64,
        ids: hits
            .into_iter()
            .map(|((_, _, id), _)| id.expect("every document has an ID"))
            .collect(),
    }
}

/// Matches every term in field that starts with word. A range has no limit
/// on how many terms it matches, unlike Tantivy's prefix queries, which
/// stop at 50.
fn starts_with(field: Field, word: &str) -> Box<dyn Query> {
    let mut after = Term::from_field_text(field, "");
    after.set_bytes(&after_prefix(word));
    Box::new(RangeQuery::new(
        Bound::Included(Term::from_field_text(field, word)),
        Bound::Excluded(after),
    ))
}

/// The first byte string after every string that starts with prefix: its
/// bytes with the last raised by one. The last byte of UTF-8 is at most
/// 0xBF, so it can't overflow.
fn after_prefix(prefix: &str) -> Vec<u8> {
    let mut bytes = prefix.as_bytes().to_vec();
    *bytes.last_mut().expect("words aren't empty") += 1;
    bytes
}

#[cfg(test)]
mod tests {
    use super::after_prefix;

    #[test]
    fn after_prefix_raises_the_last_byte() {
        assert_eq!(after_prefix("quartals"), b"quartalt");
        assert_eq!(after_prefix("mü"), [b'm', 0xC3, 0xBD]);
        assert_eq!(after_prefix("\u{10FFFF}"), [0xF4, 0x8F, 0xBF, 0xC0]);
    }
}
