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

Expect false positives from alternate translations of one prayer and from
rubric-prefixed rows. Structured sources with position codes give the
trustworthy signal.

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
