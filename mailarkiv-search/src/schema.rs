//! The index schema. Changing it means a new SCHEMA_VERSION and a rebuild.

use tantivy::schema::{
    FAST, Field, INDEXED, IndexRecordOption, Schema, TextFieldIndexing, TextOptions,
};

use crate::analysis;

/// Stored beside each index. The search process refuses to open another.
pub const SCHEMA_VERSION: u32 = 1;

/// The schema's fields. Words fields record only which documents hold a
/// word. Nothing is stored: a search answers message IDs, read from `id`.
#[derive(Clone, Copy)]
pub struct Fields {
    /// Subject, sender and recipient names and addresses, both bodies, and
    /// attachment names, as words.
    pub all: Field,
    pub subject: Field,
    /// Sender names and addresses, as words.
    pub from: Field,
    /// To, Cc, and Bcc names and addresses, as words.
    pub to: Field,
    pub attachment: Field,
    /// Each complete sender address, lowercased.
    pub from_address: Field,
    pub to_address: Field,
    /// The part of each address after its last `@`, lowercased.
    pub from_domain: Field,
    pub to_domain: Field,
    /// The message ID, for deletes and the last sort key.
    pub id: Field,
    /// Unix milliseconds.
    pub list_date: Field,
    /// Unix milliseconds.
    pub first_archived_at: Field,
    /// 0 or 1.
    pub has_attachments: Field,
}

pub const LIST_DATE: &str = "list_date";
pub const FIRST_ARCHIVED_AT: &str = "first_archived_at";
pub const ID: &str = "id";

fn analyzed(tokenizer: &str) -> TextOptions {
    TextOptions::default().set_indexing_options(
        TextFieldIndexing::default()
            .set_tokenizer(tokenizer)
            .set_index_option(IndexRecordOption::Basic)
            .set_fieldnorms(false),
    )
}

pub fn schema() -> Schema {
    let mut builder = Schema::builder();
    for name in ["all", "subject", "from", "to", "attachment"] {
        builder.add_text_field(name, analyzed(analysis::WORDS));
    }
    for name in ["from_address", "to_address", "from_domain", "to_domain"] {
        builder.add_text_field(name, analyzed(analysis::WHOLE));
    }
    builder.add_u64_field(ID, INDEXED | FAST);
    builder.add_i64_field(LIST_DATE, FAST);
    builder.add_i64_field(FIRST_ARCHIVED_AT, FAST);
    builder.add_u64_field("has_attachments", FAST);
    builder.build()
}

impl Fields {
    /// The fields of schema, which must equal schema().
    pub fn of(schema: &Schema) -> Fields {
        let field = |name: &str| schema.get_field(name).expect("the schema has every field");
        Fields {
            all: field("all"),
            subject: field("subject"),
            from: field("from"),
            to: field("to"),
            attachment: field("attachment"),
            from_address: field("from_address"),
            to_address: field("to_address"),
            from_domain: field("from_domain"),
            to_domain: field("to_domain"),
            id: field(ID),
            list_date: field(LIST_DATE),
            first_archived_at: field(FIRST_ARCHIVED_AT),
            has_attachments: field("has_attachments"),
        }
    }
}
