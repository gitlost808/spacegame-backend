// Package namefilter checks player names against local English and German policies.
package namefilter

import (
	_ "embed"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

//go:embed words/en.txt
var english string

//go:embed words/de.txt
var german string

//go:embed words/allow.txt
var allowed string

//go:embed words/extremism.txt
var extremistReferences string

type term struct {
	value string
	whole bool
}

var terms = loadTerms(english + "\n" + german)
var extremistTerms = loadTerms(extremistReferences)
var exceptions = strings.Fields(normalize(allowed))

// Disallowed checks a comparison copy; callers retain the original display name.
func Disallowed(name string) bool {
	tokens := strings.Fields(normalize(name))
	compact := strings.Join(tokens, "")
	if matches(extremistTerms, tokens, compact, compact) || extremistContext(name, tokens, compact) {
		return true
	}
	partial := compact
	for _, exception := range exceptions {
		partial = strings.ReplaceAll(partial, exception, "")
	}
	return matches(terms, tokens, compact, partial)
}

func matches(words []term, tokens []string, whole, partial string) bool {
	for _, word := range words {
		if word.whole {
			if whole == word.value || slices.Contains(tokens, word.value) {
				return true
			}
		} else if strings.Contains(partial, word.value) {
			return true
		}
	}
	return false
}

func extremistContext(name string, tokens []string, compact string) bool {
	// Keep digits literal: leetspeak maps 1488 to "ia88", also found in Julia88.
	literal := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, norm.NFKC.String(name))
	codes := strings.FieldsFunc(literal, func(r rune) bool { return !unicode.IsDigit(r) })
	if slices.Contains(codes, "1488") || slices.Contains(codes, "8814") {
		return true
	}
	// Common misspellings need an additional cue; ordinary surnames stay usable.
	if strings.Contains(compact, "adolf") || strings.Contains(compact, "adolph") || strings.Contains(compact, "heil") || slices.Contains(codes, "88") {
		for _, token := range tokens {
			if slices.Contains([]string{"hilter", "hitlar", "hitier", "hittler"}, strings.Trim(token, "x0123456789")) {
				return true
			}
		}
	}
	return false
}

func loadTerms(data string) []term {
	var words []term
	for _, line := range strings.Split(data, "\n") {
		line, _, _ = strings.Cut(line, "#")
		line = strings.TrimSpace(line)
		whole := strings.HasPrefix(line, "=")
		value := strings.Join(strings.Fields(normalize(strings.TrimPrefix(line, "="))), "")
		if value != "" {
			words = append(words, term{value: value, whole: whole})
		}
	}
	return words
}

func normalize(value string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '0':
			return 'o'
		case '1', '!':
			return 'i'
		case '3':
			return 'e'
		case '4', '@':
			return 'a'
		case '5', '$':
			return 's'
		case '7':
			return 't'
		}
		if unicode.IsMark(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, cases.Fold().String(norm.NFKD.String(value)))
}
