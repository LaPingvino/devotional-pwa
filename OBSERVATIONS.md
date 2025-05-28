# Observations on the Prayer Translation Switcher Behavior

This document outlines findings regarding the prayer translation switcher in the Devotional PWA, specifically why it might appear to be missing or broken.

## Current Symptom

*   The HTML `div` element intended for the translation switcher (`<div id="prayer-translations-switcher-area" class="translations-switcher">`) is correctly created and inserted into the DOM.
*   Its CSS styles, including `display: flex;`, are generally applied correctly, meaning the container itself is not hidden by CSS rules like `display: none;`.
*   The primary issue is that the `innerHTML` of this `div` is often empty. This makes the switcher *appear* to be missing because it contains no visible content (like the "Translations:" label or language links), even though the container `div` itself is present.

## Core Logic for Populating the Switcher

The decision to populate the switcher with content or leave its `innerHTML` empty occurs within the `_renderPrayerContent` function in `app.js`. The key logic is:

1.  **`phelpsCodeForSwitcher` Determination:**
    *   A variable `phelpsCodeForSwitcher` is derived from `phelpsCodeForNav` (if navigating via a URL like `#prayercode/PHELPS_CODE/LANG_CODE`) or `phelpsToDisplay` (derived from the current prayer's `phelps` data or suggestions from the prayer matching tool).

2.  **Condition 1: Valid Phelps Code:**
    *   The code checks if `phelpsCodeForSwitcher` is truthy (not null, not empty) AND does not start with `"TODO"`.
    *   If this condition fails, `translationsAreaDiv.innerHTML` is set to `''`.

3.  **Condition 2: Multiple Translations Exist:**
    *   If the Phelps code is valid, a SQL query is executed: `SELECT DISTINCT language FROM writings WHERE phelps = '${phelpsCodeForSwitcher}' ...`.
    *   The code then checks if the number of `distinctLangs` returned by this query is greater than 1.
    *   If 0 or 1 distinct languages are found, `translationsAreaDiv.innerHTML` is set to `''`.

4.  **Successful Population:**
    *   Only if *both* conditions above are met (valid Phelps code AND >1 distinct language found) will the `translationsAreaDiv.innerHTML` be populated with the "Translations:" label and the respective language links.

## Impact of Recent Refactor (Commit `42107170c9b0`)

A major refactor introduced `renderPageLayout` and restructured how views like the prayer page are rendered.
*   The core logic for determining `phelpsCodeForSwitcher` and the conditions for querying and displaying translations (valid Phelps code, >1 language) were preserved and moved from the older `renderPrayer` function into the new `_renderPrayerContent` renderer.
*   The passing of parameters (like `versionId`, `phelpsCodeForNav`, `activeLangForNav`) through the new function call chain (`handleRouteChange` -> `renderPrayer` -> `renderPageLayout` -> `_renderPrayerContent`) appears to be correctly implemented for the data dependencies of the translation switcher.
*   No obvious errors due to incorrect function names or miswired parameters directly affecting this feature's core data dependencies were identified in the refactor. The fundamental decision-making process for showing the switcher remains consistent with pre-refactor versions.

## Most Likely Causes for an Empty Switcher

Given that the core logic for the switcher's visibility has not fundamentally changed, the most probable reasons for it appearing empty are related to the data for the specific prayer being viewed:

1.  **Invalid or Missing `phelpsCodeForSwitcher`:**
    *   The specific prayer version being displayed does not have a valid `phelps` code assigned to it in the database.
    *   The prayer matching tool logic might be influencing `phelpsToDisplay` to become `null` or a "TODO" value based on pinned items or collected matches.
    *   This leads to the first condition (`phelpsCodeForSwitcher && !phelpsCodeForSwitcher.startsWith("TODO")`) failing.

2.  **Insufficient Number of Translations in Database:**
    *   The `phelpsCodeForSwitcher`, even if valid, does not have more than one distinct language associated with it in the `writings` table.
    *   The SQL query for `distinctLangs` returns 0 or 1 results, causing the second condition (`distinctLangs.length > 1`) to fail.

## Ruled Out / Less Likely Causes

*   **CSS Hiding the Container:** The container `div` itself is generally not being hidden by `display: none;`. The issue is its empty content.
*   **MDL `tabs.js` Error:** A previously observed `tabs.js` error was related to the main language picker tabs and has been addressed. It is unlikely to be the root cause for an empty `innerHTML` in the prayer-specific translation switcher.

## Next Recommended Diagnostic Step

To definitively determine why the switcher is empty for a specific prayer, the most effective next step is to add temporary `console.log` statements within the `_renderPrayerContent` function. These logs should inspect:
*   The value of `phelpsCodeForNav` and `phelpsToDisplay`.
*   The final value of `phelpsCodeForSwitcher`.
*   The exact SQL query string generated for `distinctLangs`.
*   The `distinctLangs` array returned from `executeQuery`.
*   Which specific conditional branch leads to `translationsAreaDiv.innerHTML = '';`.

This runtime information will clarify whether the issue stems from an invalid/missing input Phelps code or from the database not having multiple translations linked under the determined Phelps code.