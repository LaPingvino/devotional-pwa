// Package phelpscode parses and formats Phelps codes.
//
// THE RULE (Joop, 2026-08-04): a colon separates IDENTITY from STRUCTURE.
// Base + mnemonic is the identity of a work and is never punctuated — the
// mnemonic is part of what the thing IS. Everything after a colon is a
// structural slice of that identity.
//
//	BH00002        the work
//	BH00002:1:1    part 1, paragraph 1 of it
//	AB00003:1      paragraph 1
//	BH00001G166    a work-within-a-work (Gleanings 166 of that tablet) — UNCHANGED
//	BH00386A71     Arabic Hidden Word 71 — UNCHANGED
//	AB00142S098    Selections (SWAB) item 98 — UNCHANGED
//	AB00001FIR     semantic mnemonic — UNCHANGED
//	BH00001G166:3  paragraph 3 of that Gleanings passage — newly expressible
//
// A mnemonic may contain digits: G166/A71/S098 name a collection's own
// numbering. Pure-digit segments are the structural class. The flat legacy
// form could not express a paragraph OF a mnemonic at all.
//
// Why punctuate at all: the legacy form packs the 7-character base together
// with an unpunctuated suffix, so the boundary must be guessed from character
// classes — and the guess was wrong by design in places (the old
// writingBaseCode derived "BH000021" as the base of the Íqán's BH000021001,
// its own comment calling that "acceptable" rather than correct). Six suffix
// shapes exist and two are two-level with a per-base split point.
//
// Upgrade() and Legacy() are total and mutually inverse for real corpus
// codes, so old favourites, permalinks and shared links keep resolving.
package phelpscode

import (
	"regexp"
	"strconv"
	"strings"
)

// twoLevel maps a base to the number of digits in the FIRST level, for the
// six bases whose legacy suffix packs two levels into four digits.
var twoLevel = map[string]int{
	"AB00002": 2, // Memorials of the Faithful — memorial : paragraph
	"BH00002": 1, // Kitáb-i-Íqán — part : paragraph
	"BB00001": 2, // Persian Bayán — váḥid : báb
	"BB00002": 1, // Qayyúmu'l-Asmá' — (leading 0) : súrih
	"AB00001": 2, // Will & Testament — section : paragraph
	"BB00020": 2, // Arabic Bayán — váḥid : (trailing 00)
}

var (
	legacyPat = regexp.MustCompile(`^([A-Z]{2}[0-9]{5})(.*)$`)
	digitsPat = regexp.MustCompile(`^[0-9]+$`)
)

// Code is a parsed Phelps code: an identity (base plus optional mnemonic) and
// zero or more structural segments.
type Code struct {
	Base     string   // BH00001
	Mnemonic string   // G166, A71, FIR — "" when the identity is the bare work
	Parts    []string // structural segments, unpadded
}

// Identity is the code without structure — what the row is OF.
func (c Code) Identity() string { return c.Base + c.Mnemonic }

func (c Code) String() string {
	if len(c.Parts) == 0 {
		return c.Identity()
	}
	return c.Identity() + ":" + strings.Join(c.Parts, ":")
}

// Parse accepts either form.
func Parse(s string) Code {
	s = strings.TrimSpace(s)
	var structural []string
	if i := strings.Index(s, ":"); i >= 0 {
		structural = strings.Split(s[i+1:], ":")
		s = s[:i]
	}
	m := legacyPat.FindStringSubmatch(s)
	if m == nil {
		return Code{Base: s, Parts: structural}
	}
	base, suffix := m[1], m[2]

	switch {
	case suffix == "":
		return Code{Base: base, Parts: structural}

	case !digitsPat.MatchString(suffix):
		// Starts with a letter (or is otherwise non-numeric): a mnemonic, and
		// mnemonics are identity — they never gain a colon of their own.
		return Code{Base: base, Mnemonic: suffix, Parts: structural}

	case len(structural) > 0:
		// Already-punctuated input whose head was digits: keep as structure.
		return Code{Base: base, Parts: append([]string{trimZeros(suffix)}, structural...)}

	case len(suffix) == 4:
		if n, ok := twoLevel[base]; ok {
			return Code{Base: base, Parts: []string{trimZeros(suffix[:n]), trimZeros(suffix[n:])}}
		}
		return Code{Base: base, Parts: []string{trimZeros(suffix)}}

	default:
		return Code{Base: base, Parts: []string{trimZeros(suffix)}}
	}
}

// Upgrade converts a legacy code to the punctuated form. Punctuated and
// mnemonic-only codes come back unchanged, so it is safe to apply to anything
// — including a user's saved favourites.
func Upgrade(s string) string { return Parse(s).String() }

// Legacy renders a code in the unpunctuated form, restoring zero-padding, so
// old URLs and stored data can still be matched.
func Legacy(s string) string {
	c := Parse(s)
	if len(c.Parts) == 0 {
		return c.Identity()
	}
	if n, ok := twoLevel[c.Base]; ok && len(c.Parts) == 2 && c.Mnemonic == "" {
		return c.Base + pad(c.Parts[0], n) + pad(c.Parts[1], 4-n)
	}
	if c.Mnemonic == "" && len(c.Parts) == 1 {
		return c.Base + pad(c.Parts[0], 3)
	}
	// A paragraph OF a mnemonic has no legacy equivalent — the flat form
	// could not express it. Return the punctuated code rather than
	// manufacturing something like BH00001G1663, which would parse back as a
	// different mnemonic entirely.
	return c.String()
}

// BasePIN returns the work the code belongs to — always the bare PIN.
func BasePIN(s string) string { return Parse(s).Base }

// IsMnemonic reports whether the code names a work-within-a-work.
func IsMnemonic(s string) bool { return Parse(s).Mnemonic != "" }

func trimZeros(s string) string {
	if t := strings.TrimLeft(s, "0"); t != "" {
		return t
	}
	return "0"
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	if _, err := strconv.Atoi(s); err != nil {
		return s
	}
	return strings.Repeat("0", n-len(s)) + s
}
