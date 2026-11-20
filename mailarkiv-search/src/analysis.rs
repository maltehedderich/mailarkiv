//! How text becomes terms, the same for documents and for what an
//! administrator types.

use tantivy::Index;
use tantivy::tokenizer::{
    LowerCaser, RawTokenizer, RemoveLongFilter, SimpleTokenizer, TextAnalyzer,
};

/// Words: split at every character that isn't a letter or digit, drop words
/// over MAX_WORD bytes, and lowercase.
pub const WORDS: &str = "mailarkiv_words";

/// The whole value as one lowercased term, for addresses and domains.
pub const WHOLE: &str = "mailarkiv_whole";

/// Longer words in mail are mostly encoded data. Tantivy itself drops words
/// over 65,530 bytes with a warning, which this limit keeps from happening.
const MAX_WORD: usize = 100;

fn words_analyzer() -> TextAnalyzer {
    TextAnalyzer::builder(SimpleTokenizer::default())
        // The filter keeps words shorter than its limit.
        .filter(RemoveLongFilter::limit(MAX_WORD + 1))
        .filter(LowerCaser)
        .build()
}

/// Registers both analyzers on index, which must happen before the index
/// is written or searched.
pub fn register(index: &Index) {
    index.tokenizers().register(WORDS, words_analyzer());
    index.tokenizers().register(
        WHOLE,
        TextAnalyzer::builder(RawTokenizer::default())
            .filter(LowerCaser)
            .build(),
    );
}

/// The terms words gives, in order.
pub fn words(text: &str) -> Vec<String> {
    let mut analyzer = words_analyzer();
    let mut stream = analyzer.token_stream(text);
    let mut terms = Vec::new();
    stream.process(&mut |token| terms.push(token.text.clone()));
    terms
}
