# Prayer Matching Architecture

## Core Concept

Phelps Index Numbers (PIN) are used as **hangers** — stable identifiers for tablets/prayers. The inventory maps PINs to first lines and metadata. Our system extends this by:

1. Assigning the same PIN to translations of the same prayer across languages
2. Adding mnemonic suffixes (3 uppercase letters) when a tablet contains multiple distinct prayers (e.g., BH02806FAM for families, BH02806FAS for fasting)
3. Using X-codes (XAB/XBH/XBB) for identified prayers without official Phelps numbers
4. Using TMP codes for genuinely unresolved prayers

## Database

- **Dolt** at `~/bahaiwritings`, DoltHub: `holywritings/bahaiwritings`
- Primary key: `version` (UUID), not source_id
- `phelps` is the matching key — same phelps = same prayer/text
- `type` field: NULL or 'prayer' for prayers; 'iqan', 'aqdas', 'hidden_words', 'gleanings', 'pm', 'saq', 'tablets', 'days_remembrance', 'ridvan', 'lawh', 'divineplan' for writings
- Sources: bahaiprayers.net, bahaiprayers.app, llm-translation, bahai.org, reference.bahai.org, librodecerteco

## Writing Types

| Type | Title | Author | SingleBook | Paragraph scheme |
|------|-------|--------|------------|-----------------|
| hidden_words | The Hidden Words | Bahá'u'lláh | No | BH00386Axx (Arabic), BH00113Pxx (Persian) |
| aqdas | Kitáb-i-Aqdas | Bahá'u'lláh | Yes | BH00001xxx (K1-K190) |
| iqan | Kitáb-i-Íqán | Bahá'u'lláh | Yes | BH000021xxx (Part 1), BH000022xxx (Part 2) |
| gleanings | Gleanings | Bahá'u'lláh | Yes | BH10200xxx |
| pm | Prayers & Meditations | Bahá'u'lláh | Yes | BH09700xxx |
| saq | Some Answered Questions | 'Abdu'l-Bahá | Yes | AB09900xxx |
| tablets | Tablets of Bahá'u'lláh | Bahá'u'lláh | No | Various codes |
| days_remembrance | Days of Remembrance | Bahá'u'lláh | Yes | BH09600xxx |
| ridvan | Ridván Messages | UHJ | Yes | UHRyyyy |
| lawh | Other Tablets | Bahá'u'lláh | No | Various (BH00005xxx for ESW) |
| divineplan | Tablets of the Divine Plan | 'Abdu'l-Bahá | No | AB00956-AB01552 (14 tablets) |

## Code Assignment Rules

- Assigning EXISTING codes: fine
- Minting NEW AB/BH/BB/ABU codes: NOT allowed (requires Phelps authority)
- X-codes (XAB/XBH/XBB): for identified prayers without official numbers
- TMP codes: for genuinely unknown prayers
- NULL phelps: NEVER correct

## False Positive Codes (never assign)

ABU0030, ABU0196, ABU0394, AB00049, AB02825, AB12482, BH09952, BH02023, BH03060, BH07775, BH08846, BH03270

## Compilation Codes

Some codes (especially BHU/BBU/ABU) bundle different prayers per language. These need splitting with mnemonic suffixes. Detect via high length ratios across languages.

## Cross-Source Handling

Near-duplicate entries from different sources (app vs net, <30% length difference) are KEPT. The site deduplicates at display time. Only delete when entries are clearly different prayers.
