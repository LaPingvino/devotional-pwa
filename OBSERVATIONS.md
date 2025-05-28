# To-Do List for Devotional PWA Enhancements

1.  **Fix Prayer Page Action Buttons:**
    *   Investigate and resolve why the "Pin this Prayer" button (and potentially other dynamically created action buttons like "Suggest Phelps Code", "Change Language", "Add Note", etc., in the `actionsDiv` on the prayer page) are not firing their `click` event handlers.
    *   Ensure MDL component upgrades are correctly applied and event listeners are functional.

2.  **Verify Prayer Matching Tool Link Update:**
    *   Once the "Pin this Prayer" button is working, verify that the "Return to [Language Name] Prayers" link in the pinned prayer section of the Prayer Matching Helper tool correctly displays the full language name instead of the language code. (This change was made, but testing was blocked by the button issue).

3.  **Implement English Prayer Pre-caching:**
    *   On application load, or on first use of the search feature, check if the local prayer cache contains a baseline set of English prayers.
    *   If not, fetch a reasonable number (e.g., 20-50) of English prayers (perhaps a random selection or most common ones if such data exists) and add them to the local cache (`localStorage`) to improve the initial utility of the client-side search.

4.  **Integrate Markdown Parser for Prayer Texts:**
    *   Select and integrate a JavaScript Markdown parsing library (e.g., Showdown.js, Marked.js, or a suitable lightweight alternative).
    *   Update the prayer rendering logic (`_renderPrayerContent`, `createPrayerCardHtml`, `_fetchAndDisplayRandomPrayer`) to parse prayer text content from Markdown to HTML before displaying it. This will ensure proper formatting for line breaks, emphasis, lists, etc., if prayers in the database use Markdown.

5.  **Address User-Noted Minor Issue (Details Pending):**
    *   User has mentioned a minor, non-critical issue observed. This is a placeholder to investigate and address it once details are provided and if it persists after other changes.