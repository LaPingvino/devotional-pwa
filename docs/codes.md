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

## Compilations and quoting works

Three kinds, three treatments. The discriminator between the first two is
**unitary circulation** — does it recite as one text, or read as many items? —
not the presence or absence of connective prose.

1. **A fused compilation.** Another's words welded by a compiler into a single
   text that circulates as one work: one title, one opening, one closer. The
   Ziyáratnámih (Tablet of Visitation, compiled by Nabíl-i-Aʻẓam) is the
   type. **This is a work.** It takes the Phelps PIN where one has been
   assigned — `BH02307` is catalogued as *"Ziyarat-Namih (Tablet of
   Visitation)"*, so nothing is minted — and otherwise `C<author>####`, e.g.
   `CBH0001` for a compilation of Bahá'u'lláh's words. Full mnemonic and
   structural grammar applies. Its sources are recorded as relations, not as
   codes.

2. **An arranged anthology.** Extracts that remain discrete items, with or
   without connective prose — Gleanings, *Selections*, themed extract books.
   **This is a collection, never a code.** Items keep their own codes, the
   book is a `writing_collections` key, membership lives in refs. Gleanings
   is the reason the discriminator is circulation rather than voice: it has
   no connective prose either, yet its items stay items.

3. **A work in its author's own voice that quotes.** *God Passes By*,
   *Bahá'u'lláh and the New Era*. **This is its author's work** — its
   quotations are refs in `inventory_refs`, never codes of their own. At the
   scale of a book that is mostly quotation, per-quotation codes would be
   unusable anyway; the refs table already holds ~70k of them.

C binds the **content-author** — whose words the compilation contains — which
stays knowable even when the compiler is someone else. The compiler is
provenance, recorded in notes. Note the risk this inherits: `XT` exists
because 16 of 16 `XAB` codes turned out to be Bahá'u'lláh, the scrape's author
claim never having been evidence. A `C` mint asserts authorship and must
establish it rather than inherit a source's label.

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
- **An anthology's identifier is its `collection_key`.** A code on the
  book-object could never have a carrier, every character in it belonging to some
  item, so its only possible use would be positional coding of those items —
  displacement of the origin codes. Where tooling wants a code-shaped handle for
  a book, that is presentation of the key; it never enters `writings.phelps` or
  any relation table's phelps column. Many keys *do* map to real codes
  (`iqan` → `BH00002`, `hidden-words` → `BH00386`/`BH00113`), and that mapping is
  worth making explicit — but for the pure anthologies the right-hand side is
  empty and must stay empty.

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
- `writing_collections` — anthology membership and positions. **One table, two
  readings:** `gleanings` rows point outward to member works, while `gpb` rows
  point at the book's *own* chapters. Nothing disambiguates them but knowing
  which kind of book you are looking at. It has not bitten yet; it will the first
  time a fused compilation lists both its own slices and its sources, so that
  case needs a key convention before it arrives.
- `writing_related` — passages that live inside a collection's tablet without
  being the collection's item; the site links these out rather than rendering
  them as entries
- `inventory_refs` — witnesses per PIN. A trailing `x` on a locator marks an
  **excerpt** (`SWAB#098x`). It tells you the item is an excerpt; it does not
  tell you how to name it.
