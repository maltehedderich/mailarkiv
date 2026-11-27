//! Requests and answers. Errors are closed codes, never Tantivy's text, which
//! can quote words from mail.

use serde::{Deserialize, Serialize};

/// The protocol's one version. Server and search process change together.
pub const PROTOCOL: u32 = 1;

/// The most documents one index request may carry.
pub const MAX_DOCUMENTS: usize = 500;

/// The deepest result a search may ask for: offset plus limit.
pub const MAX_RESULTS: u64 = 100_050;

#[derive(Deserialize)]
#[serde(tag = "op", rename_all = "snake_case")]
pub enum Request {
    Open {
        id: u64,
        generation: u64,
        create: bool,
    },
    Index {
        id: u64,
        generation: u64,
        documents: Vec<Document>,
        commit: bool,
    },
    Search(Search),
}

impl Request {
    pub fn id(&self) -> u64 {
        match self {
            Request::Open { id, .. } | Request::Index { id, .. } => *id,
            Request::Search(search) => search.id,
        }
    }

    /// Whether the request keeps the rules a request's frame must keep.
    pub fn is_valid(&self) -> bool {
        let rules = match self {
            Request::Open { .. } => true,
            Request::Index { documents, .. } => documents.len() <= MAX_DOCUMENTS,
            Request::Search(search) => {
                search.limit > 0 && search.offset.saturating_add(search.limit) <= MAX_RESULTS
            }
        };
        self.id() > 0 && rules
    }
}

/// A search. Empty text, null dates, and a false `has_attachments` are no
/// filter.
#[derive(Deserialize)]
pub struct Search {
    pub id: u64,
    pub generation: u64,
    pub words: String,
    pub from: String,
    /// Matches To, Cc, and Bcc.
    pub to: String,
    pub subject: String,
    pub attachment: String,
    /// Unix milliseconds. Messages whose list date is at or after it.
    pub list_date_from: Option<i64>,
    /// Unix milliseconds. Messages whose list date is before it.
    pub list_date_before: Option<i64>,
    pub has_attachments: bool,
    pub offset: u64,
    pub limit: u64,
    pub deadline_ms: u64,
}

/// One message's index document.
#[derive(Deserialize)]
pub struct Document {
    pub id: u64,
    /// Unix milliseconds.
    pub list_date: i64,
    /// Unix milliseconds.
    pub first_archived_at: i64,
    pub has_attachments: bool,
    #[serde(default)]
    pub subject: Option<String>,
    #[serde(default)]
    pub from: Vec<Address>,
    /// To, Cc, and Bcc.
    #[serde(default)]
    pub to: Vec<Address>,
    #[serde(default)]
    pub attachment_names: Vec<String>,
    #[serde(default)]
    pub text: Vec<String>,
}

/// A mailbox. A missing name and `null` are the same. The address is empty
/// when the header wasn't an address list and the name holds its text.
#[derive(Deserialize)]
pub struct Address {
    #[serde(default)]
    pub name: Option<String>,
    #[serde(default)]
    pub address: String,
}

/// The first frame the search process sends.
#[derive(Serialize)]
pub struct Hello {
    #[serde(rename = "type")]
    pub kind: &'static str,
    pub protocol: u32,
    pub tantivy: String,
    pub schema_version: u32,
}

/// The answer to one request: `ok`, or `error` with an optional `cause`.
#[derive(Serialize)]
pub struct Answer {
    pub id: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub ok: Option<Done>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<ErrorCode>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cause: Option<Cause>,
}

#[derive(Serialize)]
#[serde(untagged)]
pub enum Done {
    Opened {},
    Indexed { committed: bool },
    Found { total: u64, ids: Vec<u64> },
    NoTerms { no_terms: bool },
}

#[derive(Serialize, Clone, Copy, Debug, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum ErrorCode {
    IndexMissing,
    IndexDamaged,
    SchemaMismatch,
    WriteFailed,
    UnknownGeneration,
    TimedOut,
}

/// Why a write failed.
#[derive(Serialize, Clone, Copy, Debug, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Cause {
    NoSpace,
    PermissionDenied,
    Other,
}

impl Answer {
    pub fn ok(id: u64, done: Done) -> Answer {
        Answer {
            id,
            ok: Some(done),
            error: None,
            cause: None,
        }
    }

    pub fn error(id: u64, error: ErrorCode) -> Answer {
        Answer {
            id,
            ok: None,
            error: Some(error),
            cause: None,
        }
    }

    pub fn failed(id: u64, failure: Failure) -> Answer {
        Answer {
            cause: failure.cause,
            ..Answer::error(id, failure.error)
        }
    }
}

/// An error code with the cause some codes carry.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Failure {
    pub error: ErrorCode,
    pub cause: Option<Cause>,
}

impl Failure {
    pub fn code(error: ErrorCode) -> Failure {
        Failure { error, cause: None }
    }

    pub fn write_failed(cause: Cause) -> Failure {
        Failure {
            error: ErrorCode::WriteFailed,
            cause: Some(cause),
        }
    }
}
