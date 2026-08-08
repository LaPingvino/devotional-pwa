# Editorial Method

Measurements, their calibration, and the failure modes that produce confident
wrong answers. Companion to [codes.md](codes.md), which covers what the codes
mean.

## Identity first, extent second

An opening line tells you **which** work a row is; it never tells you **how
much** of it the row holds. Confirm identity on structural discriminators —
opening *and* closer, ideally three or more — then use extent to decide what
portion is present. Formulaic openings are a shared pool: many prayers begin
«ای ربّ انا امة من امائک» or with a basmala, and two rows sharing one are not
thereby the same prayer.

Inventory first lines are normalised; allow one or two words of fuzz.

## Word-count calibration

Measured over 4,937 English↔original pairs in the corpus.

**original / English**, by English length — use the low percentile as the
"suspicious" threshold rather than a fixed ratio:

| en length | p02 | p05 | p10 | median |
|---|---|---|---|---|
| 50–150 | 0.34 | 0.44 | 0.48 | 0.66 |
| 150–400 | 0.17 | 0.23 | 0.35 | 0.58 |
| 400–1200 | 0.14 | 0.23 | 0.44 | 0.58 |
| >1200 | 0.16 | 0.19 | 0.25 | 0.60 |

**English / inventory word count**, measured on 526 bare codes (whole works by
definition): p05 0.17, p25 0.95, **median 1.35**, p75 1.75, p95 2.01.

| ratio | verdict |
|---|---|
| ≥ 1.2 | consistent with the whole work |
| 0.4 – 1.2 | **indeterminate — do not automate** |
| ≤ 0.4 | a part of the work |

The tails are wide at prayer length: 0.25 is unremarkable for a 200-word
prayer and extreme for a 1,500-word one. Judging short texts against a single
global ratio manufactures false anomalies.

Two limits on the denominator: inventory word counts are rounded and
occasionally absurd, and **a mnemonic row must never be scored against its
base's inventory count** — the yardstick measures the tablet while the mnemonic
names an extent-tolerant work within it.

## Comparing texts

`scripts/textnorm.py` is the shared normalizer. Three corrections it applies,
each of which produced false findings before it existed:

- **Fold Arabic/Persian orthography** — ی/ي/ى, ک/ك, أإآٱ, ة/ه, tatweel,
  harakat. Unfolded, identical texts score as different: one pair scored 0.13
  raw and 1.00 folded.
- **Treat spaceless scripts by character** — CJK, Thai, Lao, Khmer, Myanmar,
  Tibetan have no inter-word spaces, so a complete text scores 1–34 "words" and
  reads as a stub.
- **Strip apparatus** — headings, source attributions and rubric lines. At
  50–150 words a ten-word rubric is enough to flip an extent verdict.

**Use containment, not Jaccard, for whole-versus-excerpt.** An excerpt of a
four-times-longer text scores ~0.25 by Jaccard but ~1.0 by containment; that is
an extent relationship, not a conflict. Containment requires **full text** —
screening on a truncated prefix can only ever find excerpts that are themselves
prefixes, and misreads a closing-sentence excerpt as a stranger.

**But containment alone cannot make a join.** It answers "does this sit inside
that", and a long work contains *every* excerpt of itself — so it will happily
report 0.99 for a dozen different passages against one code. Ten Selections
positions scored 0.99 against `BB00018MAJ` on exactly this: all true, none of
them identity. A join asserts "this item **is** that work", which needs extent
agreement as well as content, so require Jaccard too. On one pass the Jaccard
gate removed 14 of 40 candidate joins, every one an excerpt sitting inside a
larger work. Use containment to *discover* the relationship and Jaccard to
decide whether it is sameness or containment-proper.

**Compare same-language witnesses only.** A containment score between a Persian
text and an English one is noise wearing a number. Where the two sides have no
language in common, fall back to extent plus opening comparison in whatever
languages do pair up — and say that is what you did.

**Know whose text you are holding.** When an item's text is taken from its
*base* code, every membership position on that base shares it, so the comparison
cannot tell those positions apart — it is evidence about the base, not the item.
Restrict such a pass to bases carrying a single position, or fetch per-item text
first.

Expect false positives from alternate translations of one prayer and from
rubric-prefixed rows. Structured sources with position codes give the
trustworthy signal.

**Where does it start, and how much of it is there?** These are two questions,
and a collection item that claims a whole tablet's code needs both answered
before you believe it. The inventory answers both without fetching anything:
`Word count` gives extent in the original language, and `First line (original)`
gives the opening. Neither instrument decides alone — a short text that opens at
the tablet's start is an excerpt *from the beginning*, and a full-length text
that opens elsewhere is a different work. Only agreement means "whole tablet".

Comparing openings needs a different normalizer from comparing texts. `norm()`
returns a **set**, which is what containment wants and which throws word order
away; an opening is a **sequence**, so `opening()` keeps the order and drops the
spaces — Persian word boundaries are not stable between the inventory's
transcriptions and the published editions (هر چند / هرچند). Even folded, the two
disagree on real spellings (علا/علی, به/ب as a prefix), so `opens_alike()` scores
similarity rather than demanding an exact prefix. Across 43 SWAB items the score
came out bimodal — ≥0.83 or ≤0.48, nothing between — so the 0.85 cut sits in an
empty gap rather than on a slope. Check that gap on each new population; if the
scores are continuous there, the threshold is doing the work, not the evidence.

Calibrate on rows already known to be excerpts before trusting either number.
SWAB's 33 tightened excerpts average 0.23 extent (max 0.56) and Gleanings' 62
average 0.07 (max 0.43) — in both the whole-tablet region is empty of known
excerpts, which is what earns the reading. The two books then answer very
differently: 17 of 43 SWAB items are whole tablets, against 6 of 81 in Gleanings.
That contrast is the instrument tracking the corpus — Gleanings is a book of
extracts, SWAB reprints many complete tablets — and not a threshold choice.

## Cross-language placement

Similarity scores do not cross languages. What works instead: **extent
summation plus opening-and-closer matching**. Where an original row is
section-extent and the translation is split into paragraphs, the translation's
parts sum to the original at the normal ratio, the original's opening matches
the first part and its closer matches the last. That places rows without any
cross-language text comparison.

## Failure modes

**A clean profile is not evidence.** A method whose output looks orderly
regardless of whether its premise holds has told you nothing. Two examples from
this corpus: grouping editions by slot *occupancy* produced tidy "families"
that reading a single slot's text destroyed; numbering section headers by
*position* produced a clean spacing profile while mislabelling six of fifteen,
because one edition presents its sections out of order. Before trusting a
profile, ask what input would make it look different. If nothing would, it is
decoration.

**Corrections that fit the data make things simpler.** Every correct
reinterpretation in this corpus has *deleted* machinery — special cases,
lookup tables, redirect layers. Growing apparatus is a signal to re-read rather
than push through.

**Suspect boundaries before identity.** A row that resists matching is more
often mis-segmented than misidentified. Mid-formula cuts, rows spanning two
anthology items, and paragraph fragments duplicating a whole-passage row are
all common.

## Measuring the right thing

Most false findings in this corpus come from a measurement that was correct in
itself and answered the wrong question. Seven that have each produced a confident
mistake:

**Match the granularity of the measurement to the granularity of the claim.** A
ratio is evidence about the unit it was computed over — nothing finer, nothing
coarser. The Tablets of the Divine Plan appeared to have ~194 truncated Persian
rows, 1 to 10 words against English paragraphs of 100 to 345; summed per work
every one is in band (0.64–0.74), so the *work-level* claim "text is missing" is
false. But the slot-level claim "this row is aligned with that one" is a
different claim and still false, and only a slot-level measurement can settle
it. Totals answer whether anything was lost; slots answer whether it sits where
its code says. Reaching for the wrong one produced both a false alarm here and,
in the SWAB case, four rows that looked like they spanned an item boundary and
read 0.62 and 0.50 against English totals.

**A count-check is evidence only for the rows it measured.** Verifying on a
subset and applying to the superset is worse than not checking, because it
produces confidence. The WHERE clause of the measurement must be the WHERE clause
of the update, character for character.

**Name the known-good population before running a check.** "Identical text under
two language labels" returns 168, of which 163 are the Persian/Arabic twins that
are *correct* — the label there records which section of a source a row came
from. "Markup in a text" looks alarming until you know 34,000 rows legitimately
carry paragraph tags. A check that has not had its exceptions named is not ready
to run, and a check that fires far more often than the truth teaches its reader
to ignore it.

**A mint needs a referent, not necessarily a text.** A code with no text is
normal — no language is guaranteed to exist for any work, and a catalogued item
we hold nothing for is an honest gap. What a mint cannot do without is something
that says *what the name names*: a text row, or an inventory or registry entry.
Prayers and Meditations is the worked example of getting this wrong in the other
direction — eleven item codes were nearly minted whose items existed only as
positions in a collection, which would have replaced one useful page with
several blank ones. Fetching the per-item texts first gave each code a referent.

**A screen and an assertion are different instruments.** A loose fingerprint
that tolerates false positives is a fine way to generate a reading list; a
published count is a claim that must mean exactly what it says. Swapping them is
easy and quiet: a quality-page check once shipped keying on a truncated,
punctuation-stripped fingerprint while its "returns zero" claim had been verified
with exact full-text comparison. Screens go in research passes. Published numbers
carry the measurement that was actually verified.

**"We hold it" and "a reader can read it" are different questions.** Asking
whether an item's tablet has any text becomes true for every item the moment a
collection is anchored — a check that cannot fail. Ask instead whether there is
text in a language the reader has.

**Measure existing coverage before building an ingest.** The instinct on finding
a gap is to build the fetcher, and the fetcher is the expensive, error-prone
part. Ask first how much of the gap is already held: Prayers and Meditations
looked like it needed an ingest of every item, and the texts were largely present
already — what was missing was the numbering that connects item to text, which is
a query, not a scrape. Of the catalogued items that genuinely held nothing, all
88 turned out to have deep links, so the measurement also chose the method. The
coverage query costs minutes and decides both whether to build and what to build.

## Safeguards before a write

- **Name the act.** Placing a row under an existing name (a *join*) is the safe
  tier. Creating a name (a *mint*) is inherited by everyone downstream and is
  not a mechanical operation — mints ship as reviewed, named lists. Weakening or
  retiring a claim (a *demotion*) is history-preserving and visible. Moving rows
  between slots of a numbered space (a *realignment*) is legal only once that
  space has a declared owner. If an operation has no name here, that is the edge
  of the rules.
- **Count-check every targeted update.** Before and after.
- **Sample the neighbourhood.** Look at sibling codes and the same code in other
  languages before a single-row fix; a second instance of the defect means it is
  systemic and the class should be filed rather than the row patched.
- **Recoding must sweep every table.** `writings`, `writing_collections`,
  `writing_related` (both columns), and `prayer_book_structure`. The foreign key
  catches the last one, which is how it keeps being remembered.
- **A defect that respects a metadata boundary exactly was probably drawn by
  your filter.** Real defects are ragged. Re-key on content and diff.
- **Measure derived constants.** A threshold produced by arithmetic on other
  statistics deserves one histogram before use.
- **Distrust gates that cannot fail.** A verification that passes 100%, or a
  screen returning zero hits over a large corpus, needs a planted negative.
