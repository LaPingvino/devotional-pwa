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

### One passage, two circulation contexts

The anthologies work alike even where they were coded differently. A tablet
quarried by *Selections* and the same passage circulating in prayer books are
the **same ground truth at slightly different identity** — the prayer-book
rendering is often a shorter variant, and the two carry different extents.

Where that happens, both names are legitimate and neither is a stranger-split:
the semantic mnemonic names the prayer as it circulates devotionally, the
citation mnemonic names the anthology item at that book's extent. Record the
relationship rather than merging them.

Worked example: `AB03461MAR` (the marriage prayer, prayer-book rendering,
en 102w) and `AB03461S086` (Selections item 86, en 73+44w) coexist on one
tablet. The signature to look for is an extent difference that tracks the
source, within one language.

Letters in use for citation mnemonics: `G` Gleanings, `S` Selections,
`A`/`P` Arabic and Persian Hidden Words, `D` Duʻá for *Prayers and
Meditations* — `P` being already taken.

## Books that exist as books

Three kinds, but one genus. **A book earns a work's name the same way a passage
does — by unitary circulation.** Gleanings is translated as a unit into 25
languages, cited by its own item numbers, and already named in the inventory's
own reference layer: `inventory_refs` carries 165 `GWB` refs over 122 PINs,
locators `001x`–`166x`, alongside `SWAB` 229, `PM` 182, `BNE`/`BNEP` 38 and
`ESW` 3. These labels *are* the books' names; the only open question was ever
which namespace holds them.

The kinds differ in **where the text lives**, and that decides what the code may
carry.

1. **A fused compilation** recites as one text: one title, one opening, one
   closer. The Ziyáratnámih (Tablet of Visitation, compiled by Nabíl-i-Aʻẓam) is
   the type. **Its code carries text** — rows in `writings`, full mnemonic and
   structural grammar. It takes the Phelps PIN where one exists (`BH02307` is
   catalogued as *"Ziyarat-Namih (Tablet of Visitation)"*, so nothing is minted).
   Its sources are relations.

2. **An arranged anthology** reads as many items — Gleanings, *Selections*,
   themed extract books. **Its code carries an arrangement, not a text.** The
   book-level work is the selection, the order and the apparatus; that content is
   carried by `writing_collections` exactly as a textual work's content is
   carried by `writings`. The code is the book's identifier — collection key,
   ref-source label (`GWB` ↔ `CBHGLEA`), i18n key, and the anchor for its
   citation letter, so that "`G` is the citation letter of `CBHGLEA`" becomes
   data rather than a sentence in this file.

   **No `writings` row ever bears an anthology's code, and it takes no
   structural segments.** Not `CBHGLEA`, not `CBHGLEA:166`. Its positions are
   membership rows, addressed by (key, position), each resolving to an item under
   the item's *own* name. Item identities are never renamed: `BH00001G166` stays,
   its base carrying whose tablet and its mnemonic carrying which citation.
   Compiler prose, where a compiler writes any, is the compiler's own work
   (`SEGPBFW`) and never the anthology's slice.

3. **A work in its author's own voice that quotes** — *God Passes By*,
   *Bahá'u'lláh and the New Era*. **This is its author's work**; its quotations
   are refs, never codes. At the scale of a book that is mostly quotation,
   per-quotation codes would be unusable anyway.

Why the constraint on kind 2 is exact rather than cautious: every character of
Gleanings, in every language, belongs to some item. A code on the book that
carried text could therefore only carry text taken from items — which is
displacement of the origin codes, the one thing such a code must not do. So the
invariant is checkable:

```sql
-- for any arrangement-only code, this must be 0, permanently
SELECT COUNT(*) FROM writings WHERE phelps LIKE 'CBHGLEA%';
```

This makes `C` a **two-mode namespace**: text-carrying (kind 1) takes the full
grammar, arrangement-only (kind 2) takes none. The mode is declared at mint time,
in the registry, alongside the code.

### Shape of a C base

`C` + content-author + a four-character mnemonic: `CBHGLEA` is C + BH + GLEA. A
serial (`CBH0001`) is simply the degenerate mnemonic for a compilation with no
established name — consistent with mnemonics being allowed to contain digits.
Prefer the semantic four where a book has a name. Seven characters either way.

Four characters collide easily, so **C bases mint from the registry only**.

C binds the **content-author** — whose words the book contains — which stays
knowable even when the compiler is someone else; the compiler is provenance,
recorded in notes. Note the risk this inherits: `XT` exists because 16 of 16
`XAB` codes turned out to be Bahá'u'lláh, the scrape's author claim never having
been evidence. A `C` mint asserts authorship and must establish it rather than
inherit a source's label.

## A name is not an address

One stretch of text can be reached two ways: by what it is — its code — and by
where it appears — a position in some containing book. **These are not two
names.** A code asserts that text rows exist whose identity it is; a position is
an address, computed from the relation tables at read time.

The catalog already runs on this split. The `gleanings` collection addresses all
166 of its items by **origin** code — not one membership row carries a G-code —
while the 63 `G#` identities live in `writings` exactly where a text row carries
that extent. Phelps recorded the *Bahá'u'lláh and the New Era* positions the same
way: 41 refs like `BNE.071x` against 39 PINs, origin on the left and book page on
the right, reversible by query and never minted as codes.

- A row's `phelps` is the identity of its text **as it circulates**. An anthology
  item circulates as its origin work: origin code stored, book position a
  relation. A fused compilation's paragraph circulates as the compilation:
  `CBH0001:3` stored, the source span a relation. Same rule, opposite outcomes —
  which is how you can tell the rule is circulation rather than origin-worship.
- **Two codes for one ground truth require two circulating identities** — an
  extent or variant difference, as with `AB03461MAR` and `AB03461S086`. Identical
  characters under two addressing structures are one code plus one relation row.
  There is no alias class, and citation is not a reason to mint one: "cite BNE
  ¶4.12 and land on the passage" asks for a resolver over the relation tables,
  and an address can be derived at read time where an identity must be stored.
- **An anthology's code names the book, never its items' text.** The book is a
  real work (see above) and may hold an identifier; what it may not hold is a
  `writings` row or a structural segment, because every character in it belongs
  to some item. Many collection keys also map to ordinary textual works
  (`iqan` → `BH00002`, `hidden-words` → `BH00386`/`BH00113`), and that mapping is
  worth making explicit either way.

## Namespaces beyond the central figures

`UH` and `SE` are extension namespaces rather than Phelps space, minted freely.
`W` extends that to Bahá'í works by other authors — `WESBANE` is Esslemont's
*Bahá'u'lláh and the New Era*, so a book that is mostly quotation is a work by
its author with its quotations as refs, not a compilation of anyone's words.

**Every base in every namespace is exactly 7 characters** — `BH00002`, `SEGPBFW`,
`CBH0001`, `WESBANE` — so `LEFT(phelps,7)` keeps working uniformly.

`C` and `W` bases carry no mnemonics, and the parser knows none: it splits base
from mnemonic on `[A-Z]{2}[0-9]{5}`, so these namespaces parse as base-only while
their structural segments work normally. **This is correct rather than a gap.** A
famous passage inside a quoting work is the origin author's work — named on the
origin base if circulation has earned it a name, held as a ref otherwise — and a
compilation's sub-passage is always a source span, named at origin. A `W`
mnemonic could only ever name a passage in the author's *own* voice that
circulates independently, and no such case exists yet. Because the base length is
fixed at 7, the split point is already defined if one ever does; the parser
extends losslessly then.

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
- `writing_collections` — anthology membership and positions, and for a kind-2
  book **this table is where its content lives**. Two readings share it:
  `gleanings` rows point outward to member works, while `gpb` rows are the book's
  own table of contents. That is just the two kinds showing through — once a key
  maps to a code, the code's kind tells the reader which reading applies.
- `writing_related` — passages that live inside a collection's tablet without
  being the collection's item; the site links these out rather than rendering
  them as entries
- `inventory_refs` — witnesses per PIN. A trailing `x` on a locator marks an
  **excerpt** (`SWAB#098x`). It tells you the item is an excerpt; it does not
  tell you how to name it.
