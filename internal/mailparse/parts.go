package mailparse

import (
	"strconv"
	"strings"

	"github.com/jhillyerd/enmime/v2"
	"github.com/jhillyerd/enmime/v2/mediatype"
)

// leaf is a part that holds content rather than other parts.
type leaf struct {
	part     *enmime.Part
	position string
}

// walkParts numbers the parts as IMAP section numbers do (RFC 3501, section
// 6.4.5): the parts of a multipart are 1, 2, 3, a part inside part 2 is 2.1,
// and a message that isn't multipart has one part, 1. The multipart at the
// top has no number of its own, so its problems belong to the whole message.
// It records each part's problems and returns the leaves in document order.
func walkParts(root *enmime.Part, problems *problemList) []leaf {
	var leaves []leaf
	var visit func(part *enmime.Part, position string)
	visit = func(part *enmime.Part, position string) {
		problems.addPart(part, position)
		if strings.HasPrefix(part.ContentType, "multipart/") && part.Boundary == "" {
			// enmime finds no parts in it and reports nothing.
			problems.add(position, declaredType(part), MalformedStructure)
		}
		if !isContainer(part) {
			leaves = append(leaves, leaf{part: part, position: position})
			return
		}
		number := 1
		for child := part.FirstChild; child != nil; child = child.NextSibling {
			visit(child, childPosition(position, number))
			number++
		}
	}
	position := "1"
	if isContainer(root) {
		position = ""
	}
	visit(root, position)
	return leaves
}

func isContainer(part *enmime.Part) bool {
	return strings.HasPrefix(part.ContentType, "multipart/") && (part.FirstChild != nil || part.Boundary != "")
}

func childPosition(parent string, number int) string {
	if parent == "" {
		return strconv.Itoa(number)
	}
	return parent + "." + strconv.Itoa(number)
}

// declaredType is the part's media type as declared, lowercased and without
// parameters, or "" when the part declares none.
func declaredType(part *enmime.Part) string {
	if part.Header.Get("Content-Type") == "" {
		return ""
	}
	return cutRunes(strings.ToLower(part.ContentType), maxDeclaredType)
}

// chooseBodies picks the first text/plain and the first text/html part that
// aren't attachments.
func chooseBodies(leaves []leaf) (plain, html *leaf) {
	for i := range leaves {
		candidate := &leaves[i]
		if !isBodyCandidate(candidate.part) {
			continue
		}
		switch strings.ToLower(candidate.part.ContentType) {
		case "text/plain":
			if plain == nil {
				plain = candidate
			}
		case "text/html":
			if html == nil {
				html = candidate
			}
		}
	}
	return plain, html
}

// isBodyCandidate is false for a part marked as an attachment or carrying a
// file name, which mail programs show as a file rather than as the message.
func isBodyCandidate(part *enmime.Part) bool {
	return !strings.EqualFold(part.Disposition, "attachment") && fileName(part) == nil
}

// fileName is the decoded Content-Disposition filename, or else the decoded
// Content-Type name, or nil when the part gives neither.
func fileName(part *enmime.Part) *string {
	name := headerParameter(part, "Content-Disposition", "filename")
	if name == "" {
		// enmime doesn't keep the type's parameters when it parses.
		name = headerParameter(part, "Content-Type", "name")
	}
	if name == "" {
		return nil
	}
	decoded := decodeHeader(name)
	return &decoded
}

// headerParameter reads one parameter of a header field, with RFC 2231
// encoded values decoded.
func headerParameter(part *enmime.Part, field, parameter string) string {
	_, params, _, err := mediatype.Parse(part.Header.Get(field))
	if err != nil {
		return ""
	}
	return params[parameter]
}

// body decodes a displayed body to valid UTF-8 and cuts it at MaxBody bytes.
func body(chosen *leaf, problems *problemList) *Body {
	content, cut := cutBytes(validUTF8(string(chosen.part.Content)), MaxBody)
	if cut {
		problems.add(chosen.position, declaredType(chosen.part), BodyCut)
	}
	return &Body{Position: chosen.position, Content: content, Truncated: cut}
}
