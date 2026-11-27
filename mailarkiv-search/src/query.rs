//! Turning a search request into a query and running it.

use std::ops::Bound;
use std::time::Instant;

use tantivy::collector::sort_key::SortByStaticFastValue;
use tantivy::collector::{Count, TopDocs};
use tantivy::query::{BooleanQuery, Query, RangeQuery, TermQuery};
use tantivy::schema::{Field, IndexRecordOption};
use tantivy::{Order, Term};

use crate::analysis;
use crate::collector::Deadline;
use crate::faults::{self, Point};
use crate::index::OpenIndex;
use crate::protocol::{Done, ErrorCode, Search};
use crate::schema::{FIRST_ARCHIVED_AT, Fields, ID, LIST_DATE};

/// Runs search on index, unless deadline passes first. Every word and every
/// filter must match.
pub fn search(index: &OpenIndex, search: &Search, deadline: Instant) -> Result<Done, ErrorCode> {
    let parts = parts(&index.fields, search);
    if parts.is_empty() {
        return Ok(Done::NoTerms { no_terms: true });
    }
    let query = BooleanQuery::intersection(parts);
    if faults::at(Point::DuringSearch) {
        return Err(ErrorCode::TimedOut);
    }
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
    let Ok(found) = searcher.search(&query, &Deadline::new((Count, order), deadline)) else {
        panic!("reading the index failed");
    };
    let (total, hits) = found.ok_or(ErrorCode::TimedOut)?;
    Ok(Done::Found {
        total: total as u64,
        ids: hits
            .into_iter()
            .map(|((_, _, id), _)| id.expect("every document has an ID"))
            .collect(),
    })
}

/// The queries that must all match. Empty when the words give no terms and
/// every filter is empty.
fn parts(fields: &Fields, search: &Search) -> Vec<Box<dyn Query>> {
    let mut parts = words(fields.all, &search.words);
    parts.extend(mailbox(
        [fields.from, fields.from_address, fields.from_domain],
        &search.from,
    ));
    parts.extend(mailbox(
        [fields.to, fields.to_address, fields.to_domain],
        &search.to,
    ));
    parts.extend(words(fields.subject, &search.subject));
    parts.extend(words(fields.attachment, &search.attachment));
    if search.list_date_from.is_some() || search.list_date_before.is_some() {
        let at = |ms| Term::from_field_i64(fields.list_date, ms);
        parts.push(Box::new(RangeQuery::new(
            search
                .list_date_from
                .map_or(Bound::Unbounded, |ms| Bound::Included(at(ms))),
            search
                .list_date_before
                .map_or(Bound::Unbounded, |ms| Bound::Excluded(at(ms))),
        )));
    }
    if search.has_attachments {
        let yes = || Bound::Included(Term::from_field_u64(fields.has_attachments, 1));
        parts.push(Box::new(RangeQuery::new(yes(), yes())));
    }
    parts
}

/// One query per word of text in field.
fn words(field: Field, text: &str) -> Vec<Box<dyn Query>> {
    analysis::words(text)
        .iter()
        .map(|word| {
            // A one-character word would match too much as a prefix.
            if word.chars().count() == 1 {
                exact(field, word)
            } else {
                starts_with(field, word)
            }
        })
        .collect()
}

/// What a From or To value matches. A value with an `@` and text on both
/// sides is a complete address. A value that starts with `@` or contains a
/// dot is a domain, matched whole, so `example.com` doesn't find
/// `attacker-example.com`. A value with spaces, or anything else, is words.
fn mailbox([words_field, address, domain]: [Field; 3], value: &str) -> Vec<Box<dyn Query>> {
    let value = value.trim();
    if !value.contains(char::is_whitespace) && value.contains(char::is_alphanumeric) {
        if let Some((local, after)) = value.rsplit_once('@')
            && !local.is_empty()
            && !after.is_empty()
        {
            return vec![exact(address, &analysis::whole(value))];
        }
        if let Some(after) = value.strip_prefix('@') {
            return vec![exact(domain, &analysis::whole(after))];
        }
        if value.contains('.') {
            return vec![exact(domain, &analysis::whole(value))];
        }
    }
    words(words_field, value)
}

/// Matches the one term in field, which must already be analyzed.
fn exact(field: Field, term: &str) -> Box<dyn Query> {
    Box::new(TermQuery::new(
        Term::from_field_text(field, term),
        IndexRecordOption::Basic,
    ))
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
