package main

import "strings"

// Message is the parsed form of one IRC line.
//
//	[@Tags] [:Source] Verb Params...
type Message struct {
	Tags   map[string]string
	Source string
	Verb   string
	Params []string
}

// Parse implements the modern IRC message grammar (Modern IRC §2, IRCv3
// message-tags, RFC 1459 §2.3).
//
// We deliberately handle the edge cases the chapter-01 parser missed:
//   - Leading "@tag=v;tag2=v2" tag block, with IRCv3 tag-value escapes.
//   - Leading ":source" prefix.
//   - Empty trailing parameter ("foo bar :" → trailing is "", not absent).
//   - "::asdf" trailing that begins with a literal ':'.
//   - Multiple ASCII spaces between atoms (collapsed, per the parser-tests
//     corpus's note on RFC1459 vs RFC2812).
//
// The function never returns an error: any non-empty input is parsed into
// *some* Message. Callers decide what to do with malformed verbs/params.
func Parse(line string) Message {
	line = strings.TrimRight(line, "\r\n")
	var m Message

	// 1. Tags. "@k1=v1;k2;k3=v3 ..." up to the first space.
	if strings.HasPrefix(line, "@") {
		end := strings.IndexByte(line, ' ')
		if end < 0 {
			return m // tags only, no verb — malformed but not our problem
		}
		m.Tags = parseTags(line[1:end])
		line = strings.TrimLeft(line[end+1:], " ")
	}

	// 2. Source. ":source ..." — note that a trailing param's leading ':'
	//    is later, never here, so this is unambiguous.
	if strings.HasPrefix(line, ":") {
		end := strings.IndexByte(line, ' ')
		if end < 0 {
			return m
		}
		m.Source = line[1:end]
		line = strings.TrimLeft(line[end+1:], " ")
	}

	// 3. Trailing param. " :rest of line including spaces and even ':'".
	//    Search for " :" — note the leading space; that's what disambiguates
	//    from a verb that starts with ':' (which is illegal anyway).
	var trailing string
	hasTrailing := false
	if i := strings.Index(line, " :"); i >= 0 {
		trailing = line[i+2:]
		line = line[:i]
		hasTrailing = true
	}

	// 4. Verb + remaining params. Multiple spaces collapse.
	fields := strings.Fields(line)
	if len(fields) == 0 {
		if hasTrailing {
			m.Params = []string{trailing}
		}
		return m
	}
	m.Verb = fields[0]
	if len(fields) > 1 {
		m.Params = append([]string(nil), fields[1:]...)
	}
	if hasTrailing {
		m.Params = append(m.Params, trailing)
	}
	return m
}

// parseTags decodes a "k=v;k2;k3=v3" tag block. Tag values use a non-standard
// escape alphabet (IRCv3 message-tags spec):
//
//	\:  → ;     \s  → ' '     \\ → \     \r → CR     \n → LF
//	any other \x → x (escape dropped, character preserved)
//
// A bare `\` at end-of-string is dropped. A tag with no '=' has empty value.
func parseTags(s string) map[string]string {
	out := make(map[string]string)
	for _, kv := range strings.Split(s, ";") {
		if kv == "" {
			continue
		}
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out[kv] = ""
			continue
		}
		out[kv[:eq]] = unescapeTagValue(kv[eq+1:])
	}
	return out
}

func unescapeTagValue(v string) string {
	if !strings.ContainsRune(v, '\\') {
		return v
	}
	var b strings.Builder
	b.Grow(len(v))
	for i := 0; i < len(v); i++ {
		if v[i] != '\\' {
			b.WriteByte(v[i])
			continue
		}
		if i+1 >= len(v) {
			break // dangling backslash dropped
		}
		switch v[i+1] {
		case ':':
			b.WriteByte(';')
		case 's':
			b.WriteByte(' ')
		case '\\':
			b.WriteByte('\\')
		case 'r':
			b.WriteByte('\r')
		case 'n':
			b.WriteByte('\n')
		default:
			b.WriteByte(v[i+1]) // unknown escape: drop the backslash, keep the char
		}
		i++
	}
	return b.String()
}
