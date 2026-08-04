# The Code System

How a Phelps code is built, what each part means, and which kind to use.

## A code names a work

A code does not merely record a fact about a text — it makes the text into a
work: addressable, citable, able to gather translations. Before a passage has a
code it is undifferentiated text inside something larger.

Two consequences that decide most questions:

- **Everything treated as a unit is a work.** The tablet is a work; a prayer
  inside it is a work; an excerpt that circulates in an anthology is a work.
  Naming follows use, not physical boundaries.
- **No language is guaranteed to exist for any work.** A code with only
  translations is normal, not a gap in the evidence. An original that turns up
  later is a *first carrier* joining a name that was already correct.

This is why `NULL` is never an acceptable code: it fails to bring the work into
being at all. `TMP` states "one work, identity pending" — still constitutive,
just deferred. `X` states "identity absent given the tools used" — a verdict
indexed to its toolkit, legitimately reopened when better tools arrive. Retired
codes stay retired and are never reissued with different content.

## Anatomy

```
BH00002:1:1        identity + structure
└─────┘ └─┘
 base   structural segments

BH00001G166        identity only (mnemonic)
AB00142S098:2      identity + structure
```

**A colon separates identity from structure.** Base plus mnemonic is the
identity of a work and is never punctuated — the mnemonic is part of what the
thing *is*. Everything after a colon is a structural slice of that identity.

| code | reads as |
|---|---|
| `BH00002` | the work (Kitáb-i-Íqán) |
| `BH00002:1:1` | part 1, paragraph 1 of it |
| `AB00003:1` | paragraph 1 |
| `BH00386A71` | Arabic Hidden Word 71 — a work within a work |
| `BH00001G166` | Gleanings 166, of that tablet |
| `AB00142S098` | Selections (SWAB) item 98, of that tablet |
| `BH00001G166:3` | paragraph 3 of that Gleanings passage |

Codes were previously written unpunctuated (`BH000021001`), which forced the
base/suffix boundary to be guessed from character classes. `scripts/phelpscode`
parses and formats both forms and can upgrade legacy codes; the client does the
same on read so stored codes keep resolving.

## Mnemonics may contain digits

A mnemonic is not always three letters. These are all mnemonics:

| shape | example | means |
|---|---|---|
| three letters | `BH00074BLE` | "Blessed is the Spot" |
| letter + number | `BH00386A71` | Arabic Hidden Word 71 |
| letter + number | `BH00113P83` | Persian Hidden Word 83 |
| letter + number | `BH00001G166` | Gleanings 166 |
| letter + number | `AB00142S098` | SWAB item 98 |

The tell that these are identities rather than slices: `A#` and `P#` sit on one
base each, but `G#` spans twenty bases — twenty different tablets quarried by
one book, each passage named by its citation in that book.

**When a collection has its own numbering, the mnemonic carries it.** Prefer a
marker reflecting the work's own divisions over an invented three-letter name;
fall back to a semantic mnemonic only where no citation exists.

## Choosing between a structural code and a mnemonic

Both can coexist on one base — a tablet can have paragraph codes *and* a famous
excerpt under a mnemonic. The question is only which kind a given row is.

**Test the witnesses of the collection the passage comes from.**

- *Extent-congruent translations of one book* → structural codes work, because
  a slice denotes the same stretch of text in every edition. Gleanings is the
  worked example: its language editions translate one book, so `G166` is stable.
- *Extent-divergent siblings* → mnemonics, the extent-tolerant class. SWAB and
  MMK1 carry coordinated item numbers but make independent extent decisions —
  one marks an item an excerpt where the other does not — so no structural code
  could be extent-exact against either.

A passage that circulates independently — printed in an anthology, cited by its
item number, read on its own — has earned a name. A boundary alone never earns
one.

## Paragraph spaces belong to one edition

A numbered structural space names slices of exactly **one** edition's
paragraphing. Until that owner is declared the space is contested, and
single-row edits there choose a witness rather than fix a defect.

The repair shape:

1. Declare the owner — normally the edition the codes were minted from.
2. Align other sources **by text**, never by their own numbering.
3. Rows whose edition splits differently cannot wear the space's codes; they
   take a bare code (if whole), a mnemonic (if a passage with its own life), or
   documentation.
4. Interleaved foreign texts are intruders to refile, not slices to renumber.

Two failure modes seen in practice: editions that agree on slot *counts* while
disagreeing on slot *boundaries*, and editions that present sections out of
canonical order (one Bishárát edition runs 1, 2, 3, **6, 4, 5**). Neither is
visible without reading the text at a slot.

## Related tables

- `writings.phelps` — the coded rows
- `writing_collections` — anthology membership and positions
- `writing_related` — passages that live inside a collection's tablet without
  being the collection's item; the site links these out rather than rendering
  them as entries
- `inventory_refs` — witnesses per PIN. A trailing `x` on a locator marks an
  **excerpt** (`SWAB#098x`). It tells you the item is an excerpt; it does not
  tell you how to name it.
