// DATABASE STRUCTURE ASSUMPTIONS (holywritings/bahaiwritings):
// Table: writings
// Columns: version, text, language, phelps, name, source, link
// Table: languages
// Columns: langcode, inlang, name

const DOLTHUB_API_BASE_URL =
  "https://www.dolthub.com/api/v1alpha1/holywritings/bahaiwritings/main?q=";
const DOLTHUB_REPO_QUERY_URL_BASE =
  "https://www.dolthub.com/repositories/holywritings/bahaiwritings/query/main?q=";
const DOLTHUB_REPO_ISSUES_NEW_URL_BASE =
  "https://www.dolthub.com/repositories/holywritings/bahaiwritings/issues/new";

const MAX_DIRECT_LINKS_IN_HEADER = 4;
const ITEMS_PER_PAGE = 20;
const LOCALSTORAGE_PRAYER_CACHE_PREFIX = "hw_prayer_cache_";
const FAVORITES_STORAGE_KEY = "hw_favorite_prayers";
const MAX_PREVIEW_LENGTH = 120; // Max length for card preview text
const LANGUAGE_NAMES_CACHE_KEY = "hw_language_names_cache";
const LANGUAGE_NAMES_CACHE_EXPIRY_MS = 24 * 60 * 60 * 1000; // 1 day
const FETCH_LANG_NAMES_TIMEOUT_MS = 5000; // 5 seconds

// --- Language Statistics Cache ---
const LANGUAGE_STATS_CACHE_KEY = "devotionalPWA_languageStats";
const LANGUAGE_STATS_CACHE_EXPIRY_MS = 1 * 60 * 60 * 1000; // 1 hour
// --- End Language Statistics Cache ---

// --- Recent Language Storage ---
const RECENT_LANGUAGES_KEY = "devotionalPWA_recentLanguages";
const MAX_RECENT_LANGUAGES = 4; // Number of recent languages to store

function getRecentLanguages() {
  try {
    const recentLanguagesJSON = localStorage.getItem(RECENT_LANGUAGES_KEY);
    if (recentLanguagesJSON) {
      const languages = JSON.parse(recentLanguagesJSON);
      if (Array.isArray(languages)) {
        return languages;
      }
    }
  } catch (error) {
    console.error("Error getting recent languages from localStorage:", error);
    // Clear potentially corrupted data
    localStorage.removeItem(RECENT_LANGUAGES_KEY);
  }
  return []; // Default to empty array if not found or error
}

function addRecentLanguage(langCode) {
  if (!langCode || typeof langCode !== 'string') return;

  const code = langCode.toLowerCase(); // Store consistently
  let recentLanguages = getRecentLanguages();

  // Remove the language if it already exists to move it to the front
  recentLanguages = recentLanguages.filter(l => l !== code);

  // Add the new language to the beginning
  recentLanguages.unshift(code);

  // Trim the list to the maximum allowed number
  if (recentLanguages.length > MAX_RECENT_LANGUAGES) {
    recentLanguages = recentLanguages.slice(0, MAX_RECENT_LANGUAGES);
  }

  try {
    localStorage.setItem(RECENT_LANGUAGES_KEY, JSON.stringify(recentLanguages));
  } catch (error) {
    console.error("Error saving recent languages to localStorage:", error);
  }
}
// --- End Recent Language Storage ---

const contentDiv = document.getElementById("content");
const prayerLanguageNav = document.getElementById("prayer-language-nav");
const drawerPrayerLanguageNav = document.getElementById(
  "drawer-prayer-language-nav",
);

let currentPageByLanguage = {}; // { langCode: { page, showOnlyUnmatched } }
let currentPageBySearchTerm = {}; // { searchTerm: page }
let currentPageByPhelpsCode = {}; // { phelpsCode: page }
let currentPageByPhelpsLangCode = {}; // { phelps/lang : page } - not used yet for pagination but good for state

let pinnedPrayerDetails = null; // Stores { version, phelps, name, language, text }
let collectedMatchesForEmail = []; // Array of objects: { type?, pinned?, current?, prayer?, newPhelps?, newLanguage?, newName?, note?, description }
let favoritePrayers = []; // Stores array of {version, name, language, phelps}

let languageNamesMap = {}; // To store { langcode_lowercase: { user_lang_name: 'Localized Name', en_name: 'English Name' } }
let languageNamesPromise = null;
let browserLang = (navigator.language || navigator.userLanguage || "en")
  .split("-")[0]
  .toLowerCase();

async function _fetchLanguageNamesInternal() {
  // Try to load from cache first
  try {
    const cachedData = localStorage.getItem(LANGUAGE_NAMES_CACHE_KEY);
    if (cachedData) {
      const { timestamp, data } = JSON.parse(cachedData);
      if (Date.now() - timestamp < LANGUAGE_NAMES_CACHE_EXPIRY_MS) {
        languageNamesMap = data; // Update global map
        // console.log("Loaded language names from cache.");
        return languageNamesMap;
      } else {
        // console.log("Language names cache expired.");
        localStorage.removeItem(LANGUAGE_NAMES_CACHE_KEY);
      }
    }
  } catch (e) {
    console.error("Error reading language names from cache:", e);
    localStorage.removeItem(LANGUAGE_NAMES_CACHE_KEY);
  }

  // If cache is not available or expired, fetch from API
  const userLangForQuery = browserLang.replace(/\'/g, "''");
  const sql = `SELECT langcode, inlang, name FROM languages WHERE inlang = '${userLangForQuery}' OR inlang = 'en'`;

  let fetchCompleted = false;
  const currentFetchAttemptMap = {}; // Use a temporary map for this specific fetch

  const fetchOperationPromise = (async () => {
    const rows = await executeQuery(sql);
    fetchCompleted = true;
    if (rows && Array.isArray(rows)) {
      rows.forEach((row) => {
        const lc = row.langcode.toLowerCase();
        if (!currentFetchAttemptMap[lc]) {
          currentFetchAttemptMap[lc] = {};
        }
        if (row.inlang.toLowerCase() === browserLang) {
          currentFetchAttemptMap[lc].user_lang_name = row.name;
        }
        if (row.inlang.toLowerCase() === "en") {
          currentFetchAttemptMap[lc].en_name = row.name;
        }
      });
      languageNamesMap = currentFetchAttemptMap; // Update global map on success
      try {
        localStorage.setItem(
          LANGUAGE_NAMES_CACHE_KEY,
          JSON.stringify({ timestamp: Date.now(), data: languageNamesMap }),
        );
        // console.log("Fetched and cached language names.");
      } catch (e) {
        console.error("Error caching language names:", e);
      }
      return languageNamesMap;
    } else {
      console.warn("No rows returned from language names query or invalid format. Language map may be empty.");
      languageNamesMap = {}; // Set global map to empty if no data
      return languageNamesMap;
    }
  })();

  const timeoutPromise = new Promise((resolve, reject) => {
    setTimeout(() => {
      if (!fetchCompleted) {
        console.warn(`Fetching language names timed out after ${FETCH_LANG_NAMES_TIMEOUT_MS / 1000}s.`);
        reject(new Error("Language names fetch timed out"));
      }
      // If fetchOperationPromise already completed, this timeout is a no-op for Promise.race
      // It will resolve/reject based on fetchOperationPromise.
      // To prevent an unhandled rejection if timeoutPromise is slower than a successful fetch,
      // we can resolve it harmlessly if fetch is already completed.
      // However, Promise.race handles this: it settles as soon as one promise settles.
    }, FETCH_LANG_NAMES_TIMEOUT_MS);
  });

  try {
    return await Promise.race([fetchOperationPromise, timeoutPromise]);
  } catch (error) {
    // Do not modify global languageNamesMap here if _fetchLanguageNamesInternal fails.
    // The caller (fetchLanguageNames wrapper) will handle resetting languageNamesPromise.
    // console.error("_fetchLanguageNamesInternal: Error during fetch operation (API or timeout):", error);
    throw error; // Propagate error
  }
}

async function fetchLanguageNames() {
  if (!languageNamesPromise) {
    // console.log("Initiating new language names fetch operation.");
    languageNamesPromise = _fetchLanguageNamesInternal()
      .then(names => {
        // _fetchLanguageNamesInternal already updated the global languageNamesMap on its success.
        // console.log("Language names fetched successfully via promise.");
        return names; // Return the map
      })
      .catch(error => {
        console.error("fetchLanguageNames: Failed to fetch language names.", error.message);
        languageNamesPromise = null; // Reset promise on failure to allow retries
        // languageNamesMap will retain its previous state (either old data or empty if never succeeded)
        throw error; // Re-throw so callers can handle it if necessary
      });
  } else {
    // console.log("Returning existing language names promise.");
  }
  return languageNamesPromise;
}

async function getLanguageDisplayName(langCode) {
  if (!langCode || typeof langCode !== "string") return "N/A";

  try {
    // Ensure language names are loaded or an attempt has been made.
    // languageNamesMap will be populated by fetchLanguageNames if successful.
    await fetchLanguageNames();
  } catch (error) {
    // Error is already logged by fetchLanguageNames or _fetchLanguageNamesInternal.
    // We can proceed to lookup in languageNamesMap, which might be stale or empty.
    // console.warn(`getLanguageDisplayName: Could not ensure language names were loaded for '${langCode}'. Fallback may be used. Error: ${error.message}`);
  }

  const lc = langCode.toLowerCase();
  // Access the global languageNamesMap which fetchLanguageNames populates.
  const langData = languageNamesMap[lc];

  if (langData) {
    if (langData.user_lang_name) {
      return langData.user_lang_name;
    }
    if (langData.en_name) {
      return langData.en_name;
    }
  }
  // Fallback to original langCode if no specific name found
  // This can happen if the langCode is not in languageNamesMap or if the map entries don't have user_lang_name or en_name.
  // It might also happen if fetchLanguageNames() failed and languageNamesMap is empty or stale.
  if (!langData || (!langData.user_lang_name && !langData.en_name)) {
    console.warn(`getLanguageDisplayName: Display name not found for language code \'${langCode}\'. Returning original code.`);
  }
  return langCode;
}

function getDomain(url) {
  if (!url) return "";
  try {
    let fullUrl = url;
    if (!url.startsWith("http://") && !url.startsWith("https://")) {
      fullUrl = "http://" + url;
    }
    return new URL(fullUrl).hostname.replace(/^www\./, "");
  } catch (e) {
    return "";
  }
}

function uuidToBase36(uuid_string) {
  if (!uuid_string || typeof uuid_string !== "string")
    return "INVALID_UUID_INPUT";
  const hexString = uuid_string.replace(/-/g, "");
  try {
    if (!/^[0-9a-fA-F]+$/.test(hexString)) {
      console.error("Invalid characters in UUID hex string:", hexString);
      return "INVALID_HEX_IN_UUID";
    }
    return BigInt("0x" + hexString).toString(36);
  } catch (e) {
    console.error("Error converting UUID to Base36:", uuid_string, e);
    return "CONV_ERR_" + hexString.substring(0, 8);
  }
}

// --- Favorite Prayer Functions ---
function loadFavoritePrayers() {
  const storedFavorites = localStorage.getItem(FAVORITES_STORAGE_KEY);
  favoritePrayers = storedFavorites ? JSON.parse(storedFavorites) : [];
}

function saveFavoritePrayers() {
  localStorage.setItem(FAVORITES_STORAGE_KEY, JSON.stringify(favoritePrayers));
}

function isPrayerFavorite(versionId) {
  return favoritePrayers.some((fav) => fav.version === versionId);
}

function toggleFavoritePrayer(prayerData) {
  const snackbarContainer = document.querySelector(".mdl-js-snackbar");
  const index = favoritePrayers.findIndex(
    (fav) => fav.version === prayerData.version,
  );
  let message = "";

  if (index > -1) {
    favoritePrayers.splice(index, 1); // Remove
    message = "Prayer removed from favorites.";
  } else {
    favoritePrayers.push({
      version: prayerData.version,
      name: prayerData.name,
      language: prayerData.language,
      phelps: prayerData.phelps || null,
    });
    message = "Prayer added to favorites.";
  }
  saveFavoritePrayers();

  if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
    snackbarContainer.MaterialSnackbar.showSnackbar({
      message: message,
    });
  }
  handleRouteChange();
}

// --- Prayer Matching Helper Functions ---
function updatePrayerMatchingToolDisplay() {
  const pinnedSection = document.getElementById("pinned-prayer-section");
  const itemsListUl = document.getElementById(
    // Renamed from matchesListUl
    "collected-items-list",
  );
  const reviewAndSendButton = document.getElementById(
    // Was doltHubIssueButton
    "review-and-send-button",
  );
  const clearAllLink = document.getElementById(
    // Was clearMatchesButton
    "clear-all-items-link",
  );

  const snackbarContainer = document.querySelector(".mdl-js-snackbar");

  if (pinnedPrayerDetails) {
    let pinnedName =
      pinnedPrayerDetails.name || `Prayer ${pinnedPrayerDetails.version}`;
    let metaText = `<strong>Pinned Prayer:</strong> ${pinnedName} (Ver: <a href="#prayer/${pinnedPrayerDetails.version}">${pinnedPrayerDetails.version}</a>, Lang: ${pinnedPrayerDetails.language}`;
    if (pinnedPrayerDetails.phelps) {
      metaText += `, Phelps: <a href="#prayercode/${pinnedPrayerDetails.phelps}">${pinnedPrayerDetails.phelps}</a>`;
    }
    metaText += `)`;

    const pMeta = document.createElement("p");
    pMeta.innerHTML = metaText;

    const unpinButton = document.createElement("button");
    unpinButton.className =
      "mdl-button mdl-js-button mdl-button--icon mdl-button--colored";
    unpinButton.innerHTML = '<i class="material-icons">cancel</i>';
    unpinButton.title = "Unpin Prayer (Item list preserved)";
    unpinButton.onclick = unpinPrayer;

    pinnedSection.innerHTML = "";
    pinnedSection.appendChild(pMeta);
    pMeta.appendChild(unpinButton);

    if (pinnedPrayerDetails.text) {
      const prayerTextDiv = document.createElement("div");
      prayerTextDiv.id = "pinned-prayer-text-display";
      prayerTextDiv.textContent = pinnedPrayerDetails.text;
      pinnedSection.appendChild(prayerTextDiv);
    }
    if (pinnedPrayerDetails.language) {
      let pageState = currentPageByLanguage[pinnedPrayerDetails.language];
      let returnUrlParams = pageState
        ? `?page=${pageState.page || 1}${pageState.showOnlyUnmatched ? "&filter=unmatched" : ""}`
        : `?page=1`;
      const returnLinkHref = `#prayers/${pinnedPrayerDetails.language}${returnUrlParams}`;
      const pinnedNameSnippet = (
        pinnedPrayerDetails.name || `Version ${pinnedPrayerDetails.version}`
      ).substring(0, 30);

      const returnLinkP = document.createElement("p");
      returnLinkP.style.fontSize = "0.9em";
      returnLinkP.style.marginTop = "10px";
      returnLinkP.innerHTML = `To continue finding items for this language, return to <a href="${returnLinkHref}">${pinnedPrayerDetails.language.toUpperCase()} Prayers</a>.`;
      pinnedSection.appendChild(returnLinkP);
    }
    if (typeof componentHandler !== "undefined") {
      componentHandler.upgradeElement(unpinButton);
    }
  } else {
    pinnedSection.innerHTML =
      '<p>No prayer is currently pinned. Navigate to a prayer and click "Pin this Prayer" to start.</p>';
  }

  itemsListUl.innerHTML = ""; // Renamed from matchesListUl
  const hasItems = collectedMatchesForEmail.length > 0;
  if (hasItems) {
    collectedMatchesForEmail.forEach((itemData, index) => {
      // Renamed from matchData
      const li = document.createElement("li");
      const textSpan = document.createElement("span");
      textSpan.className = "match-text";
      textSpan.textContent = itemData.description; // Renamed from matchData
      li.appendChild(textSpan);

      const removeButton = document.createElement("button");
      removeButton.className = "mdl-button mdl-js-button mdl-button--icon";
      removeButton.innerHTML = '<i class="material-icons">close</i>';
      removeButton.title = "Remove this item"; // Changed from "match"
      removeButton.onclick = () => removeCollectedMatch(index); // "Match" in function name is fine
      li.appendChild(removeButton);
      itemsListUl.appendChild(li); // Renamed from matchesListUl
      if (typeof componentHandler !== "undefined") {
        componentHandler.upgradeElement(removeButton);
      }
    });
  } else {
    const li = document.createElement("li");
    li.textContent = "No items collected yet."; // Changed from "matches"
    itemsListUl.appendChild(li); // Renamed from matchesListUl
  }

  if (reviewAndSendButton) reviewAndSendButton.disabled = !hasItems;
  if (clearAllLink) {
    const hasSomethingToClear =
      collectedMatchesForEmail.length > 0 || pinnedPrayerDetails;
    clearAllLink.style.display = hasSomethingToClear ? "inline-block" : "none";
  }

  if (typeof componentHandler !== "undefined") {
    if (
      reviewAndSendButton &&
      reviewAndSendButton.classList.contains("mdl-js-button")
    ) {
      componentHandler.upgradeElement(reviewAndSendButton);
    }
    // clearAllLink is an <a> and not an MDL button, so no upgrade needed unless styled as one
  }
}

function removeCollectedMatch(index) {
  collectedMatchesForEmail.splice(index, 1);
  updatePrayerMatchingToolDisplay();
  const snackbarContainer = document.querySelector(".mdl-js-snackbar");
  if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
    snackbarContainer.MaterialSnackbar.showSnackbar({
      message: "Item removed from list.", // Changed from "Match"
    });
  }
}

function pinPrayer(prayerData) {
  pinnedPrayerDetails = {
    version: prayerData.version,
    phelps: prayerData.phelps || null,
    name: prayerData.name || `Prayer ${prayerData.version}`,
    language: prayerData.language,
    text: prayerData.text || "Text not available.",
  };
  updatePrayerMatchingToolDisplay();
  handleRouteChange();
}

function unpinPrayer() {
  const wasPinned = !!pinnedPrayerDetails;
  pinnedPrayerDetails = null;
  updatePrayerMatchingToolDisplay(); // Will update the pinned section and clearAllLink visibility
  // handleRouteChange(); // Not strictly necessary for unpin alone if view doesn't change
  const snackbarContainer = document.querySelector(".mdl-js-snackbar");
  if (wasPinned && snackbarContainer && snackbarContainer.MaterialSnackbar) {
    snackbarContainer.MaterialSnackbar.showSnackbar({
      message: "Prayer unpinned. Item list preserved.",
    });
  }
}

function addCurrentPrayerAsMatch(currentPrayerData) {
  if (!pinnedPrayerDetails) {
    alert("Error: No prayer is pinned to match against.");
    return;
  }
  if (pinnedPrayerDetails.version === currentPrayerData.version) {
    alert("Cannot match a prayer with itself.");
    return;
  }

  const p1 = pinnedPrayerDetails;
  const p2_for_match = {
    version: currentPrayerData.version,
    phelps: currentPrayerData.phelps || null,
    name: currentPrayerData.name || `Prayer ${currentPrayerData.version}`,
    language: currentPrayerData.language,
  };

  let emailMatchDetail = "";
  if (p1.phelps && !p2_for_match.phelps) {
    emailMatchDetail = `Pinned (Phelps ${p1.phelps}, V:${p1.version}) matches Current (V:${p2_for_match.version}). Propose ${p1.phelps} for Current.`;
  } else if (!p1.phelps && p2_for_match.phelps) {
    emailMatchDetail = `Pinned (V:${p1.version}) matches Current (Phelps ${p2_for_match.phelps}, V:${p2_for_match.version}). Propose ${p2_for_match.phelps} for Pinned.`;
  } else if (!p1.phelps && !p2_for_match.phelps) {
    const tempPhelps = `TODO${uuidToBase36(p1.version)}`;
    emailMatchDetail = `Pinned (V:${p1.version}) matches Current (V:${p2_for_match.version}). Propose ${tempPhelps} for both.`;
  } else {
    // Both have Phelps codes
    emailMatchDetail = `Pinned (Phelps ${p1.phelps}, V:${p1.version}) matches Current (Phelps ${p2_for_match.phelps}, V:${p2_for_match.version}). This suggests they are the same prayer.`;
    if (p1.phelps !== p2_for_match.phelps) {
      emailMatchDetail += ` WARNING: Phelps codes differ! (${p1.phelps} vs ${p2_for_match.phelps})`;
    }
  }

  collectedMatchesForEmail.push({
    type: "match_prayers",
    pinned: p1,
    current: p2_for_match,
    description: emailMatchDetail,
  });
  updatePrayerMatchingToolDisplay();
  handleRouteChange();

  const snackbarContainer = document.querySelector(".mdl-js-snackbar");
  if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
    snackbarContainer.MaterialSnackbar.showSnackbar({
      message: "Item added to list.", // Changed from "Match"
      timeout: 5000,
    });
  }
}

function addPhelpsCodeToMatchList(prayerData, phelpsCode) {
  if (!phelpsCode || phelpsCode.trim() === "") {
    alert("Phelps code cannot be empty.");
    return;
  }
  const description = `Assign Phelps [${phelpsCode}] to ${prayerData.name || "V:" + prayerData.version} (Lang: ${prayerData.language}, V: ${prayerData.version})`;
  collectedMatchesForEmail.push({
    type: "assign_phelps",
    prayer: { ...prayerData },
    newPhelps: phelpsCode.trim(),
    description: description,
  });
  updatePrayerMatchingToolDisplay();
  handleRouteChange();

  const snackbarContainer = document.querySelector(".mdl-js-snackbar");
  if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
    snackbarContainer.MaterialSnackbar.showSnackbar({
      message: `Phelps code ${phelpsCode} suggestion added.`,
    });
  }
}

function addLanguageChangeToMatchList(prayerData, newLanguageCode) {
  if (!newLanguageCode || newLanguageCode.trim() === "") {
    alert("Language code cannot be empty.");
    return;
  }
  const trimmedLangCode = newLanguageCode.trim();
  const description = `Change language of ${prayerData.name || "V:" + prayerData.version} (V: ${prayerData.version}) from ${prayerData.language.toUpperCase()} to [${trimmedLangCode.toUpperCase()}]`;
  collectedMatchesForEmail.push({
    type: "change_language",
    prayer: { ...prayerData },
    newLanguage: trimmedLangCode.toLowerCase(), // Store consistently
    description: description,
  });
  updatePrayerMatchingToolDisplay();
  handleRouteChange(); // Language change might affect display or nav

  const snackbarContainer = document.querySelector(".mdl-js-snackbar");
  if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
    snackbarContainer.MaterialSnackbar.showSnackbar({
      message: `Language change to ${trimmedLangCode.toUpperCase()} suggestion added.`,
    });
  }
}

function addNameChangeToMatchList(prayerData, newName) {
  const trimmedName = newName.trim();
  const description = `Change name of V:${prayerData.version} (Lang: ${prayerData.language.toUpperCase()}) to "${trimmedName}" (was: "${prayerData.name || "N/A"}")`;
  collectedMatchesForEmail.push({
    type: "change_name",
    prayer: { ...prayerData },
    newName: trimmedName,
    description: description,
  });
  updatePrayerMatchingToolDisplay();
  handleRouteChange(); // Name change will affect display

  const snackbarContainer = document.querySelector(".mdl-js-snackbar");
  if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
    snackbarContainer.MaterialSnackbar.showSnackbar({
      message: `Name change to "${trimmedName}" suggestion added.`,
    });
  }
}

function addNoteToMatchList(prayerData, noteText) {
  if (!noteText || noteText.trim() === "") {
    alert("Note cannot be empty.");
    return;
  }
  const trimmedNote = noteText.trim();
  const description = `Note for ${prayerData.name || "V:" + prayerData.version} (V: ${prayerData.version}, Lang: ${prayerData.language.toUpperCase()}): "${trimmedNote}"`;
  collectedMatchesForEmail.push({
    type: "add_note",
    prayer: { ...prayerData },
    note: trimmedNote,
    description: description,
  });
  updatePrayerMatchingToolDisplay();

  const snackbarContainer = document.querySelector(".mdl-js-snackbar");
  if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
    snackbarContainer.MaterialSnackbar.showSnackbar({
      message: `Note added to items list.`, // Changed
    });
  }
}

function generateSqlUpdates(forEmailWithComments = true) {
  const sqlUpdateQueries = [];

  collectedMatchesForEmail.forEach((matchData) => {
    let comment = "";
    if (matchData.type === "assign_phelps") {
      if (forEmailWithComments)
        comment = ` -- Assign Phelps ${matchData.newPhelps} to ${matchData.prayer.version} (${matchData.prayer.language})`;
      sqlUpdateQueries.push(
        `UPDATE writings SET phelps = '${matchData.newPhelps.replace(/'/g, "''")}' WHERE version = '${matchData.prayer.version}';` +
          comment,
      );
    } else if (matchData.type === "match_prayers") {
      const p1 = matchData.pinned;
      const p2 = matchData.current;

      if (p1.phelps && !p2.phelps) {
        if (forEmailWithComments)
          comment = ` -- Auto-assign from pinned ${p1.version} (Phelps: ${p1.phelps}) to ${p2.version} (${p2.language})`;
        sqlUpdateQueries.push(
          `UPDATE writings SET phelps = '${p1.phelps.replace(/'/g, "''")}' WHERE version = '${p2.version}';` +
            comment,
        );
      } else if (!p1.phelps && p2.phelps) {
        if (forEmailWithComments)
          comment = ` -- Auto-assign from current ${p2.version} (Phelps: ${p2.phelps}) to ${p1.version} (${p1.language})`;
        sqlUpdateQueries.push(
          `UPDATE writings SET phelps = '${p2.phelps.replace(/'/g, "''")}' WHERE version = '${p1.version}';` +
            comment,
        );
      } else if (!p1.phelps && !p2.phelps) {
        const base36Version = uuidToBase36(p1.version);
        const fakePhelps = `TODO${base36Version}`;
        let comment1 = "",
          comment2 = "";
        if (forEmailWithComments) {
          comment1 = ` -- Assign new temp Phelps: ${p1.version} (${p1.language}) linked with ${p2.version} (${p2.language}) using ${fakePhelps}`;
          comment2 = ` -- Assign new temp Phelps: ${p2.version} (${p2.language}) linked with ${p1.version} (${p1.language}) using ${fakePhelps}`;
        }
        sqlUpdateQueries.push(
          `UPDATE writings SET phelps = '${fakePhelps}' WHERE version = '${p1.version}';` +
            comment1,
        );
        sqlUpdateQueries.push(
          `UPDATE writings SET phelps = '${fakePhelps}' WHERE version = '${p2.version}';` +
            comment2,
        );
      }
    } else if (matchData.type === "change_language") {
      if (forEmailWithComments)
        comment = ` -- Change language for ${matchData.prayer.version} from ${matchData.prayer.language} to ${matchData.newLanguage}`;
      sqlUpdateQueries.push(
        `UPDATE writings SET language = '${matchData.newLanguage.replace(/'/g, "''")}' WHERE version = '${matchData.prayer.version}';` +
          comment,
      );
    } else if (matchData.type === "change_name") {
      if (forEmailWithComments)
        comment = ` -- Change name for ${matchData.prayer.version} to "${matchData.newName}"`;
      sqlUpdateQueries.push(
        `UPDATE writings SET name = '${matchData.newName.replace(/'/g, "''")}' WHERE version = '${matchData.prayer.version}';` +
          comment,
      );
    }
    // "add_note" type intentionally does not generate SQL
  });
  return sqlUpdateQueries;
}

function sendMatchesByEmail() {
  if (collectedMatchesForEmail.length === 0) {
    alert("No items to send."); // Changed
    return;
  }

  const subject = "holywritings.net: Prayer Matches/Suggestions";
  const bodyLines = [
    "Hello,",
    "I've found/suggested the following prayer matches/assignments/changes on holywritings.net:",
    "",
  ];
  collectedMatchesForEmail.forEach((matchData, index) => {
    bodyLines.push(`${index + 1}. ${matchData.description}`);
  });
  bodyLines.push("");
  bodyLines.push("Thank you for maintaining this wonderful resource!");
  bodyLines.push(
    `Base URL for reference: ${window.location.origin}${window.location.pathname}`,
  );
  bodyLines.push("");

  const sqlUpdateQueries = generateSqlUpdates(true);

  if (sqlUpdateQueries.length > 0) {
    bodyLines.push("--- Suggested SQL UPDATE Statements ---");
    sqlUpdateQueries.forEach((q) => bodyLines.push(q));
  } else {
    bodyLines.push(
      "--- No SQL UPDATE Statements Suggested for these items ---",
    );
  }
  bodyLines.push("");

  const mailBody = encodeURIComponent(bodyLines.join("\n"));
  const mailtoLink = `mailto:ikojba@gmail.com?subject=${encodeURIComponent(subject)}&body=${mailBody}`;

  const tempLink = document.createElement("a");
  tempLink.href = mailtoLink;
  document.body.appendChild(tempLink);
  tempLink.click();
  document.body.removeChild(tempLink);
}

function generateDoltHubIssueBody() {
  const title = "Prayer Data Suggestions from Web Tool";
  const bodyLines = [
    "The following prayer data suggestions have been collected using the holywritings.net web tool:",
    "",
  ];
  collectedMatchesForEmail.forEach((matchData, index) => {
    bodyLines.push(`**Suggestion ${index + 1} (Type: ${matchData.type}):**`);
    bodyLines.push(`> ${matchData.description}`);
    if (matchData.type === "match_prayers") {
      bodyLines.push(
        `- Pinned: Version ${matchData.pinned.version} (${matchData.pinned.language}), Phelps: ${matchData.pinned.phelps || "N/A"}`,
      );
      bodyLines.push(
        `- Current: Version ${matchData.current.version} (${matchData.current.language}), Phelps: ${matchData.current.phelps || "N/A"}`,
      );
    } else if (matchData.type === "assign_phelps") {
      bodyLines.push(
        `- Prayer: Version ${matchData.prayer.version} (${matchData.prayer.language})`,
      );
      bodyLines.push(`- Suggested Phelps: ${matchData.newPhelps}`);
    } else if (matchData.type === "change_language") {
      bodyLines.push(
        `- Prayer: Version ${matchData.prayer.version} (Current Lang: ${matchData.prayer.language.toUpperCase()})`,
      );
      bodyLines.push(
        `- Suggested New Language: ${matchData.newLanguage.toUpperCase()}`,
      );
    } else if (matchData.type === "change_name") {
      bodyLines.push(
        `- Prayer: Version ${matchData.prayer.version} (Lang: ${matchData.prayer.language.toUpperCase()})`,
      );
      bodyLines.push(
        `- Suggested New Name: "${matchData.newName}" (Current: "${matchData.prayer.name || "N/A"}")`,
      );
    } else if (matchData.type === "add_note") {
      bodyLines.push(
        `- Prayer: Version ${matchData.prayer.version} (Lang: ${matchData.prayer.language.toUpperCase()}, Name: "${matchData.prayer.name || "N/A"}")`,
      );
      bodyLines.push(`- Note: "${matchData.note}"`);
    }
    bodyLines.push("");
  });

  const sqlUpdateQueries = generateSqlUpdates(false);
  if (sqlUpdateQueries.length > 0) {
    bodyLines.push("--- Suggested SQL UPDATE Statements ---");
    bodyLines.push("```sql");
    sqlUpdateQueries.forEach((q) => bodyLines.push(q));
    bodyLines.push("```");
  } else {
    bodyLines.push("--- No SQL UPDATE Statements Suggested ---");
  }
  bodyLines.push("");
  bodyLines.push(
    `Submitted from: ${window.location.origin}${window.location.pathname} (current hash: ${window.location.hash})`,
  );
  return bodyLines.join("\n");
}

function openDoltHubIssueDialog() {
  if (collectedMatchesForEmail.length === 0) {
    const snackbarContainer = document.querySelector(".mdl-js-snackbar");
    if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
      snackbarContainer.MaterialSnackbar.showSnackbar({
        message: "No items to create an issue for.",
      });
    } else {
      alert("No items to create an issue for.");
    }
    return;
  }

  const issueBodyString = generateDoltHubIssueBody();
  const dialog = document.getElementById("dolthub-issue-dialog");
  const textarea = document.getElementById("dolthub-issue-textarea");

  if (dialog && textarea) {
    textarea.value = issueBodyString;
    if (typeof dialog.showModal === "function") {
      dialog.showModal();
    } else {
      alert("DoltHub Issue Dialog could not be opened.");
    }
  }
}

// --- LocalStorage Cache Functions ---
function cachePrayerText(prayerData) {
  if (
    !prayerData ||
    !prayerData.version ||
    typeof prayerData.text === "undefined"
  ) {
    console.warn("Attempted to cache invalid prayer data", prayerData);
    return;
  }
  try {
    const key = `${LOCALSTORAGE_PRAYER_CACHE_PREFIX}${prayerData.version}`;
    const dataToStore = {
      version: prayerData.version,
      text: prayerData.text,
      name: prayerData.name || null,
      language: prayerData.language || null,
      phelps: prayerData.phelps || null,
      link: prayerData.link || null,
      source: prayerData.source || null,
      timestamp: Date.now(),
    };
    localStorage.setItem(key, JSON.stringify(dataToStore));
  } catch (e) {
    console.error("Error caching prayer text to localStorage:", e);
  }
}

function getCachedPrayerText(versionId) {
  try {
    const key = `${LOCALSTORAGE_PRAYER_CACHE_PREFIX}${versionId}`;
    const storedData = localStorage.getItem(key);
    if (storedData) {
      return JSON.parse(storedData);
    }
  } catch (e) {
    console.error("Error retrieving cached prayer text from localStorage:", e);
  }
  return null;
}

function getAllCachedPrayers() {
  const cachedPrayers = [];
  for (let i = 0; i < localStorage.length; i++) {
    const key = localStorage.key(i);
    if (key && key.startsWith(LOCALSTORAGE_PRAYER_CACHE_PREFIX)) {
      try {
        const item = JSON.parse(localStorage.getItem(key));
        if (item && item.version && typeof item.text !== "undefined") {
          cachedPrayers.push(item);
        }
      } catch (e) {
        console.warn(
          "Error parsing a cached prayer from localStorage:",
          key,
          e,
        );
      }
    }
  }
  return cachedPrayers;
}
// --- End LocalStorage Cache Functions ---

function getCachedLanguageStats(allowStale = false) {
  try {
    const cachedData = localStorage.getItem(LANGUAGE_STATS_CACHE_KEY);
    if (cachedData) {
      const { timestamp, data } = JSON.parse(cachedData);
      if (allowStale || (Date.now() - timestamp < LANGUAGE_STATS_CACHE_EXPIRY_MS)) {
        // console.log(`Language stats loaded from cache ${allowStale && (Date.now() - timestamp >= LANGUAGE_STATS_CACHE_EXPIRY_MS) ? '(stale)' : '(fresh)'}.`);
        return data;
      } else {
        // console.log("Language stats cache expired.");
        localStorage.removeItem(LANGUAGE_STATS_CACHE_KEY);
      }
    }
  } catch (e) {
    console.error("Error reading language stats from cache:", e);
    localStorage.removeItem(LANGUAGE_STATS_CACHE_KEY); // Clear corrupted cache
  }
  return null;
}

function cacheLanguageStats(data) {
  if (!data || !Array.isArray(data)) {
    console.error("Attempted to cache invalid language stats data:", data);
    return;
  }
  try {
    localStorage.setItem(
      LANGUAGE_STATS_CACHE_KEY,
      JSON.stringify({ timestamp: Date.now(), data: data }),
    );
    // console.log("Language stats cached.");
  } catch (e) {
    console.error("Error caching language stats:", e);
  }
}

async function executeQuery(sql) {
  try {
    const response = await fetch(
      DOLTHUB_API_BASE_URL + encodeURIComponent(sql),
    );
    if (!response.ok) {
      console.error("Failing SQL query:", sql); 
      console.error("Encoded Failing SQL query string part:", encodeURIComponent(sql)); 
      throw new Error(`HTTP error! status: ${response.status}`);
    }
    const data = await response.json();
    return data.rows || [];
  } catch (error) {
    console.error("Error executing query:", error);
    // Also log SQL from catch block if it was not logged above (e.g. network error before response.ok check)
    if (!error.message.startsWith("HTTP error!")) { 
        console.error("SQL that failed (network or other error before HTTP status check):", sql);
    }
    // contentDiv.innerHTML = `<p>Error loading data: ${error.message}. Please try again later.</p>`;
    // return []; // Let the caller handle UI and return values on error
    throw error; // Re-throw the error so the caller can handle it
  }
}

function getAuthorFromPhelps(phelpsCode) {
  if (!phelpsCode || typeof phelpsCode !== "string" || phelpsCode.length < 2) {
    return null;
  }
  const prefix = phelpsCode.substring(0, 2).toUpperCase();
  switch (prefix) {
    case "AB":
      return "`Abdu'l-Bahá";
    case "BH":
      return "Bahá'u'lláh";
    case "BB":
      return "The Báb";
    default:
      return null;
  }
}

// --- Prayer Card Generation ---
async function createPrayerCardHtml(prayerData, allPhelpsDetails = {}) {
  const {
    version,
    name,
    language = "N/A",
    phelps,
    opening_text,
    link,
  } = prayerData;

  const displayLanguageForTitle = await getLanguageDisplayName(language);
  let displayTitle =
    name || (phelps ? `${phelps} - ${displayLanguageForTitle}` : `${version} - ${displayLanguageForTitle}`);
  const cardLinkHref = phelps
    ? `#prayercode/${phelps}/${language}` // Keep original 'language' (code) for URL
    : `#prayer/${version}`;
  const previewSnippet = opening_text || "No text preview available.";

  let otherVersionsHtml = "";
  if (phelps && allPhelpsDetails && allPhelpsDetails[phelps]) {
    const allVersionsForThisPhelps = allPhelpsDetails[phelps];
    const groupedByLanguage = {};
    allVersionsForThisPhelps.forEach((v) => {
      if (v.version === version) return;
      if (!groupedByLanguage[v.language]) groupedByLanguage[v.language] = [];
      groupedByLanguage[v.language].push(v);
    });

    let otherLanguageLinks = [];
    let sameLanguageAltVersions = [];
    const sortedLangCodes = Object.keys(groupedByLanguage).sort();

    for (const langCode of sortedLangCodes) {
      if (langCode !== language) {
        otherLanguageLinks.push(
          `<li class="favorite-prayer-card-translations-item"><a href="#prayercode/${phelps}/${langCode}">${langCode.toUpperCase()}</a></li>`,
        );
      } else {
        groupedByLanguage[langCode].forEach((altVersion) => {
          sameLanguageAltVersions.push(
            `<li><a href="#prayer/${altVersion.version}">${altVersion.name || "Version " + altVersion.version}${altVersion.link ? ` (${getDomain(altVersion.link)})` : ""}</a></li>`,
          );
        });
      }
    }

    if (otherLanguageLinks.length > 0 || sameLanguageAltVersions.length > 0) {
      otherVersionsHtml += `<div class="favorite-prayer-card-translations">`;
      if (otherLanguageLinks.length > 0) {
        otherVersionsHtml += `<h5>Translations:</h5><ul class="favorite-prayer-card-translations-list">${otherLanguageLinks.join("")}</ul>`;
      }
      if (sameLanguageAltVersions.length > 0) {
        otherVersionsHtml += `<h5 style="margin-top: ${otherLanguageLinks.length > 0 ? "10px" : "0"};">Other versions in ${language.toUpperCase()}:</h5><ul class="other-versions-in-lang-list">${sameLanguageAltVersions.join("")}</ul>`;
      }
      otherVersionsHtml += `</div>`;
    }
  }

  return `
                <div class="favorite-prayer-card">
                    <div>
                        <div class="favorite-prayer-card-header"><a href="${cardLinkHref}">${displayTitle}</a></div>
                        <p class="favorite-prayer-card-preview">${previewSnippet}</p>
                        <div class="favorite-prayer-card-meta">
                            <span>Lang: ${language.toUpperCase()}</span>
                            ${phelps ? `<span class="phelps-code">Phelps: <a href="#prayercode/${phelps}">${phelps}</a></span>` : ""}
                            ${link ? `<span>Source: <a href="${link}" target="_blank">${getDomain(link) || "Link"}</a></span>` : ""}
                        </div>
                    </div>
                    ${otherVersionsHtml}
                </div>`;
}

async function updateHeaderNavigation(links = []) {
  if (!prayerLanguageNav) return;
  prayerLanguageNav.innerHTML = "";

  if (links.length === 0) {
    const loadingLink = document.createElement("span");
    loadingLink.className = "mdl-navigation__link";
    loadingLink.innerHTML =
      '<span class="star" style="font-size: 1em; color: rgba(255,255,255,0.7); vertical-align: middle;">&#x1f7d9;</span>';
    prayerLanguageNav.appendChild(loadingLink);
    return;
  }

  if (links.length > MAX_DIRECT_LINKS_IN_HEADER) {
    const buttonId = "languages-menu-button";
    let menuButton = document.getElementById(buttonId);
    if (!menuButton) {
      menuButton = document.createElement("button");
      menuButton.id = buttonId;
      menuButton.className = "mdl-button mdl-js-button mdl-button--icon";
      const icon = document.createElement("i");
      icon.className = "material-icons";
      icon.textContent = "language";
      menuButton.appendChild(icon);
      prayerLanguageNav.appendChild(menuButton);
    }

    const existingMenuUl = prayerLanguageNav.querySelector(".mdl-menu");
    if (existingMenuUl) existingMenuUl.remove();

    const menuUl = document.createElement("ul");
    menuUl.className =
      "mdl-menu mdl-menu--bottom-right mdl-js-menu mdl-js-ripple-effect";
    menuUl.setAttribute("for", buttonId);

    links.forEach((linkInfo) => {
      const menuItemLi = document.createElement("li");
      menuItemLi.className = "mdl-menu__item";
      const linkA = document.createElement("a");
      linkA.href = linkInfo.href;
      linkA.textContent = linkInfo.text;
      linkA.style.textDecoration = "none";
      linkA.style.color = "inherit";
      linkA.style.display = "block";
      if (linkInfo.isActive) linkA.style.fontWeight = "bold";
      menuItemLi.appendChild(linkA);
      menuUl.appendChild(menuItemLi);
    });
    prayerLanguageNav.appendChild(menuUl);

    if (typeof componentHandler !== "undefined") {
      if (menuButton.MaterialButton)
        componentHandler.upgradeElement(menuButton);
      if (menuUl.MaterialMenu) componentHandler.upgradeElement(menuUl);
      else componentHandler.upgradeDom();
    }
  } else {
    links.forEach((linkInfo) => {
      const link = document.createElement("a");
      link.className = "mdl-navigation__link";
      link.href = linkInfo.href;
      link.textContent = linkInfo.text;
      if (linkInfo.isActive) {
        link.style.fontWeight = "bold";
        link.style.textDecoration = "underline";
      }
      prayerLanguageNav.appendChild(link);
    });
  }
}

async function updateDrawerLanguageNavigation(links = []) {
  if (!drawerPrayerLanguageNav) return;
  drawerPrayerLanguageNav.innerHTML = "";

  if (links.length === 0) {
    const noLinksMsg = document.createElement("span");
    noLinksMsg.className = "mdl-navigation__link";
    noLinksMsg.innerHTML =
      '<span class="star" style="font-size: 1.2em; color: #757575; display: block; text-align: center; line-height: normal;">&#x1f7d9;</span>';
    noLinksMsg.style.padding = "16px 0";
    drawerPrayerLanguageNav.appendChild(noLinksMsg);
    return;
  }

  links.forEach((linkInfo) => {
    const link = document.createElement("a");
    link.className = "mdl-navigation__link";
    link.href = linkInfo.href;
    link.textContent = linkInfo.text;
    if (linkInfo.isActive) link.style.fontWeight = "bold";
    link.addEventListener("click", () => {
      const layout = document.querySelector(".mdl-layout");
      if (layout && layout.MaterialLayout && layout.MaterialLayout.drawer_) {
        layout.MaterialLayout.toggleDrawer();
      }
    });
    drawerPrayerLanguageNav.appendChild(link);
  });
}

async function renderPrayer(
  versionId,
  phelpsCodeForNav = null,
  activeLangForNav = null,
) {
  contentDiv.innerHTML =
    '<div class="mdl-spinner mdl-js-spinner is-active" style="margin: auto; display: block;"></div>';
  if (typeof componentHandler !== "undefined") componentHandler.upgradeDom();

  updateHeaderNavigation([]);
  updateDrawerLanguageNavigation([]);

  // Determine the active language for the picker before fetching prayer details
  // This is a bit of a chicken-and-egg if activeLangForNav is not provided,
  // as we need the prayer's language. We'll pass activeLangForNav if available,
  // otherwise, the picker will show with no specific language active initially,
  // or we could make a preliminary query for just the prayer's language.

  const sql = `SELECT version, text, language, phelps, name, source, link FROM writings WHERE version = '${versionId}' LIMIT 1`;
  const rows = await executeQuery(sql);

  if (rows.length === 0) {
    const debugQueryUrl = `${DOLTHUB_REPO_QUERY_URL_BASE}${encodeURIComponent(sql)}`;
    contentDiv.innerHTML = `<p>Prayer with ID ${versionId} not found.</p><p>Query used:</p><pre style="white-space: pre-wrap; word-break: break-all; background: #eee; padding: 10px; border-radius: 4px;">${sql}</pre><p><a href="${debugQueryUrl}" target="_blank">Debug this query on DoltHub</a></p>`;
    return;
  }

  const prayer = rows[0];

  // Determine the language to highlight in the picker:
  // Use activeLangForNav if provided (context from URL), otherwise use the prayer's own language.
  const languageToHighlightInPicker = activeLangForNav || prayer.language;
  populateLanguageSelection(languageToHighlightInPicker); // Async populate

  cachePrayerText({ ...prayer });

  const authorName = getAuthorFromPhelps(prayer.phelps);
  const initialDisplayPrayerLanguage = await getLanguageDisplayName(prayer.language);
  let prayerTitle =
    prayer.name ||
    (prayer.phelps ? `${prayer.phelps} - ${initialDisplayPrayerLanguage}` : null) ||
    (prayer.text
      ? prayer.text.substring(0, 50) + "..."
      : `Prayer ${prayer.version}`);

  let phelpsToDisplay = prayer.phelps;
  let phelpsIsSuggested = false;
  let languageToDisplay = prayer.language;
  let languageIsSuggested = false;
  let nameToDisplay = prayer.name;
  let nameIsSuggested = false;

  for (const match of collectedMatchesForEmail) {
    if (match.prayer && match.prayer.version === prayer.version) {
      if (match.type === "assign_phelps") {
        phelpsToDisplay = match.newPhelps;
        phelpsIsSuggested = true;
      } else if (match.type === "change_language") {
        languageToDisplay = match.newLanguage;
        languageIsSuggested = true;
      } else if (match.type === "change_name") {
        nameToDisplay = match.newName;
        nameIsSuggested = true;
        // If languageToDisplay or phelpsToDisplay were also suggested, they are already updated.
        const currentDisplayLanguageForTitle = await getLanguageDisplayName(languageToDisplay);
        prayerTitle =
          nameToDisplay ||
          (phelpsToDisplay
            ? `${phelpsToDisplay} - ${currentDisplayLanguageForTitle}`
            : `Prayer ${prayer.version}`);
      }
    }
    // Phelps suggestions from match_prayers
    if (match.type === "match_prayers") {
      if (
        match.current.version === prayer.version &&
        !prayer.phelps &&
        match.pinned.phelps
      ) {
        phelpsToDisplay = match.pinned.phelps;
        phelpsIsSuggested = true;
      }
      if (
        match.pinned.version === prayer.version &&
        !prayer.phelps &&
        match.current.phelps
      ) {
        phelpsToDisplay = match.current.phelps;
        phelpsIsSuggested = true;
      }
      if (
        !prayer.phelps &&
        (match.pinned.version === prayer.version ||
          match.current.version === prayer.version) &&
        !match.pinned.phelps &&
        !match.current.phelps
      ) {
        phelpsToDisplay = `TODO${uuidToBase36(match.pinned.version)}`;
        phelpsIsSuggested = true;
      }
    }
  }

  const finalDisplayLanguageToDisplay = await getLanguageDisplayName(languageToDisplay);
  let phelpsDisplayHtml = "Not Assigned";
  if (phelpsToDisplay) {
    const textPart = phelpsIsSuggested
      ? `<span style="font-weight: bold; color: red;">${phelpsToDisplay}</span>`
      : `<a href="#prayercode/${phelpsToDisplay}">${phelpsToDisplay}</a>`;
    phelpsDisplayHtml = `${textPart} (Lang: ${finalDisplayLanguageToDisplay})`;
  } else {
    phelpsDisplayHtml = `Not Assigned (Lang: ${finalDisplayLanguageToDisplay})`;
  }
  if (languageIsSuggested) {
    // languageToDisplay here is the *new* language. So finalDisplayLanguageToDisplay is appropriate.
    phelpsDisplayHtml += ` <span style="font-weight: bold; color: red;">(New Lang: ${finalDisplayLanguageToDisplay})</span>`;
  }

  let html = `
                    <header><h2><span id="category">Prayer</span><span id="blocktitle">${prayerTitle}${nameIsSuggested ? ' <em style="color:red; font-size:0.8em">(Suggested Name)</em>' : ""}</span></h2></header>
                    <div class="scripture">
                        <div class="prayer" style="white-space: pre-wrap;">${prayer.text || "No text available."}</div>
                        ${authorName ? `<div class="author">${authorName}</div>` : ""}
                        ${prayer.source ? `<div style="font-size: 0.8em; margin-left: 2em; margin-top: 0.5em; font-style: italic;">Source: ${prayer.source} ${prayer.link ? `(<a href="${prayer.link}" target="_blank">${getDomain(prayer.link) || "link"}</a>)` : ""}</div>` : ""}
                        <div style="font-size: 0.7em; margin-left: 2em; margin-top: 0.3em; color: #555;">Phelps ID: ${phelpsDisplayHtml}</div>
                        <div style="font-size: 0.7em; margin-left: 2em; margin-top: 0.3em; color: #555;">Version ID: ${prayer.version}</div>
                    </div>`;
 contentDiv.innerHTML = getLanguagePickerShellHtml() + html; // Prepend language picker SHELL
 if (typeof componentHandler !== "undefined") componentHandler.upgradeElement(contentDiv.querySelector('.mdl-js-tabs'));


 const favoriteButton = document.createElement("button");
  favoriteButton.className =
    "mdl-button mdl-js-button mdl-button--icon favorite-toggle-button";
  const favoriteIcon = document.createElement("i");
  favoriteIcon.className = "material-icons";
  if (isPrayerFavorite(prayer.version)) {
    favoriteButton.classList.add("is-favorite");
    favoriteIcon.textContent = "star";
    favoriteButton.title = "Remove from Favorites";
  } else {
    favoriteIcon.textContent = "star_border";
    favoriteButton.title = "Add to Favorites";
  }
  favoriteButton.appendChild(favoriteIcon);
  favoriteButton.onclick = () => toggleFavoritePrayer(prayer);
  contentDiv.appendChild(favoriteButton);

  const actionsDiv = document.createElement("div");
  actionsDiv.className = "prayer-actions";

  if (pinnedPrayerDetails) {
    if (pinnedPrayerDetails.version !== prayer.version) {
      const addMatchButton = document.createElement("button");
      addMatchButton.className =
        "mdl-button mdl-js-button mdl-button--raised mdl-button--accent";
      const pinnedNameSnippet = (
        pinnedPrayerDetails.name || `Version ${pinnedPrayerDetails.version}`
      ).substring(0, 20);
      addMatchButton.innerHTML = `<i class="material-icons">playlist_add_check</i>Match with Pinned: ${pinnedNameSnippet}${pinnedNameSnippet.length === 20 ? "..." : ""}`;
      addMatchButton.onclick = () => addCurrentPrayerAsMatch(prayer);
      actionsDiv.appendChild(addMatchButton);

      const replacePinButton = document.createElement("button");
      replacePinButton.className =
        "mdl-button mdl-js-button mdl-button--raised";
      replacePinButton.innerHTML =
        '<i class="material-icons">swap_horiz</i> Replace Pin';
      replacePinButton.title =
        "Replaces the currently pinned prayer with this one. Item list preserved.";
      replacePinButton.onclick = () => {
        pinPrayer(prayer);
        const snackbarContainer = document.querySelector(".mdl-js-snackbar");
        if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
          snackbarContainer.MaterialSnackbar.showSnackbar({
            message: "Pinned prayer replaced. Item list preserved.",
          });
        }
      };
      actionsDiv.appendChild(replacePinButton);
      if (typeof componentHandler !== "undefined") {
        componentHandler.upgradeElement(addMatchButton);
        componentHandler.upgradeElement(replacePinButton);
      }
    } else {
      const p = document.createElement("p");
      p.innerHTML =
        "<em>This prayer is currently pinned. Use the tool on the right to manage items or unpin.</em>";
      actionsDiv.appendChild(p);
    }
  } else {
    const pinButton = document.createElement("button");
    pinButton.className =
      "mdl-button mdl-js-button mdl-button--raised mdl-button--colored";
    pinButton.innerHTML =
      '<i class="material-icons">push_pin</i> Pin this Prayer';
    pinButton.onclick = () => {
      pinPrayer(prayer);
      const snackbarContainer = document.querySelector(".mdl-js-snackbar");
      if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
        snackbarContainer.MaterialSnackbar.showSnackbar({
          message: "Prayer pinned! Navigate to find items or suggestions.",
        });
      }
    };
    actionsDiv.appendChild(pinButton);
    if (typeof componentHandler !== "undefined")
      componentHandler.upgradeElement(pinButton);
  }

  if (!prayer.phelps && !phelpsIsSuggested) {
    const suggestPhelpsButton = document.createElement("button");
    suggestPhelpsButton.className =
      "mdl-button mdl-js-button mdl-button--raised";
    suggestPhelpsButton.innerHTML =
      '<i class="material-icons">library_add</i> Add/Suggest Phelps Code';
    suggestPhelpsButton.onclick = () => {
      const enteredCode = prompt(
        `Enter Phelps code for:\\n${prayer.name || "Version " + prayer.version}\\n(${initialDisplayPrayerLanguage})`,
      );
      if (enteredCode && enteredCode.trim() !== "") {
        addPhelpsCodeToMatchList(prayer, enteredCode.trim());
      }
    };
    actionsDiv.appendChild(suggestPhelpsButton);
    if (typeof componentHandler !== "undefined")
      componentHandler.upgradeElement(suggestPhelpsButton);
  }

  const changeLangButton = document.createElement("button");
  changeLangButton.className = "mdl-button mdl-js-button mdl-button--raised";
  changeLangButton.innerHTML =
    '<i class="material-icons">translate</i> Change Language';
  changeLangButton.title = `Current language: ${finalDisplayLanguageToDisplay}`;
  changeLangButton.onclick = () => {
    const newLang = prompt(
      `Enter new language code for:\\n${nameToDisplay || "Version " + prayer.version}\\n(V: ${prayer.version})\\nCurrent language: ${finalDisplayLanguageToDisplay}`,
      languageToDisplay, // Keep original code as default for prompt input
    );
    if (
      newLang &&
      newLang.trim() !== "" &&
      newLang.trim().toLowerCase() !== languageToDisplay.toLowerCase()
    ) {
      addLanguageChangeToMatchList(prayer, newLang.trim());
    } else if (
      newLang &&
      newLang.trim().toLowerCase() === languageToDisplay.toLowerCase()
    ) {
      alert("New language is the same as the current language.");
    }
  };
  actionsDiv.appendChild(changeLangButton);

  const changeNameButton = document.createElement("button");
  changeNameButton.className = "mdl-button mdl-js-button mdl-button--raised";
  changeNameButton.innerHTML =
    '<i class="material-icons">edit_note</i> Add/Change Name';
  changeNameButton.title = `Current name: ${prayer.name || "Not Set"}`;
  changeNameButton.onclick = () => {
    const newName = prompt(
      `Enter name for:\\nVersion ${prayer.version} (Lang: ${finalDisplayLanguageToDisplay})\\nCurrent name: ${nameToDisplay || "Not Set"}`,
      nameToDisplay || "",
    );
    if (newName !== null) {
      // Allow empty string to clear name
      addNameChangeToMatchList(prayer, newName.trim());
    }
  };
  actionsDiv.appendChild(changeNameButton);

  const addNoteButton = document.createElement("button");
  addNoteButton.className = "mdl-button mdl-js-button mdl-button--raised";
  addNoteButton.innerHTML =
    '<i class="material-icons">speaker_notes</i> Add General Note';
  addNoteButton.onclick = () => {
    const note = prompt(
      `Enter a general note for:\n${nameToDisplay || "Version " + prayer.version} (V: ${prayer.version})`,
    );
    if (note && note.trim() !== "") {
      addNoteToMatchList(prayer, note.trim());
    }
  };
  actionsDiv.appendChild(addNoteButton);

  contentDiv.appendChild(actionsDiv);
  if (typeof componentHandler !== "undefined") {
    componentHandler.upgradeElement(favoriteButton);
    componentHandler.upgradeElement(changeLangButton);
    componentHandler.upgradeElement(changeNameButton);
    componentHandler.upgradeElement(addNoteButton);
    Array.from(actionsDiv.querySelectorAll(".mdl-js-button")).forEach((btn) => {
      if (btn.MaterialButton) componentHandler.upgradeElement(btn);
    });
  }

  const finalPhelpsCode = phelpsCodeForNav || phelpsToDisplay;
  // Use languageToDisplay (which is prayer.language or a suggested one) for header nav context
  const finalActiveLangForHeaderNav = activeLangForNav || languageToDisplay; 

  if (finalPhelpsCode && !finalPhelpsCode.startsWith("TODO")) {
    const transSql = `SELECT DISTINCT language FROM writings WHERE phelps = \'${finalPhelpsCode.replace(/\'/g, "\'\'")}\' AND phelps IS NOT NULL AND phelps != \'\' ORDER BY language`;
    const distinctLangs = await executeQuery(transSql);
    const navLinkPromises = distinctLangs.map(async (langRow) => ({
      text: await getLanguageDisplayName(langRow.language),
      href: `#prayercode/${finalPhelpsCode}/${langRow.language}`,
      isActive: langRow.language === finalActiveLangForHeaderNav,
    }));
    const navLinks = await Promise.all(navLinkPromises);
    updateHeaderNavigation(navLinks);
    updateDrawerLanguageNavigation(navLinks);
  } else {
    // finalDisplayLanguageToDisplay should be in scope from earlier in renderPrayer
    const singleLink = [
      {
        text: finalDisplayLanguageToDisplay, // Uses variable already holding display name
        href: `#prayer/${prayer.version}`,
        isActive: true,
      },
    ];
    updateHeaderNavigation(singleLink);
    updateDrawerLanguageNavigation(singleLink);
  }
}

async function renderPrayersForLanguage(
  langCode,
  page = 1,
  showOnlyUnmatched = false,
) {
  addRecentLanguage(langCode); // Track this language as recently viewed
  currentPageByLanguage[langCode] = { page, showOnlyUnmatched };
  const offset = (page - 1) * ITEMS_PER_PAGE;
  contentDiv.innerHTML =
    '<div class="mdl-spinner mdl-js-spinner is-active" style="margin: auto; display: block;"></div>';
  if (typeof componentHandler !== "undefined") componentHandler.upgradeDom();
  updateHeaderNavigation([]);
  updateDrawerLanguageNavigation([]);

  const languagePickerHtml = getLanguagePickerShellHtml(); // Get shell first
  // Insert shell + spinner BEFORE fetching other data for this view
  contentDiv.innerHTML = languagePickerHtml +
    '<div class="mdl-spinner mdl-js-spinner is-active" style="margin: auto; display: block; margin-top: 20px;"></div>';
  if (typeof componentHandler !== "undefined") componentHandler.upgradeElement(contentDiv.querySelector('.mdl-js-tabs'));

  // Asynchronously populate the picker.
  populateLanguageSelection(langCode); // Fire and forget.

  const languageDisplayName = await getLanguageDisplayName(langCode);
  let filterCondition = showOnlyUnmatched
    ? " AND (phelps IS NULL OR phelps = '')"
    : "";

  const metadataSql = `SELECT version, name, language, phelps, link FROM writings WHERE language = '${langCode}'${filterCondition} ORDER BY name, version LIMIT ${ITEMS_PER_PAGE} OFFSET ${offset}`;
  const prayersMetadata = await executeQuery(metadataSql);

  const countSql = `SELECT COUNT(*) as total FROM writings WHERE language = '${langCode}'${filterCondition}`;
  const countResult = await executeQuery(countSql);
  const totalPrayers = countResult.length > 0 ? countResult[0].total : 0;
  const totalPages = Math.ceil(totalPrayers / ITEMS_PER_PAGE);

  if (prayersMetadata.length === 0 && page === 1) {
    const filterSwitchId = `filter-unmatched-${langCode}`;
    const filterSwitchHtml = `<div class="filter-switch-container"><label class="mdl-switch mdl-js-switch mdl-js-ripple-effect" for="${filterSwitchId}"><input type="checkbox" id="${filterSwitchId}" class="mdl-switch__input" onchange="setLanguageView('${langCode}', 1, this.checked)" ${showOnlyUnmatched ? "checked" : ""}><span class="mdl-switch__label">Show only prayers without Phelps code</span></label></div>`;
    contentDiv.innerHTML = `${languagePickerHtml}${filterSwitchHtml}<p>No prayers found for language: ${languageDisplayName}${showOnlyUnmatched ? " (matching filter)" : ""}.</p><p>Query for metadata:</p><pre>${metadataSql}</pre><p><a href="${DOLTHUB_REPO_QUERY_URL_BASE}${encodeURIComponent(metadataSql)}" target="_blank">Debug metadata query</a></p><p>Count query:</p><pre>${countSql}</pre><p><a href="${DOLTHUB_REPO_QUERY_URL_BASE}${encodeURIComponent(countSql)}" target="_blank">Debug count query</a></p>`;
    if (typeof componentHandler !== "undefined") componentHandler.upgradeDom();
    return;
  }
  if (prayersMetadata.length === 0 && page > 1) {
    setLanguageView(langCode, Math.max(1, totalPages), showOnlyUnmatched);
    return;
  }

  const prayersForDisplay = [];
  for (const pMeta of prayersMetadata) {
    let full_text_for_preview = null;
    const cached = getCachedPrayerText(pMeta.version);
    if (cached) {
      full_text_for_preview = cached.text;
      pMeta.name = cached.name || pMeta.name;
      pMeta.phelps = cached.phelps || pMeta.phelps;
      pMeta.link = cached.link || pMeta.link;
    } else {
      const textSql = `SELECT text FROM writings WHERE version = '${pMeta.version}' LIMIT 1`;
      const textRows = await executeQuery(textSql);
      if (textRows.length > 0 && textRows[0].text) {
        full_text_for_preview = textRows[0].text;
        cachePrayerText({
          version: pMeta.version,
          text: full_text_for_preview,
          name: pMeta.name,
          language: pMeta.language,
          phelps: pMeta.phelps,
          link: pMeta.link,
        });
      }
    }
    const opening_text_for_card = full_text_for_preview
      ? full_text_for_preview.substring(0, MAX_PREVIEW_LENGTH) +
        (full_text_for_preview.length > MAX_PREVIEW_LENGTH ? "..." : "")
      : "No text preview available.";
    prayersForDisplay.push({
      ...pMeta,
      opening_text: opening_text_for_card,
    });
  }

  let allPhelpsDetailsForCards = {};
  const phelpsCodesInList = [
    ...new Set(prayersForDisplay.filter((p) => p.phelps).map((p) => p.phelps)),
  ];
  if (phelpsCodesInList.length > 0) {
    const phelpsInClause = phelpsCodesInList
      .map((p) => `'${p.replace(/'/g, "''")}'`)
      .join(",");
    const translationsSql = `SELECT version, language, phelps, name, link FROM writings WHERE phelps IN (${phelpsInClause})`;
    try {
      const translationRows = await executeQuery(translationsSql);
      translationRows.forEach((row) => {
        if (!allPhelpsDetailsForCards[row.phelps])
          allPhelpsDetailsForCards[row.phelps] = [];
        allPhelpsDetailsForCards[row.phelps].push({
          version: row.version,
          language: row.language,
          name: row.name,
          link: row.link,
        });
      });
    } catch (e) {
      console.error("Failed to fetch details for phelps codes:", e);
    }
  }

  const listCardPromises = prayersForDisplay.map((pData) =>
    createPrayerCardHtml(pData, allPhelpsDetailsForCards),
  );
  const listCardsHtmlArray = await Promise.all(listCardPromises);
  const listHtml = `<div class="favorite-prayer-grid">${listCardsHtmlArray.join("")}</div>`;

// This function was originally from renderPrayerCodeView and is being removed as its contentDiv.innerHTML is now set above.
// async function renderPrayerCodeView_original_content_population() {
// This is a placeholder comment to ensure the diff tool sees a change if the original function was just setting contentDiv.innerHTML
// }

// The following was the original content setting logic for renderPrayerCodeView
/*
  let paginationHtml = "";
  if (totalPages > 1) {
    paginationHtml = '<div class="pagination">';
    if (page > 1)
      paginationHtml += `<button class="mdl-button mdl-js-button mdl-button--raised" onclick="navigateToPhelpsCodeView('${phelpsCode}', ${page - 1})">Previous</button>`;
    paginationHtml += ` <span>Page ${page} of ${totalPages}</span> `;
    if (page < totalPages)
      paginationHtml += `<button class="mdl-button mdl-js-button mdl-button--raised" onclick="navigateToPhelpsCodeView('${phelpsCode}', ${page + 1})">Next</button>`;
    paginationHtml += "</div>";
  }

  contentDiv.innerHTML = `<header><h2><span id="category">Prayer Code</span><span id="blocktitle">${phelpsCode} (Page ${page}) - All Languages</span></h2></header>${listHtml}${paginationHtml}`;
  if (typeof componentHandler !== "undefined") componentHandler.upgradeDom();
*/
  // The actual paginationHtml building needs to be preserved if it's still used.
  // For now, assuming the structure above replaces the direct contentDiv.innerHTML assignment.
  // If paginationHtml variable is needed, it must be constructed before the new contentDiv.innerHTML line.

  let paginationHtml = "";
  if (totalPages > 1) {
    paginationHtml = '<div class="pagination">';
    if (page > 1)
      paginationHtml += `<button class="mdl-button mdl-js-button mdl-button--raised" onclick="setLanguageView('${langCode}', ${page - 1}, ${showOnlyUnmatched})">Previous</button>`;
    paginationHtml += ` <span>Page ${page} of ${totalPages}</span> `;
    if (page < totalPages)
      paginationHtml += `<button class="mdl-button mdl-js-button mdl-button--raised" onclick="setLanguageView('${langCode}', ${page + 1}, ${showOnlyUnmatched})">Next</button>`;
    paginationHtml += "</div>";
  }

  const filterSwitchId = `filter-unmatched-${langCode}`;
  const filterSwitchHtml = `<div class="filter-switch-container"><label class="mdl-switch mdl-js-switch mdl-js-ripple-effect" for="${filterSwitchId}"><input type="checkbox" id="${filterSwitchId}" class="mdl-switch__input" onchange="setLanguageView('${langCode}', 1, this.checked)" ${showOnlyUnmatched ? "checked" : ""}><span class="mdl-switch__label">Show only prayers without Phelps code</span></label></div>`;

  contentDiv.innerHTML = `${languagePickerHtml}<header><h2><span id="category">Prayers</span><span id="blocktitle">Language: ${languageDisplayName} (Page ${page})${showOnlyUnmatched ? " - Unmatched" : ""}</span></h2></header>${filterSwitchHtml}${listHtml}${paginationHtml}`;
  if (typeof componentHandler !== "undefined") componentHandler.upgradeDom();
}

async function renderPrayerCodeView(phelpsCode, page = 1) {
  currentPageByPhelpsCode[phelpsCode] = page;
  const offset = (page - 1) * ITEMS_PER_PAGE;

  contentDiv.innerHTML =
    '<div class="mdl-spinner mdl-js-spinner is-active" style="margin: auto; display: block;"></div>';
  if (typeof componentHandler !== "undefined") componentHandler.upgradeDom();

  const pickerShellHtml = getLanguagePickerShellHtml();
  // Insert shell + spinner BEFORE fetching other data for this view
  contentDiv.innerHTML = pickerShellHtml +
    '<div class="mdl-spinner mdl-js-spinner is-active" style="margin: auto; display: block; margin-top: 20px;"></div>';
  if (typeof componentHandler !== "undefined") componentHandler.upgradeElement(contentDiv.querySelector('.mdl-js-tabs'));

  // Asynchronously populate the picker.
  populateLanguageSelection(null); // No specific active lang for this view. Fire and forget.

  const phelpsLangsSql = `SELECT DISTINCT language FROM writings WHERE phelps = '${phelpsCode.replace(/\'/g, "''")}' ORDER BY language`;
  const distinctLangs = await executeQuery(phelpsLangsSql);
  const navLinks = distinctLangs.map((langRow) => ({
    text: langRow.language.toUpperCase(),
    href: '#prayercode/' + phelpsCode + '/' + langRow.language,
    isActive: false,
  }));
  updateHeaderNavigation(navLinks);
  updateDrawerLanguageNavigation(navLinks);

  const metadataSql = `SELECT version, name, language, text, phelps, link FROM writings WHERE phelps = '${phelpsCode.replace(/'/g, "''")}' AND phelps IS NOT NULL AND phelps != '' ORDER BY language, name LIMIT ${ITEMS_PER_PAGE} OFFSET ${offset}`;
  const prayersMetadata = await executeQuery(metadataSql);

  const countSql = `SELECT COUNT(*) as total FROM writings WHERE phelps = '${phelpsCode.replace(/'/g, "''")}' AND phelps IS NOT NULL AND phelps != ''`;
  const countResult = await executeQuery(countSql);
  const totalPrayers = countResult.length > 0 ? countResult[0].total : 0;
  const totalPages = Math.ceil(totalPrayers / ITEMS_PER_PAGE);

  if (prayersMetadata.length === 0 && page === 1) {
    const debugQueryUrl = `${DOLTHUB_REPO_QUERY_URL_BASE}${encodeURIComponent(metadataSql)}`;
    contentDiv.innerHTML = `<p>No prayer versions found for Phelps Code: ${phelpsCode}.</p><p>Query used:</p><pre>${metadataSql}</pre><p><a href="${debugQueryUrl}" target="_blank">Debug this query</a></p>`;
    return;
  }
  if (prayersMetadata.length === 0 && page > 1) {
    renderPrayerCodeView(phelpsCode, Math.max(1, totalPages));
    return;
  }

  const prayersForDisplay = prayersMetadata.map((p) => {
    let full_text_for_preview = p.text;
    if (!getCachedPrayerText(p.version) && p.text) {
      cachePrayerText({
        version: p.version,
        text: p.text,
        name: p.name,
        language: p.language,
        phelps: p.phelps,
        link: p.link,
      });
    } else if (!p.text) {
      const cached = getCachedPrayerText(p.version);
      if (cached && cached.text) full_text_for_preview = cached.text;
    }
    const opening_text_for_card = full_text_for_preview
      ? full_text_for_preview.substring(0, MAX_PREVIEW_LENGTH) +
        (full_text_for_preview.length > MAX_PREVIEW_LENGTH ? "..." : "")
      : "No text preview available.";
    return { ...p, opening_text: opening_text_for_card };
  });

  let allPhelpsDetailsForCards = {};
  if (phelpsCode) {
    const allVersionsSql = `SELECT version, language, phelps, name, link FROM writings WHERE phelps = '${phelpsCode.replace(/'/g, "''")}'`;
    const allVersionRows = await executeQuery(allVersionsSql);
    if (allVersionRows.length > 0)
      allPhelpsDetailsForCards[phelpsCode] = allVersionRows.map((r) => ({
        ...r,
      }));
  }

  const listCardsHtmlArray = prayersForDisplay.map((pData) =>
    createPrayerCardHtml(pData, allPhelpsDetailsForCards),
  );
  const listHtml = `<div class="favorite-prayer-grid">${listCardsHtmlArray.join("")}</div>`;

  let paginationHtml = "";
  if (totalPages > 1) {
    paginationHtml = '<div class="pagination">';
    if (page > 1)
      paginationHtml += `<button class="mdl-button mdl-js-button mdl-button--raised" onclick="renderPrayerCodeView('${phelpsCode}', ${page - 1})">Previous</button>`;
    paginationHtml += ` <span>Page ${page} of ${totalPages}</span> `;
    if (page < totalPages)
      paginationHtml += `<button class="mdl-button mdl-js-button mdl-button--raised" onclick="renderPrayerCodeView('${phelpsCode}', ${page + 1})">Next</button>`;
    paginationHtml += "</div>";
  }

  contentDiv.innerHTML = `${languagePickerHtml}<header><h2><span id="category">Prayer Code</span><span id="blocktitle">${phelpsCode} (Page ${page}) - All Languages</span></h2></header>${listHtml}${paginationHtml}`;
  // componentHandler.upgradeDom() will be called after all content, including picker population, finishes
  // For now, the shell upgrade is done earlier.
  // If issues arise with MDL components in listHtml/paginationHtml, a targeted upgrade here might be needed.
  if (typeof componentHandler !== "undefined") componentHandler.upgradeDom(); // Or more targeted if possible
}

function getLanguagePickerShellHtml() {
    const menuId = "all-languages-menu"; // For the 'for' attribute
    const searchInputId = "language-search-input";
    const allLanguagesMenuUlId = "all-languages-menu-ul";

    return `
      <div class="mdl-tabs mdl-js-tabs mdl-js-ripple-effect language-picker-tabs">
        <div class="mdl-tabs__tab-bar" id="language-picker-tab-bar">
          <!-- Favorites and Recent tabs will be inserted here by populateLanguageSelection -->
          <div id="more-languages-wrapper" class="more-languages-section-wrapper">
            <button id="${menuId}-btn" class="mdl-button mdl-js-button mdl-button--raised mdl-js-ripple-effect">
              More Languages <i class="material-icons" role="presentation">arrow_drop_down</i>
            </button>
            <ul class="mdl-menu mdl-menu--bottom-left mdl-js-menu mdl-js-ripple-effect"
                for="${menuId}-btn" id="${allLanguagesMenuUlId}">
              <li id="language-search-li" onclick="event.stopPropagation();">
                <div class="mdl-textfield mdl-js-textfield">
                  <input class="mdl-textfield__input" type="text" id="${searchInputId}" onkeyup="filterLanguageMenu()" onclick="event.stopPropagation();">
                  <label class="mdl-textfield__label" for="${searchInputId}">Search languages...</label>
                 </div>
              </li>
              <li class="mdl-menu__divider" style="margin-top:0;"></li>
              <!-- Dynamic menu items will be inserted here -->
            </ul>
            <div id="all-languages-message-placeholder"></div>
          </div>
        </div>
        <!-- Tab panels are handled by specific views, e.g., favorites panel in renderLanguageList -->
      </div>
    `;
}

async function resolveAndRenderPrayerByPhelpsAndLang(
  phelpsCode,
  targetLanguageCode,
) {
  contentDiv.innerHTML =
    '<div class="mdl-spinner mdl-js-spinner is-active" style="margin: auto; display: block;"></div>';
  if (typeof componentHandler !== "undefined") componentHandler.upgradeDom();

  const displayTargetLanguage = await getLanguageDisplayName(targetLanguageCode);

  const sql = `SELECT version, text, language, phelps, name, source, link FROM writings WHERE phelps = \'${phelpsCode.replace(/\'/g, "\'\'")}\' AND language = \'${targetLanguageCode.replace(/\'/g, "\'\'")}\' ORDER BY LENGTH(text) DESC, name`;
  const prayerVersions = await executeQuery(sql);

  if (prayerVersions.length === 0) {
    contentDiv.innerHTML = `<p>No prayers found for Phelps Code ${phelpsCode} in language ${displayTargetLanguage}.</p>`;
    const transSql = `SELECT DISTINCT language FROM writings WHERE phelps = \'${phelpsCode.replace(/\'/g, "\'\'")}\' ORDER BY language`;
    const distinctLangs = await executeQuery(transSql);
    const navLinkPromises = distinctLangs.map(async (langRow) => ({
      text: await getLanguageDisplayName(langRow.language),
      href: `#prayercode/${phelpsCode}/${langRow.language}`,
      isActive: false,
    }));
    const navLinks = await Promise.all(navLinkPromises);
    updateHeaderNavigation(navLinks);
    updateDrawerLanguageNavigation(navLinks);
    return;
  }

  const primaryPrayer = prayerVersions[0];
  await renderPrayer(primaryPrayer.version, phelpsCode, targetLanguageCode);

  if (prayerVersions.length > 1) {
    const otherVersionsDiv = document.createElement("div");
    otherVersionsDiv.style.marginTop = "20px";
    otherVersionsDiv.style.paddingTop = "15px";
    otherVersionsDiv.style.borderTop = "1px solid #eee";
    // displayTargetLanguage is available from earlier in this function
    let listHtml = `<h5>Other versions in ${displayTargetLanguage} for ${phelpsCode}:</h5><ul class="other-versions-in-lang-list">`;
    prayerVersions.slice(1).forEach((altVersion) => {
      const displayName = altVersion.name || `Version ${altVersion.version}`;
      const domain = altVersion.link ? `(${getDomain(altVersion.link)})` : "";
      listHtml += `<li><a href="#prayer/${altVersion.version}">${displayName} ${domain}</a></li>`;
    });
    listHtml += `</ul>`;
    otherVersionsDiv.innerHTML = listHtml;
    contentDiv.appendChild(otherVersionsDiv);
    if (typeof componentHandler !== "undefined") componentHandler.upgradeDom();
  }
}

async function populateLanguageSelection(currentActiveLangCode = null) {
  const tabBarElement = document.getElementById('language-picker-tab-bar');
  const moreLanguagesWrapperElement = document.getElementById('more-languages-wrapper');
  const menuUlElement = document.getElementById('all-languages-menu-ul');
  const messagePlaceholderElement = document.getElementById('all-languages-message-placeholder');

  if (!tabBarElement || !menuUlElement || !moreLanguagesWrapperElement || !messagePlaceholderElement) {
    console.error("Language picker shell elements not found in DOM. Cannot populate.");
    return;
  }

  // Clear previous dynamic content (safer than replacing entire innerHTML of tab bar)
  // Find existing tab links (not the more-languages-wrapper) and remove them
  const existingTabLinks = tabBarElement.querySelectorAll('a.mdl-tabs__tab');
  existingTabLinks.forEach(link => link.remove());
  
  // Clear previous menu items (children of ul, skipping search and divider)
  while (menuUlElement.children.length > 2) { // Assumes search li and divider li are first two
      menuUlElement.removeChild(menuUlElement.lastChild);
  }
  messagePlaceholderElement.innerHTML = ''; // Clear previous message


  try {
    await fetchLanguageNames(); // Ensure names are loaded for display
  } catch (error) {
    console.warn("generateLanguageSelectionHtml: Failed to pre-load language names. Fallbacks may be used.", error.message);
  }

  const sql = `SELECT w.language, (SELECT COUNT(DISTINCT sub.phelps) FROM writings sub WHERE sub.language = w.language AND sub.phelps IS NOT NULL AND sub.phelps != \'\') AS phelps_covered_count, (SELECT COUNT(DISTINCT CASE WHEN sub.phelps IS NOT NULL AND sub.phelps != \'\' THEN NULL ELSE sub.version END) FROM writings sub WHERE sub.language = w.language) AS versions_without_phelps_count FROM writings w WHERE w.language IS NOT NULL AND w.language != \'\' GROUP BY w.language ORDER BY w.language;`;
  
  let allLangsWithStats = getCachedLanguageStats();
  let fetchedFreshData = false;
  let attemptedFetch = false;

  if (!allLangsWithStats) {
    // console.log("No fresh language stats in cache, attempting fetch...");
    attemptedFetch = true;
    try {
      allLangsWithStats = await executeQuery(sql);
      if (allLangsWithStats && allLangsWithStats.length > 0) {
        cacheLanguageStats(allLangsWithStats);
        fetchedFreshData = true;
        // console.log("Fetched and cached fresh language stats.");
      } else { 
        // If executeQuery resulted in no data (e.g. empty rows, or it threw and was caught below)
        // ensure allLangsWithStats is an empty array for subsequent logic.
        allLangsWithStats = []; 
      }
    } catch (e) { 
      console.error("Error during executeQuery for language stats:", e);
      // executeQuery now throws, so we catch it here.
      // allLangsWithStats will remain as it was (either null from initial getCachedLanguageStats or stale data if loaded later)
      // The subsequent logic will handle trying stale cache.
      allLangsWithStats = allLangsWithStats || []; // Ensure it's at least an empty array if it was null/undefined
    }
  } else {
    // console.log("Using fresh language stats from cache.");
    fetchedFreshData = true; // Technically it's fresh from cache
  }

  // If fetch failed or cache was empty initially, try to get stale cache
  if ((attemptedFetch && !fetchedFreshData) || !allLangsWithStats || allLangsWithStats.length === 0) {
    // console.log("Fetch might have failed or returned no data, trying stale cache for language stats...");
    const staleStats = getCachedLanguageStats(true); // Allow stale
    if (staleStats && staleStats.length > 0) {
      allLangsWithStats = staleStats;
      // console.log("Using stale language stats from cache.");
      // Optionally, set a flag here to indicate to the UI that data is stale
      // For now, just using it transparently.
    } else if (!allLangsWithStats || allLangsWithStats.length === 0) {
      // console.log("No language stats available in cache (fresh or stale) after fetch attempt.");
      allLangsWithStats = []; // Ensure it's an array
    }
  }


  if (allLangsWithStats.length === 0) {
    // const debugQueryUrl = `${DOLTHUB_REPO_QUERY_URL_BASE}${encodeURIComponent(sql)}`;
    // return `<p>No languages found.</p><p>Query:</p><pre>${sql}</pre><p><a href="${debugQueryUrl}" target="_blank">Debug query</a></p>`;
    return '<p style="text-align:center;">No languages found to select.</p>';
  }

  const recentLanguageCodes = getRecentLanguages();
  let recentAndFavoritesTabsBarHtml = "";
  const recentLangDetails = [];

  // Determine if Favorites tab should be active
  const favoritesTabIsActive = !currentActiveLangCode;

  // Start with the Favorites tab
  // It doesn't navigate via setLanguageView, it reveals a panel handled by renderLanguageList
  // For the main #languages view, this tab will be active.
  // For other views (prayer, prayers/lang, prayercode), a language tab will be active.
  recentAndFavoritesTabsBarHtml += 
    `<a href="#language-tab-panel-favorites" 
        class="mdl-tabs__tab ${favoritesTabIsActive ? 'is-active' : ''}" 
        onclick="event.preventDefault(); if (window.location.hash !== '#languages') window.location.hash = '#languages';"> 
        ⭐ Favorites
     </a>`;
  // When on a non-#languages page, clicking "Favorites" tab navigates to #languages (where its panel is shown)

  if (recentLanguageCodes.length > 0 && allLangsWithStats.length > 0) {
    for (const langCode of recentLanguageCodes) {
      const langData = allLangsWithStats.find(l => l.language.toLowerCase() === langCode);
      if (langData) {
        const displayName = await getLanguageDisplayName(langData.language);
        const phelpsCount = parseInt(langData.phelps_covered_count, 10) || 0;
        const nonPhelpsCount = parseInt(langData.versions_without_phelps_count, 10) || 0;
        const totalConceptualPrayers = phelpsCount + nonPhelpsCount;
        recentLangDetails.push({
          code: langData.language,
          display: displayName,
          phelps: phelpsCount,
          total: totalConceptualPrayers,
        });
      }
    }

    if (recentLangDetails.length > 0) {
      const recentTabsHtml = recentLangDetails.map(lang => {
        // A recent language tab is active only if currentActiveLangCode is present and matches
        const isActive = currentActiveLangCode && (lang.code.toLowerCase() === currentActiveLangCode.toLowerCase());
        return `<a href="#lang-tab-${lang.code}" 
                   class="mdl-tabs__tab ${isActive ? 'is-active' : ''}" 
                   onclick="event.preventDefault(); setLanguageView('${lang.code}', 1, false);">
                   ${lang.display} <span style="font-size:0.8em; opacity:0.7;">(${lang.phelps}/${lang.total})</span>
                 </a>`;
        }).join('');
      recentAndFavoritesTabsBarHtml += recentTabsHtml;
    }
  }

  // Filter out recent languages from the main list for the "All Languages" menu
  // This logic remains the same: languages in recent tabs are not repeated in "More"
  const otherLanguagesWithStats = allLangsWithStats.filter(lang =>
    !recentLanguageCodes.find(rlc => rlc === lang.language.toLowerCase())
  );

  let allLanguagesMenuHtml = "";
  const searchInputId = "language-search-input"; // Used for filterLanguageMenu closure
  const allLanguagesMenuUlId = "all-languages-menu-ul"; // Used for filterLanguageMenu closure

  if (otherLanguagesWithStats.length > 0) {
    moreLanguagesWrapperElement.style.display = 'inline-block'; // Show the "More Languages" section

    const menuItemPromises = otherLanguagesWithStats.map(async (langData) => {
      const langCode = langData.language;
      const displayName = await getLanguageDisplayName(langCode);
      const phelpsCount = parseInt(langData.phelps_covered_count, 10) || 0;
      const nonPhelpsCount = parseInt(langData.versions_without_phelps_count, 10) || 0;
      const totalConceptualPrayers = phelpsCount + nonPhelpsCount;
      return `<li class="mdl-menu__item" onclick="setLanguageView('${langCode}', 1, false)" data-val="${langCode}">${displayName} (${phelpsCount}/${totalConceptualPrayers})</li>`;
    });
    const menuItemsHtml = (await Promise.all(menuItemPromises)).join('\n');

    // menuItemsHtml will be appended to menuUlElement later in the code.
    // The button and ul shell are already in the DOM from getLanguagePickerShellHtml.

    // Define filterLanguageMenu within the scope or ensure it's globally available
    // Attaching to window is a way to make it available for inline onkeyup
    // Ensure it's only defined once per app lifecycle or managed appropriately if this function is called multiple times.
    if (typeof window.filterLanguageMenu !== 'function') { // Define only if not already defined
        window.filterLanguageMenu = function() {
            const input = document.getElementById(searchInputId);
            if (!input) return;
            const filter = input.value.toLowerCase();
            const ul = document.getElementById(allLanguagesMenuUlId);
            if (!ul) return;
            const items = ul.getElementsByTagName('li');
            for (let i = 0; i < items.length; i++) {
                if (items[i].querySelector(`#${searchInputId}`) || items[i].classList.contains('mdl-menu__divider')) {
                    continue;
                }
                const txtValue = items[i].textContent || items[i].innerText;
                if (txtValue.toLowerCase().indexOf(filter) > -1) {
                    items[i].style.display = "";
                } else {
                    items[i].style.display = "none";
                }
            }
        }; // Closes function body, semicolon for assignment
    } // Closes 'if (typeof window.filterLanguageMenu !== 'function')'

    // Append new menu items
// The menuItemsHtml is already a string of <li> elements
// We need to insert these LI elements into the menuUlElement
// A simple way for now, though could be more performant for large lists
const tempDiv = document.createElement('div');
tempDiv.innerHTML = menuItemsHtml; // menuItemsHtml was generated earlier
Array.from(tempDiv.children).forEach(child => {
    menuUlElement.appendChild(child);
});
} // Closes 'if (otherLanguagesWithStats.length > 0)'
else if (allLangsWithStats.length > 0 && recentLangDetails.length === allLangsWithStats.length && recentLangDetails.length > 0) {
messagePlaceholderElement.innerHTML = `<p style="font-size:0.9em; color:#555;">All available languages are shown in "Recent".</p>`;
moreLanguagesWrapperElement.style.display = 'inline-block'; // Show the wrapper to display this message
// Hide the button itself if only the message is shown
const moreButton = document.getElementById('all-languages-menu-btn');
if(moreButton) moreButton.style.display = 'none';

} else {
// No "other" languages and not all are recent (or no languages at all)
// Hide the "More Languages" section entirely if there are no other languages
  if (moreLanguagesWrapperElement) {
    moreLanguagesWrapperElement.style.display = 'none';
  }
}


// Populate tab bar: Prepend recent/favorites tabs to the existing "More Languages" section in the tab bar.
// The "More Languages" section is already in the shell. We add tabs before it.
if (recentAndFavoritesTabsBarHtml) {
  const tempTabsDiv = document.createElement('div');
  tempTabsDiv.innerHTML = recentAndFavoritesTabsBarHtml; // This is a string of <a> tags
  const moreLanguagesDiv = tabBarElement.querySelector('.more-languages-section-wrapper');
  if (moreLanguagesDiv) {
      Array.from(tempTabsDiv.children).reverse().forEach(tabLink => { // reverse to prepend correctly
          tabBarElement.insertBefore(tabLink, moreLanguagesDiv);
      });
  } else { // Fallback if more-languages-wrapper isn't there (should be, from shell)
      tabBarElement.innerHTML = recentAndFavoritesTabsBarHtml + tabBarElement.innerHTML;
  }
}

// Ensure MDL components in the picker are upgraded if they were dynamically added or modified.
// This is crucial for tabs, menu, button ripples, textfield in search.
if (typeof componentHandler !== "undefined") {
const pickerElement = tabBarElement.closest('.mdl-tabs');
if (pickerElement) {
    componentHandler.upgradeElement(pickerElement); // Upgrade tabs
    const menuButton = pickerElement.querySelector('#all-languages-menu-btn');
    const menuUL = pickerElement.querySelector('#all-languages-menu-ul');
    const searchTf = pickerElement.querySelector('#language-search-li .mdl-js-textfield');
    if (menuButton) componentHandler.upgradeElement(menuButton);
    if (menuUL) componentHandler.upgradeElement(menuUL);
    if (searchTf) componentHandler.upgradeElement(searchTf);

    // MDL menus might need specific re-initialization if items are added dynamically
    // For simplicity, upgradeDom on the menu might be easiest if specific upgrade isn't clean
    // componentHandler.upgradeDom(menuUL); // Alternative
} else {
    componentHandler.upgradeDom(); // Broader upgrade if specific element not found
}
}
} // Closes populateLanguageSelection

// This function now manipulates DOM directly, doesn't return HTML for the whole picker.

async function renderLanguageList() {
// Stage 1: Render the shell immediately
contentDiv.innerHTML = getLanguagePickerShellHtml() + 
'<div class="mdl-spinner mdl-js-spinner is-active" style="margin: auto; display: block; margin-top: 20px;"></div>'; // Spinner for data loading
if (typeof componentHandler !== "undefined") componentHandler.upgradeElement(contentDiv.querySelector('.mdl-js-tabs')); // Upgrade tabs shell

// Clear main page header/drawer nav as this view controls its own nav via the picker
updateHeaderNavigation([]);
updateDrawerLanguageNavigation([]);

currentPageByLanguage = {};
currentPageBySearchTerm = {};
    '<div class="mdl-spinner mdl-js-spinner is-active" style="margin: auto; display: block;"></div>';
  if (typeof componentHandler !== "undefined") componentHandler.upgradeDom();
  updateHeaderNavigation([]);
  updateDrawerLanguageNavigation([]);

  currentPageByLanguage = {};
  currentPageBySearchTerm = {};
  currentPageByPhelpsCode = {};
  currentPageByPhelpsLangCode = {};

  let favoritesDisplayHtml = "";
  if (favoritePrayers.length > 0) {
    favoritesDisplayHtml += `<div id="favorite-prayers-section" style="padding-top:10px;"><h3>⭐ Your Favorite Prayers</h3>`;
    const phelpsCodesToFetchForFavs = [
      ...new Set(
        favoritePrayers.filter((fp) => fp.phelps).map((fp) => fp.phelps),
      ),
    ];
    let allPhelpsDetailsForFavCards = {};

    if (phelpsCodesToFetchForFavs.length > 0) {
      const phelpsInClause = phelpsCodesToFetchForFavs
        .map((p) => `'${p.replace(/'/g, "''")}'`) // Simpler escaping for SQL
        .join(",");
      const favTranslationsSql = `SELECT version, language, phelps, name, link FROM writings WHERE phelps IN (${phelpsInClause})`;
      try {
        const translationRows = await executeQuery(favTranslationsSql);
        translationRows.forEach((row) => {
          if (!allPhelpsDetailsForFavCards[row.phelps])
            allPhelpsDetailsForFavCards[row.phelps] = [];
          allPhelpsDetailsForFavCards[row.phelps].push({
            ...row,
          });
        });
      } catch (e) {
        console.error("Failed to fetch details for favorite phelps codes:", e);
      }
    }

    const favoriteCardPromises = [];
    for (const fp of favoritePrayers) {
      const cached = getCachedPrayerText(fp.version);
      let textForCardPreview = "Preview not available.";
      let nameForCard = fp.name || (cached ? cached.name : null);
      let langForCard = fp.language || (cached ? cached.language : "N/A");
      let phelpsForCard = fp.phelps || (cached ? cached.phelps : null);
      let linkForCard = cached ? cached.link : null;
      if (cached && cached.text)
        textForCardPreview =
          cached.text.substring(0, MAX_PREVIEW_LENGTH) +
          (cached.text.length > MAX_PREVIEW_LENGTH ? "..." : "");
      favoriteCardPromises.push(
        createPrayerCardHtml(
          {
            version: fp.version,
            name: nameForCard,
            language: langForCard,
            phelps: phelpsForCard,
            opening_text: textForCardPreview,
            link: linkForCard,
          },
          allPhelpsDetailsForFavCards,
        ),
      );
    }
    const favoriteCardsHtmlArray = await Promise.all(favoriteCardPromises);
    favoritesDisplayHtml += `<div class="favorite-prayer-grid">${favoriteCardsHtmlArray.join("")}</div></div>`;
  } else {
    favoritesDisplayHtml += `<div id="favorite-prayers-section" style="text-align: center; padding: 20px 0; margin-bottom:10px;"><p>You haven't favorited any prayers yet. <br/>Click the <i class="material-icons" style="vertical-align: bottom; font-size: 1.2em;">star_border</i> icon on a prayer's page to add it here!</p></div>`;
  }

  // When renderLanguageList is called, currentActiveLangCode for populateLanguageSelection is null,
  // so the "Favorites" tab in the picker will be marked active.
  const pickerShellHtml = getLanguagePickerShellHtml(); // Get shell

  // Stage 1: Render the shell immediately with a main page spinner
  contentDiv.innerHTML = 
    `<header><h2><span id="category">Prayers</span><span id="blocktitle">Available Languages</span></h2></header>
     ${pickerShellHtml}
     <div id="main-content-area" style="min-height: 100px;"> {/* Container for spinner or content */}
        <div id="main-content-area-spinner" class="main-content-spinner">
            <div class="mdl-spinner mdl-js-spinner is-active"></div>
        </div>
        <div class="mdl-tabs__panel" id="language-tab-panel-favorites">
          {/* Favorites content will be injected here */}
        </div>
     </div>
     <p class="text-center" style="font-size:0.9em; color: #555; margin-top:20px;">Counts are (Unique Phelps Codes / Total Unique Prayers). Select a language to browse.</p>`;
  
  if (typeof componentHandler !== "undefined") {
    componentHandler.upgradeDom(); // Upgrade shell, including tabs, menu button, textfield in menu
  }

  // Stage 2: Asynchronously populate the language picker and then the favorites
  populateLanguageSelection(null)
    .then(async () => { // Proceed only if populateLanguageSelection was successful (didn't throw)
      let localFavoritesDisplayHtml = "";
      // Once picker is populated (or at least population has started), load and display favorites.
      if (favoritePrayers.length > 0) {
      localFavoritesDisplayHtml += `<div id="favorite-prayers-section"><h3>⭐ Your Favorite Prayers</h3>`; // Removed inline style
      const phelpsCodesToFetchForFavs = [
        ...new Set(
          favoritePrayers.filter((fp) => fp.phelps).map((fp) => fp.phelps),
        ),
      ];
      let allPhelpsDetailsForFavCards = {};

      if (phelpsCodesToFetchForFavs.length > 0) {
        const phelpsInClause = phelpsCodesToFetchForFavs
          .map((p) => "'" + p.replace(/'/g, "''") + "'")
          .join(",");
        const favTranslationsSql = `SELECT version, language, phelps, name, link FROM writings WHERE phelps IN (${phelpsInClause})`;
        try {
          const translationRows = await executeQuery(favTranslationsSql);
          translationRows.forEach((row) => {
            if (!allPhelpsDetailsForFavCards[row.phelps])
              allPhelpsDetailsForFavCards[row.phelps] = [];
            allPhelpsDetailsForFavCards[row.phelps].push({
              ...row,
            });
          });
        } catch (e) {
          console.error("Failed to fetch details for favorite phelps codes:", e);
        }
      }

      const favoriteCardPromises = [];
      for (const fp of favoritePrayers) {
        const cached = getCachedPrayerText(fp.version);
        let textForCardPreview = "Preview not available.";
        let nameForCard = fp.name || (cached ? cached.name : null);
        let langForCard = fp.language || (cached ? cached.language : "N/A");
        let phelpsForCard = fp.phelps || (cached ? cached.phelps : null);
        let linkForCard = cached ? cached.link : null;
        if (cached && cached.text)
          textForCardPreview =
            cached.text.substring(0, MAX_PREVIEW_LENGTH) +
            (cached.text.length > MAX_PREVIEW_LENGTH ? "..." : "");
        favoriteCardPromises.push(
          createPrayerCardHtml(
            {
              version: fp.version,
              name: nameForCard || '',
              language: langForCard || 'N/A', // 'N/A' was already a fallback but good to be explicit
              phelps: phelpsForCard || '',
              opening_text: textForCardPreview || '',
              link: linkForCard || '',
            },
            allPhelpsDetailsForFavCards,
          ),
        );
      }
      const favoriteCardsHtmlArray = await Promise.all(favoriteCardPromises);
      localFavoritesDisplayHtml += `<div class="favorite-prayer-grid">${favoriteCardsHtmlArray.join("")}</div></div>`;
    } else {
      localFavoritesDisplayHtml += `<div id="favorite-prayers-section" class="text-center" style="padding: 20px 0; margin-bottom:10px;"><p>You haven't favorited any prayers yet. <br/>Click the <i class="material-icons" style="vertical-align: bottom; font-size: 1.2em;">star_border</i> icon on a prayer's page to add it here!</p></div>`;
    }

    // Inject favorites HTML into its panel and remove spinner
    const favoritesPanel = document.getElementById('language-tab-panel-favorites');
    const mainSpinner = document.getElementById('main-content-area-spinner');
    if (favoritesPanel) {
        favoritesPanel.innerHTML = localFavoritesDisplayHtml;
        favoritesPanel.classList.add('is-active'); // Ensure it\'s active
    }
    if (mainSpinner) {
        mainSpinner.remove();
    }
    
    // Re-upgrade components in the favorites panel if necessary
    if (typeof componentHandler !== "undefined" && favoritesPanel) {
      componentHandler.upgradeDom(favoritesPanel); // More targeted
    }
  }).catch(error => {
      console.error("Error in renderLanguageList (populating picker or favorites):", error);
      const mainSpinner = document.getElementById('main-content-area-spinner');
      if(mainSpinner) mainSpinner.remove();
      
      const favoritesPanel = document.getElementById('language-tab-panel-favorites');
      if (favoritesPanel) {
        favoritesPanel.innerHTML = `<p>Error loading page content: ${error.message}. Please try refreshing.</p>`;
        favoritesPanel.classList.add('is-active'); // Ensure panel is visible to show error
      } else {
        // Fallback if the panel itself isn't there, put error in main content placeholder
        const mainContentArea = document.getElementById('main-content-area') || contentDiv;
        mainContentArea.innerHTML = `<p>Error loading page content: ${error.message}. Please try refreshing.</p>`;
      }
  });
}

async function renderSearchResults(searchTerm, page = 1) {
  currentPageBySearchTerm[searchTerm] = page;
  contentDiv.innerHTML =
    '<div class="mdl-spinner mdl-js-spinner is-active" style="margin: auto; display: block;"></div>';
  if (typeof componentHandler !== "undefined") componentHandler.upgradeDom();
  updateHeaderNavigation([]);
  updateDrawerLanguageNavigation([]);

  const headerSearchInput = document.getElementById("header-search-field");
  if (headerSearchInput && headerSearchInput.value !== searchTerm)
    headerSearchInput.value = searchTerm;
  const drawerSearchInput = document.getElementById("drawer-search-field");
  if (drawerSearchInput && drawerSearchInput.value !== searchTerm)
    drawerSearchInput.value = searchTerm;

  const saneSearchTermForSql = searchTerm.replace(/'/g, "''");
  const lowerSearchTerm = searchTerm.toLowerCase();
  const localFoundItems = [];
  const allCached = getAllCachedPrayers();
  allCached.forEach((cachedPrayer) => {
    let match = false;
    if (
      cachedPrayer.text &&
      cachedPrayer.text.toLowerCase().includes(lowerSearchTerm)
    )
      match = true;
    if (
      !match &&
      cachedPrayer.name &&
      cachedPrayer.name.toLowerCase().includes(lowerSearchTerm)
    )
      match = true;
    if (match)
      localFoundItems.push({
        ...cachedPrayer,
        opening_text: cachedPrayer.text
          ? cachedPrayer.text.substring(0, MAX_PREVIEW_LENGTH) +
            (cachedPrayer.text.length > MAX_PREVIEW_LENGTH ? "..." : "")
          : "No text preview.",
        source: "cache",
      });
  });

  const dbNameSql = `SELECT version, name, language, phelps, link FROM writings WHERE name LIKE '%${saneSearchTermForSql}%' ORDER BY name, version`;
  const dbNameItemsRaw = await executeQuery(dbNameSql);
  const dbNameItems = dbNameItemsRaw.map((item) => ({
    ...item,
    source: "db_name",
  }));

  let combinedResults = [];
  const processedVersions = new Set();
  localFoundItems.forEach((item) => {
    if (!processedVersions.has(item.version)) {
      combinedResults.push(item);
      processedVersions.add(item.version);
    }
  });
  dbNameItems.forEach((item) => {
    if (!processedVersions.has(item.version)) {
      combinedResults.push(item);
      processedVersions.add(item.version);
    }
  });

  const totalResults = combinedResults.length;
  const totalPages = Math.ceil(totalResults / ITEMS_PER_PAGE);
  if (page > totalPages && totalPages > 0) page = totalPages;
  else if (totalPages === 0 && page > 1) page = 1;
  currentPageBySearchTerm[searchTerm] = page;

  const startIndex = (page - 1) * ITEMS_PER_PAGE;
  const paginatedCombinedResults = combinedResults.slice(
    startIndex,
    startIndex + ITEMS_PER_PAGE,
  );
  const displayItems = [];
  for (const item of paginatedCombinedResults) {
    let displayItem = { ...item };
    if (item.source === "db_name" && !item.opening_text) {
      const cached = getCachedPrayerText(item.version);
      if (cached) {
        displayItem = {
          ...displayItem,
          ...cached,
          opening_text: cached.text
            ? cached.text.substring(0, MAX_PREVIEW_LENGTH) +
              (cached.text.length > MAX_PREVIEW_LENGTH ? "..." : "")
            : "No text preview.",
        };
      } else {
        const textQuerySql = `SELECT text, name, phelps, language, link FROM writings WHERE version = '${item.version}' LIMIT 1`;
        const textRows = await executeQuery(textQuerySql);
        if (textRows.length > 0) {
          const dbItem = textRows[0];
          displayItem = {
            ...displayItem,
            ...dbItem,
            opening_text: dbItem.text
              ? dbItem.text.substring(0, MAX_PREVIEW_LENGTH) +
                (dbItem.text.length > MAX_PREVIEW_LENGTH ? "..." : "")
              : "No text preview.",
          };
          cachePrayerText({ ...dbItem });
        } else displayItem.opening_text = "Text not found for preview.";
      }
    }
    displayItems.push(displayItem);
  }

  if (totalResults === 0) {
    const searchExplanationForNoResults = `<div style="margin-top: 15px; padding: 10px; background-color: #f0f0f0; border-radius: 4px; font-size: 0.9em;"><p style="margin-bottom: 5px;"><strong>Search Information:</strong></p><ul style="margin-top: 0; padding-left: 20px;"><li>This search looked for "${searchTerm}" in prayer <strong>titles</strong> from the main database.</li><li>It also checked the <strong>full text</strong> of ${allCached.length} prayer(s) stored in your browser's local cache.</li><li>Try browsing <a href="#prayers">language lists</a> to add prayers to cache for full-text search.</li></ul></div>`;
    contentDiv.innerHTML = `<p>No prayers found matching "${searchTerm}".</p>${searchExplanationForNoResults}<p style="margin-top: 15px;">For comprehensive text search, try <a href="https://tiddly.holywritings.net/workspace" target="_blank">tiddly.holywritings.net/workspace</a>.</p><p>Debug: <a href="${DOLTHUB_REPO_QUERY_URL_BASE}${encodeURIComponent(dbNameSql)}" target="_blank">View DB Name Query</a></p>`;
    return;
  }

  let allPhelpsDetailsForCards = {};
  const phelpsCodesInList = [
    ...new Set(displayItems.filter((p) => p.phelps).map((p) => p.phelps)),
  ];
  if (phelpsCodesInList.length > 0) {
    const phelpsInClause = phelpsCodesInList
      .map((p) => `'${p.replace(/'/g, "''")}'`)
      .join(",");
    const translationsSql = `SELECT version, language, phelps, name, link FROM writings WHERE phelps IN (${phelpsInClause})`;
    try {
      const translationRows = await executeQuery(translationsSql);
      translationRows.forEach((row) => {
        if (!allPhelpsDetailsForCards[row.phelps])
          allPhelpsDetailsForCards[row.phelps] = [];
        allPhelpsDetailsForCards[row.phelps].push({
          ...row,
        });
      });
    } catch (e) {
      console.error("Failed to fetch details for phelps codes in search:", e);
    }
  }

  const listCardsHtmlArray = displayItems.map((pData) =>
    createPrayerCardHtml(pData, allPhelpsDetailsForCards),
  );
  const listHtml = `<div class="favorite-prayer-grid">${listCardsHtmlArray.join("")}</div>`;
  let paginationHtml = "";
  if (totalPages > 1) {
    const escapedSearchTerm = searchTerm
      .replace(/'/g, "\\'")
      .replace(/"/g, '\\"');
    paginationHtml = '<div class="pagination">';
    if (page > 1)
      paginationHtml += `<button class="mdl-button mdl-js-button mdl-button--raised" onclick="renderSearchResults('${escapedSearchTerm}', ${page - 1})">Previous</button>`;
    paginationHtml += ` <span>Page ${page} of ${totalPages}</span> `;
    if (page < totalPages)
      paginationHtml += `<button class="mdl-button mdl-js-button mdl-button--raised" onclick="renderSearchResults('${escapedSearchTerm}', ${page + 1})">Next</button>`;
    paginationHtml += "</div>";
  }
  const searchInfoHtml = `<div style="margin-top: 15px; padding: 10px; background-color: #f0f0f0; border-radius: 4px; font-size: 0.9em;"><p style="margin-bottom: 5px;"><strong>How this search works:</strong></p><ul style="margin-top: 0; padding-left: 20px;"><li>Searches prayer <strong>titles</strong> in database.</li><li>Searches <strong>full text</strong> of cached prayers.</li><li>View prayers via <a href="#prayers">language lists</a> to improve cache for full-text search.</li></ul></div>`;
  const tiddlySuggestionHtml = `<p style="margin-top: 20px; text-align: center; font-size: 0.9em;">For more comprehensive text search, try <a href="https://tiddly.holywritings.net/workspace" target="_blank">tiddly.holywritings.net/workspace</a>.</p>`;

  contentDiv.innerHTML = `<header><h2><span id="category">Search Results</span><span id="blocktitle">For "${searchTerm}" (Page ${page})</span></h2></header>${listHtml}${paginationHtml}${searchInfoHtml}${tiddlySuggestionHtml}`;
  if (typeof componentHandler !== "undefined") componentHandler.upgradeDom();
}

async function handleRouteChange() {
  await fetchLanguageNames(); // Ensure names are loaded/attempted
  const hash = window.location.hash;
  const [mainHashPath, queryParamsStr] = hash.split("?");
  let pageParam = 1,
    showOnlyUnmatchedParam = false;

  if (queryParamsStr) {
    const params = new URLSearchParams(queryParamsStr);
    if (params.has("page")) {
      const parsedPage = parseInt(params.get("page"), 10);
      if (!isNaN(parsedPage) && parsedPage > 0) pageParam = parsedPage;
    }
    if (params.has("filter") && params.get("filter") === "unmatched")
      showOnlyUnmatchedParam = true;
  }
  const mainHash = mainHashPath || (hash.includes("?") ? "" : hash);
  const prayerCodeLangRegex = /^#prayercode\/([^/]+)\/([^/?]+)/;
  const prayerCodeLangMatch = mainHash.match(prayerCodeLangRegex);

  if (mainHash.startsWith("#search/prayers/")) {
    const searchTerm = decodeURIComponent(
      mainHash.substring("#search/prayers/".length),
    );
    renderSearchResults(
      searchTerm || "",
      currentPageBySearchTerm[searchTerm] || pageParam,
    );
  } else if (prayerCodeLangMatch) {
    resolveAndRenderPrayerByPhelpsAndLang(
      decodeURIComponent(prayerCodeLangMatch[1]),
      decodeURIComponent(prayerCodeLangMatch[2]),
    );
  } else if (mainHash.startsWith("#prayercode/")) {
    const phelpsCode = decodeURIComponent(
      mainHash.substring("#prayercode/".length),
    );
    if (phelpsCode)
      renderPrayerCodeView(
        phelpsCode,
        currentPageByPhelpsCode[phelpsCode] || pageParam,
      );
    else renderLanguageList();
  } else if (mainHash.startsWith("#prayer/")) {
    const versionId = mainHash.substring("#prayer/".length);
    if (versionId) renderPrayer(versionId);
    else renderLanguageList();
  } else if (mainHash.startsWith("#prayers/")) {
    const langCode = mainHash.substring("#prayers/".length);
    if (langCode)
      renderPrayersForLanguage(langCode, pageParam, showOnlyUnmatchedParam);
    else renderLanguageList();
  } else if (mainHash === "#prayers" || mainHash === "" || mainHash === "#") {
    renderLanguageList();
  } else {
    const phelpsRegex = /^#([A-Z]{2}\d{3,5}[A-Z]{0,3})$/i;
    const phelpsMatch = mainHash.match(phelpsRegex);
    if (phelpsMatch && phelpsMatch[1]) {
      window.location.hash = `#prayercode/${phelpsMatch[1].toUpperCase()}${pageParam > 1 ? `?page=${pageParam}` : ""}`;
    } else {
      console.log("Unhandled hash:", mainHash, "defaulting to language list.");
      renderLanguageList();
    }
  }
}

function setLanguageView(langCode, page, showOnlyUnmatched) {
  window.location.hash = `#prayers/${langCode}?page=${page}${showOnlyUnmatched ? "&filter=unmatched" : ""}`;
}
window.setLanguageView = setLanguageView;
window.renderPrayersForLanguage = renderPrayersForLanguage;
window.renderSearchResults = renderSearchResults;
window.renderPrayerCodeView = renderPrayerCodeView;

document.addEventListener("DOMContentLoaded", () => {
  loadFavoritePrayers();
  const snackbarContainer = document.querySelector(".mdl-js-snackbar");

  const headerSearchInput = document.getElementById("header-search-field");
  if (headerSearchInput) {
    headerSearchInput.addEventListener("keypress", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        const searchTerm = headerSearchInput.value.trim();
        if (searchTerm) {
          currentPageBySearchTerm[searchTerm] = 1;
          window.location.hash = `#search/prayers/${encodeURIComponent(searchTerm)}`;
        }
      }
    });
  }

  const drawerSearchInput = document.getElementById("drawer-search-field");
  if (drawerSearchInput) {
    drawerSearchInput.addEventListener("keypress", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        const searchTerm = drawerSearchInput.value.trim();
        if (searchTerm) {
          currentPageBySearchTerm[searchTerm] = 1;
          window.location.hash = `#search/prayers/${encodeURIComponent(searchTerm)}`;
          const layout = document.querySelector(".mdl-layout");
          if (
            layout &&
            layout.MaterialLayout &&
            layout.MaterialLayout.drawer_
          ) {
            layout.MaterialLayout.toggleDrawer();
          }
        }
      }
    });
  }

  document
    .querySelectorAll(
      ".mdl-layout__drawer .main-drawer-nav .mdl-navigation__link",
    )
    .forEach((link) => {
      link.addEventListener("click", () => {
        const layout = document.querySelector(".mdl-layout");
        if (layout && layout.MaterialLayout && layout.MaterialLayout.drawer_) {
          layout.MaterialLayout.toggleDrawer();
        }
      });
    });

  const reviewAndSendButton = document.getElementById(
    // Was dolthub-issue-button
    "review-and-send-button",
  );
  if (reviewAndSendButton)
    reviewAndSendButton.addEventListener(
      "click",
      openDoltHubIssueDialog, // This function now opens the dialog
    );

  const clearAllItemsLink = document.getElementById(
    // Was clear-collected-matches-button
    "clear-all-items-link",
  );
  if (clearAllItemsLink) {
    clearAllItemsLink.addEventListener("click", (e) => {
      e.preventDefault();
      const hasItems = collectedMatchesForEmail.length > 0;
      const isPinned = !!pinnedPrayerDetails;

      if (!hasItems && !isPinned) {
        if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
          snackbarContainer.MaterialSnackbar.showSnackbar({
            message: "Nothing to clear.",
          });
        }
        return;
      }

      if (
        confirm(
          "Are you sure you want to clear all collected items and unpin the current prayer (if any)? This cannot be undone.",
        )
      ) {
        collectedMatchesForEmail = [];
        let message = "All collected items cleared.";
        if (isPinned) {
          unpinPrayer(); // unpinPrayer already calls updatePrayerMatchingToolDisplay and may show its own snackbar
          message = "All items cleared and prayer unpinned.";
        } else {
          updatePrayerMatchingToolDisplay(); // If not unpinning, still need to update display for cleared items
        }

        if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
          snackbarContainer.MaterialSnackbar.showSnackbar({
            message: message,
          });
        }
      }
    });
  }

  // SQL Display Dialog (no longer a primary feature, Show SQL button removed)
  // Kept for reference or if needed by other parts, but the button to open it is gone.
  const sqlDialog = document.getElementById("sql-display-dialog");
  const sqlTextarea = document.getElementById("sql-display-textarea");
  const copySqlButton = document.getElementById("copy-sql-button");
  const closeSqlDialogButton = document.getElementById(
    "close-sql-dialog-button",
  );

  // The old "Show SQL" button's functionality is now implicitly part of the dolthub-issue-dialog textarea.

  if (copySqlButton && sqlTextarea) {
    copySqlButton.addEventListener("click", () => {
      sqlTextarea.select();
      sqlTextarea.setSelectionRange(0, 99999);
      try {
        navigator.clipboard
          .writeText(sqlTextarea.value)
          .then(() => {
            if (snackbarContainer && snackbarContainer.MaterialSnackbar)
              snackbarContainer.MaterialSnackbar.showSnackbar({
                message: "SQL copied to clipboard!",
              });
          })
          .catch((err) => {
            console.error("Failed to copy with navigator.clipboard: ", err);
            document.execCommand("copy"); // Fallback
            if (snackbarContainer && snackbarContainer.MaterialSnackbar)
              snackbarContainer.MaterialSnackbar.showSnackbar({
                message: "SQL copied (fallback method)!",
              });
          });
      } catch (err) {
        console.error("Error in copy logic: ", err);
        if (snackbarContainer && snackbarContainer.MaterialSnackbar)
          snackbarContainer.MaterialSnackbar.showSnackbar({
            message: "Failed to copy SQL.",
          });
      }
    });
  }

  if (
    closeSqlDialogButton &&
    sqlDialog &&
    typeof sqlDialog.close === "function"
  ) {
    closeSqlDialogButton.addEventListener("click", () => sqlDialog.close());
    sqlDialog.addEventListener("keydown", (event) => {
      if (event.key === "Escape") sqlDialog.close();
    });
  }

  // DoltHub Issue Dialog Elements and Listeners
  const dolthubIssueDialog = document.getElementById("dolthub-issue-dialog");
  const dolthubIssueTextarea = document.getElementById(
    "dolthub-issue-textarea",
  );
  const copyDoltHubIssueTextButton = document.getElementById(
    "copy-dolthub-issue-text-button",
  );
  const openNewDoltHubIssuePageButton = document.getElementById(
    "open-new-dolthub-issue-page-button",
  );
  const sendWhatsappButton = document.getElementById(
    "send-whatsapp-dolthub-issue-button",
  );
  const sendTelegramButton = document.getElementById(
    "send-telegram-dolthub-issue-button",
  );
  const closeDoltHubIssueDialogButton = document.getElementById(
    "close-dolthub-issue-dialog-button",
  );
  const dialogMailButton = document.getElementById("dialog-mail-button");
  const dialogOpenSqlDoltHubButton = document.getElementById(
    "dialog-open-sql-doltub-button",
  );

  if (dialogMailButton) {
    dialogMailButton.addEventListener("click", sendMatchesByEmail);
  }

  if (dialogOpenSqlDoltHubButton) {
    dialogOpenSqlDoltHubButton.addEventListener("click", () => {
      if (collectedMatchesForEmail.length === 0) {
        if (snackbarContainer && snackbarContainer.MaterialSnackbar)
          snackbarContainer.MaterialSnackbar.showSnackbar({
            message: "No items to generate SQL for.",
          });
        return;
      }
      const queries = generateSqlUpdates(false); // No comments for DoltHub query
      if (queries.length === 0) {
        if (snackbarContainer && snackbarContainer.MaterialSnackbar)
          snackbarContainer.MaterialSnackbar.showSnackbar({
            message: "No SQL queries to run on DoltHub for these items.",
          });
        return;
      }
      window.open(
        `${DOLTHUB_REPO_QUERY_URL_BASE}${encodeURIComponent(queries.join("\n"))}`,
        "_blank",
      );
    });
  }

  if (copyDoltHubIssueTextButton && dolthubIssueTextarea) {
    copyDoltHubIssueTextButton.addEventListener("click", () => {
      dolthubIssueTextarea.select();
      dolthubIssueTextarea.setSelectionRange(0, 99999); // For mobile devices
      try {
        navigator.clipboard
          .writeText(dolthubIssueTextarea.value)
          .then(() => {
            if (snackbarContainer && snackbarContainer.MaterialSnackbar)
              snackbarContainer.MaterialSnackbar.showSnackbar({
                message: "Content copied!",
              });
          })
          .catch((err) => {
            console.error("Failed to copy with navigator.clipboard: ", err);
            document.execCommand("copy"); // Fallback
            if (snackbarContainer && snackbarContainer.MaterialSnackbar)
              snackbarContainer.MaterialSnackbar.showSnackbar({
                message: "Content copied (fallback)!",
              });
          });
      } catch (err) {
        console.error("Error in copy logic: ", err);
        if (snackbarContainer && snackbarContainer.MaterialSnackbar)
          snackbarContainer.MaterialSnackbar.showSnackbar({
            message: "Failed to copy content.",
          });
      }
    });
  }

  if (openNewDoltHubIssuePageButton) {
    openNewDoltHubIssuePageButton.addEventListener("click", () => {
      window.open(DOLTHUB_REPO_ISSUES_NEW_URL_BASE, "_blank");
      if (snackbarContainer && snackbarContainer.MaterialSnackbar)
        snackbarContainer.MaterialSnackbar.showSnackbar({
          message: "DoltHub new issue page opened. Paste the copied content.",
          timeout: 5000,
        });
    });
  }

  if (sendWhatsappButton && dolthubIssueTextarea) {
    sendWhatsappButton.addEventListener("click", () => {
      const text = dolthubIssueTextarea.value;
      if (text) {
        const whatsappUrl = `https://wa.me/351913044570?text=${encodeURIComponent(text)}`;
        window.open(whatsappUrl, "_blank");
      } else {
        if (snackbarContainer && snackbarContainer.MaterialSnackbar)
          snackbarContainer.MaterialSnackbar.showSnackbar({
            message: "No content to send.",
          });
      }
    });
  }

  if (sendTelegramButton && dolthubIssueTextarea) {
    sendTelegramButton.addEventListener("click", () => {
      const text = dolthubIssueTextarea.value;
      if (text) {
        const telegramUrl = `https://t.me/lapingvino?text=${encodeURIComponent(text)}`;
        window.open(telegramUrl, "_blank");
      } else {
        if (snackbarContainer && snackbarContainer.MaterialSnackbar)
          snackbarContainer.MaterialSnackbar.showSnackbar({
            message: "No content to send.",
          });
      }
    });
  }

  if (
    closeDoltHubIssueDialogButton &&
    dolthubIssueDialog &&
    typeof dolthubIssueDialog.close === "function"
  ) {
    closeDoltHubIssueDialogButton.addEventListener("click", () =>
      dolthubIssueDialog.close(),
    );
    dolthubIssueDialog.addEventListener("keydown", (event) => {
      if (event.key === "Escape") dolthubIssueDialog.close();
    });
  }

  if (typeof componentHandler !== "undefined") componentHandler.upgradeDom();
  updatePrayerMatchingToolDisplay();
  handleRouteChange();
});

window.addEventListener("hashchange", handleRouteChange);
