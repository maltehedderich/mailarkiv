package mailparse

import "github.com/jhillyerd/enmime/v2"

// Kind is a problem kind's wire word.
type Kind string

const (
	UnknownCharset     Kind = "unknown_charset"
	InvalidEncoding    Kind = "invalid_encoding"
	MalformedHeader    Kind = "malformed_header"
	MalformedStructure Kind = "malformed_structure"
	CutShort           Kind = "cut_short"
	TooManyParts       Kind = "too_many_parts"
	NestedTooDeeply    Kind = "nested_too_deeply"
	BodyCut            Kind = "body_cut"
	TookTooLong        Kind = "took_too_long"
)

// Problem is one kind of problem in one part. It never holds enmime's error
// detail, which can quote the message.
type Problem struct {
	// Position is the part number, or "" for the whole message.
	Position string `json:"position"`
	// DeclaredType is "" when the part declares none.
	DeclaredType string `json:"declared_type,omitempty"`
	Kind         Kind   `json:"kind"`
}

// kindOf maps an enmime error name to a problem kind. A name enmime adds
// later counts as malformed structure until we decide otherwise.
func kindOf(name string) Kind {
	switch name {
	case enmime.ErrorCharsetConversion, enmime.ErrorCharsetDeclaration:
		return UnknownCharset
	case enmime.ErrorMalformedBase64, enmime.ErrorContentEncoding:
		return InvalidEncoding
	case enmime.ErrorMalformedHeader, enmime.ErrorMissingContentType:
		return MalformedHeader
	default:
		return MalformedStructure
	}
}

// problemList keeps each kind once per part, in the order first found.
type problemList struct {
	items []Problem
	seen  map[[2]string]bool
}

func (l *problemList) add(position, declared string, kind Kind) {
	key := [2]string{position, string(kind)}
	if l.seen[key] {
		return
	}
	if l.seen == nil {
		l.seen = make(map[[2]string]bool)
	}
	l.seen[key] = true
	l.items = append(l.items, Problem{Position: position, DeclaredType: declared, Kind: kind})
}

func (l *problemList) addPart(part *enmime.Part, position string) {
	for _, err := range part.Errors {
		l.add(position, declaredType(part), kindOf(err.Name))
	}
}

// damaged reports whether a part has an encoding error.
func damaged(part *enmime.Part) bool {
	for _, err := range part.Errors {
		if kindOf(err.Name) == InvalidEncoding {
			return true
		}
	}
	return false
}
