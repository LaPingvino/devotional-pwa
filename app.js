/* eslint-env browser */
/* global marked */

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

// Request debouncing and caching
let requestCache = new Map();
let requestDebounce = new Map();
// const REQUEST_DEBOUNCE_DELAY = 50; // 50ms - Currently unused
const REQUEST_CACHE_TTL = 5 * 60 * 1000; // 5 minutes

const MAX_DIRECT_LINKS_IN_HEADER = 4;
const ITEMS_PER_PAGE = 20;
const LOCALSTORAGE_PRAYER_CACHE_PREFIX = "hw_prayer_cache_";
const FAVORITES_STORAGE_KEY = "hw_favorite_prayers";
const MAX_PREVIEW_LENGTH = 120; // Max length for card preview text
const LANGUAGE_NAMES_CACHE_KEY = "hw_language_names_cache";
const LANGUAGE_NAMES_CACHE_EXPIRY_MS = 7 * 24 * 60 * 60 * 1000; // 7 days
const FETCH_LANG_NAMES_TIMEOUT_MS = 5000; // 5 seconds

// --- Language Statistics Cache ---
const LANGUAGE_STATS_CACHE_KEY = "devotionalPWA_languageStats";
const LANGUAGE_STATS_CACHE_EXPIRY_MS = 4 * 60 * 60 * 1000; // 4 hours
// --- End Language Statistics Cache ---

// --- Recent Language Storage ---
const RECENT_LANGUAGES_KEY = "devotionalPWA_recentLanguages";
const MAX_RECENT_LANGUAGES = 4; // Number of recent languages to store

// --- Markdown Rendering Functions ---
function renderMarkdown(text) {
  if (!text) return '';
  
  // Check if marked library is available
  if (typeof marked === 'undefined') {
    console.warn('Marked library not available, rendering text as-is with basic formatting');
    return text.replace(/\n/g, '<br>');
  }
  
  // Configure marked for prayer content
  marked.setOptions({
    breaks: true,        // Convert line breaks to <br>
    gfm: true,          // Enable GitHub Flavored Markdown
    sanitize: false,    // Allow HTML (we trust our prayer content)
    smartypants: true   // Use smart quotes and dashes
  });
  
  try {
    return marked.parse(text);
  } catch (error) {
    console.error('Error parsing Markdown:', error);
    // Fallback to basic HTML formatting
    return text.replace(/\n/g, '<br>');
  }
}

function extractPrayerNameFromMarkdown(text) {
  if (!text) return null;
  
  // Look for headers (## Header Name or # Header Name)
  const headerMatch = text.match(/^#{1,2}\s*(.+?)$/m);
  if (headerMatch) {
    let name = headerMatch[1].trim();
    // Remove markdown formatting from the extracted name
    name = name.replace(/\*\*(.*?)\*\*/g, '$1'); // Remove bold
    name = name.replace(/\*(.*?)\*/g, '$1');     // Remove italic
    name = name.replace(/`(.*?)`/g, '$1');       // Remove code
    return name;
  }
  
  // Look for italic text at the beginning that might be a title
  const italicMatch = text.match(/^\*([^*\n]+)\*/m);
  if (italicMatch) {
    return italicMatch[1].trim();
  }
  
  return null;
}

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
  }
  return [];
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

window.currentPrayerForStaticActions = null; // Holds { prayer, initialDisplayPrayerLanguage, nameToDisplay, languageToDisplay, finalDisplayLanguageForPhelpsMeta, phelpsIsSuggested }

function initializeStaticPrayerActions() {
    console.log("[initializeStaticPrayerActions] Initializing listeners for static prayer action buttons.");
    const snackbarContainer = document.querySelector(".mdl-js-snackbar");

    const staticPinBtn = document.getElementById('static-action-pin-this');
    if (staticPinBtn) {
        staticPinBtn.addEventListener('click', () => {
            if (window.currentPrayerForStaticActions && window.currentPrayerForStaticActions.prayer) {
                console.log("[Static Action] Pin this Prayer clicked.", window.currentPrayerForStaticActions.prayer.version);
                pinPrayer(window.currentPrayerForStaticActions.prayer);
                if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
                    snackbarContainer.MaterialSnackbar.showSnackbar({ message: "Prayer pinned!" });
                }
            } else { console.warn("Static Pin: No context."); }
        });
    } else { console.warn("Static button 'static-action-pin-this' not found."); }

    const staticAddMatchBtn = document.getElementById('static-action-add-match');
    if (staticAddMatchBtn) {
        staticAddMatchBtn.addEventListener('click', () => {
            if (window.currentPrayerForStaticActions && window.currentPrayerForStaticActions.prayer) {
                console.log("[Static Add Match Button Click] Clicked. Prayer version:", window.currentPrayerForStaticActions.prayer.version);
                addCurrentPrayerAsMatch(window.currentPrayerForStaticActions.prayer);
            } else { console.warn("[Static Add Match Button Click] No prayer context available."); }
        });
    } else { console.warn("Static button 'static-action-add-match' not found."); }

    const staticReplacePinBtn = document.getElementById('static-action-replace-pin');
    if (staticReplacePinBtn) {
        staticReplacePinBtn.addEventListener('click', () => {
            if (window.currentPrayerForStaticActions && window.currentPrayerForStaticActions.prayer) {
                console.log("[Static Replace Pin Button Click] Clicked. Prayer version:", window.currentPrayerForStaticActions.prayer.version);
                pinPrayer(window.currentPrayerForStaticActions.prayer); // pinPrayer replaces if already pinned, effectively
                if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
                    snackbarContainer.MaterialSnackbar.showSnackbar({ message: "Pinned prayer replaced. Item list preserved." });
                }
            } else { console.warn("[Static Replace Pin Button Click] No prayer context available."); }
        });
    } else { console.warn("Static button 'static-action-replace-pin' not found."); }

    const staticSuggestPhelpsBtn = document.getElementById('static-action-suggest-phelps');
    if (staticSuggestPhelpsBtn) {
        staticSuggestPhelpsBtn.addEventListener('click', () => {
            const context = window.currentPrayerForStaticActions;
            if (context && context.prayer) {
                console.log("[Static Suggest Phelps Button Click] Clicked. Prayer version:", context.prayer.version);
                const enteredCode = prompt(`Enter Phelps code for:\\n${context.prayer.name || "Version " + context.prayer.version}\\n(${context.initialDisplayPrayerLanguage})`);
                if (enteredCode && enteredCode.trim() !== "") {
                    addPhelpsCodeToMatchList(context.prayer, enteredCode.trim());
                }
            } else { console.warn("[Static Suggest Phelps Button Click] No prayer context available."); }
        });
    } else { console.warn("Static button 'static-action-suggest-phelps' not found."); }

    const staticChangeLangBtn = document.getElementById('static-action-change-lang');
    if (staticChangeLangBtn) {
        staticChangeLangBtn.addEventListener('click', () => {
            const context = window.currentPrayerForStaticActions;
            if (context && context.prayer) {
                console.log("[Static Change Language Button Click] Clicked. Prayer version:", context.prayer.version);
                const newLang = prompt(`Enter new language code for:\\n${context.nameToDisplay || "Version " + context.prayer.version}\\n(V: ${context.prayer.version})\\nCurrent language: ${context.finalDisplayLanguageForPhelpsMeta}`, context.languageToDisplay);
                if (newLang && newLang.trim() !== "" && newLang.trim().toLowerCase() !== context.languageToDisplay.toLowerCase()) {
                    addLanguageChangeToMatchList(context.prayer, newLang.trim());
                } else if (newLang && newLang.trim().toLowerCase() === context.languageToDisplay.toLowerCase()) {
                    alert("New language is the same as the current language.");
                }
            } else { console.warn("[Static Change Language Button Click] No prayer context available."); }
        });
    } else { console.warn("Static button 'static-action-change-lang' not found."); }

    const staticChangeNameBtn = document.getElementById('static-action-change-name');
    if (staticChangeNameBtn) {
        staticChangeNameBtn.addEventListener('click', () => {
            const context = window.currentPrayerForStaticActions;
            if (context && context.prayer) {
                console.log("[Static Change Name Button Click] Clicked. Prayer version:", context.prayer.version);
                const newName = prompt(`Enter name for:\\nVersion ${context.prayer.version} (Lang: ${context.finalDisplayLanguageForPhelpsMeta})\\nCurrent name: ${context.nameToDisplay || "Not Set"}`, context.nameToDisplay || "");
                if (newName !== null) {
                    addNameChangeToMatchList(context.prayer, newName.trim());
                }
            } else { console.warn("[Static Change Name Button Click] No prayer context available."); }
        });
    } else { console.warn("Static button 'static-action-change-name' not found."); }

    const staticAddNoteBtn = document.getElementById('static-action-add-note');
    if (staticAddNoteBtn) {
        staticAddNoteBtn.addEventListener('click', () => {
            const context = window.currentPrayerForStaticActions;
            if (context && context.prayer) {
                console.log("[Static Add Note Button Click] Clicked. Prayer version:", context.prayer.version);
                const note = prompt(`Enter a general note for:\\\\n${context.nameToDisplay || "Version " + context.prayer.version} (V: ${context.prayer.version})`);
                if (note && note.trim() !== "") {
                    addNoteToMatchList(context.prayer, note.trim());
                }
            } else { console.warn("[Static Add Note Button Click] No prayer context available."); }
        });
    } else { console.warn("Static button 'static-action-add-note' not found."); }

    const staticUnpinBtn = document.getElementById('static-action-unpin-this');
    if (staticUnpinBtn) {
        staticUnpinBtn.addEventListener('click', () => {
            if (window.currentPrayerForStaticActions && window.currentPrayerForStaticActions.prayer) {
                console.log("[Static Unpin Button Click] Clicked. Prayer version:", window.currentPrayerForStaticActions.prayer.version);
                unpinPrayer(); // unpinPrayer uses global pinnedPrayerDetails and updates UI
                if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
                    snackbarContainer.MaterialSnackbar.showSnackbar({ message: "Prayer unpinned." });
                }
            } else { console.warn("[Static Unpin Button Click] No prayer context available."); }
        });
    } else { console.warn("Static button 'static-action-unpin-this' not found."); }
}

const contentDiv = document.getElementById("content");
const prayerLanguageNav = document.getElementById("prayer-language-nav");

let currentPageByLanguage = {};
let currentPageBySearchTerm = {}; // { searchTerm: page }
let currentPageByPhelpsCode = {}; // { phelpsCode: page }
// let currentPageByPhelpsLangCode = {}; // { phelps/lang : page } - Currently unused

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
  const userLangForQuery = browserLang.replace(/'/g, "''");
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

  return await Promise.race([fetchOperationPromise, timeoutPromise]);
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
    console.warn(`getLanguageDisplayName: Display name not found for language code '${langCode}'. Returning original code.`);
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
  const snackbarContainer = document.querySelector('.mdl-js-snackbar');
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
async function updatePrayerMatchingToolDisplay() { // Made async
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
      prayerTextDiv.innerHTML = renderMarkdown(pinnedPrayerDetails.text);
      pinnedSection.appendChild(prayerTextDiv);
    }
    if (pinnedPrayerDetails.language) {
      let pageState = currentPageByLanguage[pinnedPrayerDetails.language];
      let returnUrlParams = pageState
        ? `?page=${pageState.page || 1}${pageState.showOnlyUnmatched ? "&filter=unmatched" : ""}`
        : `?page=1`;
      const returnLinkHref = `#prayers/${pinnedPrayerDetails.language}${returnUrlParams}`;
      // Remove unused pinnedNameSnippet variable
      const returnLinkP = document.createElement("p");
      returnLinkP.style.fontSize = "0.9em";
      returnLinkP.style.marginTop = "10px";
      const langDisplayNameForLink = await getLanguageDisplayName(pinnedPrayerDetails.language);
      returnLinkP.innerHTML = `To continue finding items for this language, return to <a href="${returnLinkHref}">${langDisplayNameForLink} Prayers</a>.`;
      pinnedSection.appendChild(returnLinkP);
    }
    if (typeof componentHandler !== 'undefined' && componentHandler) {
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
      if (typeof componentHandler !== 'undefined' && componentHandler) {
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

  if (typeof componentHandler !== 'undefined' && componentHandler) {
  if (
    reviewAndSendButton &&
    reviewAndSendButton.classList.contains("mdl-js-button")
  ) {
    componentHandler.upgradeElement(reviewAndSendButton);
  }
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
  // Update button states without full page re-render
  updateStaticPrayerActionButtons();
}

function unpinPrayer() {
  console.log("[unpinPrayer] Called. Current pinnedPrayerDetails before unpin:", JSON.stringify(pinnedPrayerDetails));
  const wasPinned = !!pinnedPrayerDetails;
  pinnedPrayerDetails = null;
  console.log("[unpinPrayer] pinnedPrayerDetails has been set to null.");
  updatePrayerMatchingToolDisplay(); // Will update the pinned section and clearAllLink visibility
  console.log("[unpinPrayer] Calling handleRouteChange to refresh view after unpin.");
  handleRouteChange(); // This is CRUCIAL to re-render the prayer page and update button states
  const snackbarContainer = document.querySelector(".mdl-js-snackbar");
  if (wasPinned && snackbarContainer && snackbarContainer.MaterialSnackbar) {
    // The snackbar message from initializeStaticPrayerActions for the unpin button might be more immediate.
    // This one serves as a fallback or secondary notification if needed, though often the button's own feedback is enough.
    // For now, let's keep it to see its timing.
    snackbarContainer.MaterialSnackbar.showSnackbar({
      message: "Prayer unpinned (from unpinPrayer fn).",
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
  // const title = "Prayer Data Suggestions from Web Tool"; // Currently unused
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

// --- Background English Prayers Caching ---
const BACKGROUND_CACHE_STORAGE_KEY = "devotionalPWA_backgroundCacheStatus";
const BACKGROUND_CACHE_BATCH_SIZE = 50; // Number of prayers to cache at once
const BACKGROUND_CACHE_DELAY_MS = 1000; // Delay between batches to avoid overwhelming the API
const BACKGROUND_CACHE_EXPIRY_MS = 24 * 60 * 60 * 1000; // 24 hours

function getBackgroundCacheStatus() {
  try {
    const statusData = localStorage.getItem(BACKGROUND_CACHE_STORAGE_KEY);
    if (statusData) {
      const { timestamp, totalCached, lastOffset } = JSON.parse(statusData);
      const isExpired = Date.now() - timestamp > BACKGROUND_CACHE_EXPIRY_MS;
      return { timestamp, totalCached, lastOffset, isExpired };
    }
  } catch (e) {
    console.warn("Error reading background cache status:", e);
    localStorage.removeItem(BACKGROUND_CACHE_STORAGE_KEY);
  }
  return { timestamp: 0, totalCached: 0, lastOffset: 0, isExpired: true };
}

function setBackgroundCacheStatus(totalCached, lastOffset) {
  try {
    const statusData = {
      timestamp: Date.now(),
      totalCached,
      lastOffset
    };
    localStorage.setItem(BACKGROUND_CACHE_STORAGE_KEY, JSON.stringify(statusData));
  } catch (e) {
    console.warn("Error saving background cache status:", e);
  }
}

async function loadEnglishPrayersInBackground() {
  const status = getBackgroundCacheStatus();
  
  // Skip if we've cached recently and it's not expired
  if (!status.isExpired && status.totalCached > 0) {
    console.log(`[Background Cache] Skipping - ${status.totalCached} English prayers cached recently`);
    return;
  }

  console.log("[Background Cache] Starting English prayers background caching...");
  
  try {
    // First, get a count of English prayers to cache
    const countSql = "SELECT COUNT(*) as total FROM writings WHERE language = 'en'";
    const countResult = await executeQuery(countSql);
    const totalEnglishPrayers = countResult[0]?.total || 0;
    
    if (totalEnglishPrayers === 0) {
      console.log("[Background Cache] No English prayers found in database");
      return;
    }

    console.log(`[Background Cache] Found ${totalEnglishPrayers} English prayers to cache`);
    
    // Get already cached English prayers to avoid duplicates
    const cachedPrayers = getAllCachedPrayers();
    const cachedEnglishVersions = new Set(
      cachedPrayers
        .filter(p => p.language === 'en')
        .map(p => p.version)
    );
    
    let totalCached = 0;
    let currentOffset = status.lastOffset;
    let batchCount = 0;
    const maxBatches = Math.ceil(totalEnglishPrayers / BACKGROUND_CACHE_BATCH_SIZE);
    
    while (currentOffset < totalEnglishPrayers && batchCount < maxBatches) {
      // Query for a batch of English prayers
      const batchSql = `SELECT version, name, text, language, phelps, source, link 
                        FROM writings 
                        WHERE language = 'en' 
                        ORDER BY version 
                        LIMIT ${BACKGROUND_CACHE_BATCH_SIZE} 
                        OFFSET ${currentOffset}`;
      
      const prayers = await executeQuery(batchSql);
      
      if (prayers.length === 0) {
        break; // No more prayers to cache
      }
      
      // Cache each prayer if not already cached
      let batchCached = 0;
      for (const prayer of prayers) {
        if (!cachedEnglishVersions.has(prayer.version)) {
          cachePrayerText(prayer);
          cachedEnglishVersions.add(prayer.version);
          batchCached++;
        }
      }
      
      totalCached += batchCached;
      currentOffset += prayers.length;
      batchCount++;
      
      console.log(`[Background Cache] Batch ${batchCount}/${maxBatches}: cached ${batchCached} new prayers (${totalCached} total)`);
      
      // Update status periodically
      setBackgroundCacheStatus(totalCached, currentOffset);
      
      // Add delay between batches to avoid overwhelming the API
      if (currentOffset < totalEnglishPrayers) {
        await new Promise(resolve => setTimeout(resolve, BACKGROUND_CACHE_DELAY_MS));
      }
    }
    
    console.log(`[Background Cache] Completed! Cached ${totalCached} English prayers for better search performance`);
    
    // Show a subtle notification if we cached a significant number
    if (totalCached > 10) {
      const snackbarContainer = document.querySelector(".mdl-js-snackbar");
      if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
        snackbarContainer.MaterialSnackbar.showSnackbar({
          message: `✨ Cached ${totalCached} English prayers for faster search`,
          timeout: 4000
        });
      }
    }
    
  } catch (error) {
    console.error("[Background Cache] Error caching English prayers:", error);
  }
}

function startBackgroundCaching() {
  // Start background caching after a short delay to let the main page load first
  setTimeout(() => {
    loadEnglishPrayersInBackground();
  }, 3000); // 3 second delay after page load
}
// --- End Background English Prayers Caching ---

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
  const cacheKey = sql;
  const now = Date.now();
  
  // Check cache first
  if (requestCache.has(cacheKey)) {
    const cached = requestCache.get(cacheKey);
    if (now - cached.timestamp < REQUEST_CACHE_TTL) {
      console.log("[executeQuery] Returning cached result for:", sql.substring(0, 100));
      return cached.data;
    } else {
      requestCache.delete(cacheKey);
    }
  }
  
  // Debounce identical requests
  if (requestDebounce.has(cacheKey)) {
    console.log("[executeQuery] Waiting for existing request:", sql.substring(0, 100));
    return requestDebounce.get(cacheKey);
  }
  
  const fullUrl = DOLTHUB_API_BASE_URL + encodeURIComponent(sql);
  console.log("[executeQuery] Fetching URL:", fullUrl);
  console.log("[executeQuery] Original SQL for this request:", sql);

  const requestPromise = (async () => {
    try {
      const response = await fetch(fullUrl);

      console.log("[executeQuery] Response status:", response.status);
      console.log("[executeQuery] Response ok:", response.ok);

      const responseText = await response.text();
      console.log("[executeQuery] Raw response text:", responseText);

      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}, body: ${responseText}`);
      }

      let data;
      try {
        data = JSON.parse(responseText);
        console.log("[executeQuery] Parsed JSON data:", data);
      } catch (jsonError) {
        console.error("[executeQuery] Failed to parse response text as JSON:", jsonError);
        console.error("[executeQuery] Response text that failed parsing was:", responseText);
        throw new Error(`Failed to parse API response as JSON. HTTP status: ${response.status}`);
      }
      
      const result = data.rows || [];
      
      // Cache the result
      requestCache.set(cacheKey, {
        data: result,
        timestamp: now
      });
      
      return result;
    } catch (error) {
      console.error("[executeQuery] Error during executeQuery fetch or processing:", error);
      if (!(error.message && error.message.startsWith("HTTP error!"))) {
          console.error("[executeQuery] SQL that failed (network or other error):", sql);
      }
      throw error;
    } finally {
      // Clean up debounce
      requestDebounce.delete(cacheKey);
    }
  })();
  
  // Store the promise for debouncing
  requestDebounce.set(cacheKey, requestPromise);
  
  return requestPromise;
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
async function createPrayerCardHtml(prayerData, allPhelpsDetails = {}, languageDisplayMap = {}) {
  const {
    version,
    name,
    language = "N/A",
    phelps,
    opening_text,
    link,
  } = prayerData;

  const displayLanguageForTitle = languageDisplayMap[language] || await getLanguageDisplayName(language);
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

    if (typeof componentHandler !== 'undefined' && componentHandler) {
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

// Function removed - drawer functionality not currently used

/**
 * @typedef {import('./models/view-spec.js').ViewSpec} ViewSpec
 */

/**
 * Renders the overall page layout, including header, title, language switcher (optional),
 * and a main content area populated by a view-specific renderer.
 *
 * @async
 * @function renderPageLayout
 * @param {ViewSpec} viewSpec - The specification for the view to render.
 *                                See /models/view-spec.js for definition.
 */
async function renderPageLayout(viewSpec) {
    const {
        titleKey, // required
        contentRenderer, // required
        showLanguageSwitcher = true,
        showBackButton = false,
        customHeaderContentRenderer = null,
        activeLangCodeForPicker = null, // New parameter from ViewSpec
        isPrayerPage = false // New flag to identify prayer pages
    } = viewSpec;
    console.log("[renderPageLayout] Received viewSpec.titleKey:", titleKey, "isPrayerPage:", isPrayerPage);
    console.log("[renderPageLayout] Received viewSpec.activeLangCodeForPicker:", activeLangCodeForPicker);

    // Hide static prayer actions host if not on a prayer page
    const staticActionsHostGlobal = document.getElementById('static-prayer-actions-host');
    if (staticActionsHostGlobal) {
        if (!isPrayerPage) {
            staticActionsHostGlobal.style.display = 'none';
            console.log("[renderPageLayout] Hiding static-prayer-actions-host as not on a prayer page.");
        } else {
            // Show it on prayer pages - it will be properly configured by _renderPrayerContent
            staticActionsHostGlobal.style.display = 'flex';
            console.log("[renderPageLayout] Showing static-prayer-actions-host on prayer page.");
        }
    }

    const headerTitleSpan = document.getElementById('page-main-title');
    const headerFavoriteButton = document.getElementById('page-header-favorite-button');
    // Define the specific area for dynamic content, leaving the header (#page-header-container) intact.
    const dynamicContentHostElement = document.getElementById('dynamic-content-area'); 

    if (!dynamicContentHostElement) {
        console.error("Critical: #dynamic-content-area not found in DOM. Page layout cannot proceed.");
        // Fallback: Clear the main contentDiv if dynamic area is missing, and show error.
        // This might still wipe the title if it's inside contentDiv, but it's a fallback.
        if (contentDiv) {
            contentDiv.innerHTML = '<p style="color:red; text-align:center; padding:20px;">Error: Page structure is broken (#dynamic-content-area missing).</p>';
        }
        return; 
    }

    // 1. Reset Header elements
    if (headerFavoriteButton) {
        headerFavoriteButton.style.display = 'none'; // Individual views can re-enable and configure it.
    }

    // Clean up elements potentially added by previous renderPageLayout calls
    const headerRow = headerTitleSpan ? headerTitleSpan.closest('.mdl-layout__header-row') : null;
    if (headerRow) {
        const existingBackButton = headerRow.querySelector('.page-layout-back-button');
        if (existingBackButton) {
            existingBackButton.remove();
        }
        // If a drawer button was hidden by a previous back button, ensure it's visible
        // This part is tricky as it assumes knowledge of the drawer button's state.
        // For now, we only manage our own back button. If drawer interaction is needed,
        // it has to be more robust (e.g. CSS classes on header row).
        // const drawerButton = headerRow.querySelector('.mdl-layout__drawer-button');
        // if (drawerButton && drawerButton.style.display === 'none' && !showBackButton) {
        // drawerButton.style.display = '';
        // }
    }


    if (customHeaderContentRenderer) {
        if (headerTitleSpan) {
            // customHeaderContentRenderer is responsible for the content of the title area
            console.log("[renderPageLayout] Using customHeaderContentRenderer.");
            const customContent = customHeaderContentRenderer(); // Assuming this is synchronous for now
            if (typeof customContent === 'string') {
                headerTitleSpan.innerHTML = customContent;
            } else if (customContent instanceof Node) {
                headerTitleSpan.innerHTML = ''; // Clear previous
                headerTitleSpan.appendChild(customContent);
            } else {
                // Fallback if custom renderer returns nothing or unexpected type
                // Attempt to use titleKey as a safeguard
                const titleText = titleKey || 'Untitled';
                headerTitleSpan.textContent = titleText;
            }
        }
    } else {
        // Default header: Title + optional Back Button
        if (headerTitleSpan) {
            const titleText = titleKey || 'Untitled';
            console.log("[renderPageLayout] Setting headerTitleSpan.textContent to:", titleText);
            headerTitleSpan.textContent = titleText;
        }

        if (showBackButton) {
            if (headerRow) {
                const backButton = document.createElement('button');
                backButton.className = 'mdl-button mdl-js-button mdl-button--icon page-layout-back-button';
                backButton.innerHTML = '<i class="material-icons">arrow_back</i>';
                const backButtonTitle = 'Back';
                backButton.title = backButtonTitle;
                backButton.onclick = () => window.history.back();

                // Insert before the title, or after drawer button if present.
                // The drawer button is usually the first interactive element.
                const drawerButton = headerRow.querySelector('.mdl-layout__drawer-button');
                if (drawerButton) {
                     // Hide the drawer button when a back button is shown.
                     // This is a design choice; adjust if both should be visible.
                    drawerButton.style.display = 'none';
                    headerRow.insertBefore(backButton, drawerButton); // effectively replacing it
                } else if (headerTitleSpan) {
                    // If no drawer button, insert before title.
                    headerTitleSpan.parentNode.insertBefore(backButton, headerTitleSpan);
                } else {
                    // Fallback: insert as first child of header row
                    headerRow.insertBefore(backButton, headerRow.firstChild);
                }
                
                if (typeof componentHandler !== 'undefined' && componentHandler) {
                    componentHandler.upgradeElement(backButton);
                }
            }
        } else {
             // If not showing back button, ensure drawer button (if exists and was hidden by us) is visible
            if (headerRow) {
                const drawerButton = headerRow.querySelector('.mdl-layout__drawer-button');
                if (drawerButton && drawerButton.style.display === 'none') {
                    drawerButton.style.display = ''; // Make it visible again
                }
            }
        }
    }

    // 2. Clear the specific dynamic content area
    dynamicContentHostElement.innerHTML = '';

    // 3. Render Language Switcher (if requested) into the dynamic area
    if (showLanguageSwitcher) {
        const pickerShellHtml = getLanguagePickerShellHtml(); // Assumes app.getLanguagePickerShellHtml or global
        dynamicContentHostElement.insertAdjacentHTML('beforeend', pickerShellHtml);
        // populateLanguageSelection is async and handles its own MDL upgrades.
        // Pass activeLangCodeForPicker to highlight the correct language.
        await populateLanguageSelection(activeLangCodeForPicker);
    }

    // 4. Prepare container for view-specific content within the dynamic area
    const viewContentContainer = document.createElement('div');
    viewContentContainer.id = 'view-specific-content-container';
    dynamicContentHostElement.appendChild(viewContentContainer);

    // 5. Render view-specific content with a loading spinner
    const tempSpinner = document.createElement('div');
    tempSpinner.className = 'mdl-spinner mdl-js-spinner is-active';
    tempSpinner.style.margin = 'auto';
    tempSpinner.style.display = 'block';
    tempSpinner.style.marginTop = '20px';
    viewContentContainer.appendChild(tempSpinner);

    if (typeof componentHandler !== 'undefined' && componentHandler) {
        componentHandler.upgradeElement(tempSpinner); // Upgrade the spinner itself
    }

    try {
        const renderedViewContent = await contentRenderer(); // Call the callback

        // Clear spinner before adding new content
        // (viewContentContainer still exists, tempSpinner was its child)
        tempSpinner.remove();

        if (typeof renderedViewContent === 'string') {
            viewContentContainer.innerHTML = renderedViewContent;
        } else if (renderedViewContent instanceof Node) { // Catches HTMLElement, DocumentFragment
            // If contentRenderer returns a Node, ensure viewContentContainer is empty before appending
            viewContentContainer.innerHTML = '';
            viewContentContainer.appendChild(renderedViewContent);
        }
        // If renderedViewContent is null/undefined (void return), viewContentContainer remains empty.
        // This assumes contentRenderer does not directly manipulate viewContentContainer if it returns void.
        // It's safer if contentRenderer always returns content to be placed.

        // 6. Upgrade MDL components in the newly added view-specific content
        if (typeof componentHandler !== 'undefined' && componentHandler) {
            console.log('[renderPageLayout] Upgrading DOM for view-specific content in #view-specific-content-container.');
            componentHandler.upgradeDom(viewContentContainer);
        }
    } catch (error) {
        console.error("Error during contentRenderer execution or processing:", error);
        if (tempSpinner.parentNode) { // Check if spinner is still in DOM
          tempSpinner.remove();
        }
        viewContentContainer.innerHTML = `<p style="color:red; text-align:center; padding: 20px;">Error loading view content: ${error.message}</p>`;
        // Optionally, upgrade this error message if it contains MDL classes (though unlikely here)
        if (typeof componentHandler !== 'undefined' && componentHandler) {
            componentHandler.upgradeDom(viewContentContainer);
        }
    }
    // No final broad MDL upgrade; rely on targeted upgrades.
}

/**
 * Calculates the dynamic title for a prayer page, considering suggestions.
 * @param {object} prayer - The core prayer object from the database.
 * @param {Array} collectedMatchesForEmailFromApp - The global collectedMatchesForEmail array.
 * @returns {Promise<object>} An object containing titleText, and suggestion details.
 */
async function _calculatePrayerPageTitle(prayer, collectedMatchesForEmailFromApp) {
  console.log("[_calculatePrayerPageTitle] Input prayer:", JSON.stringify(prayer));
  console.log("[_calculatePrayerPageTitle] Input collectedMatchesForEmailFromApp:", JSON.stringify(collectedMatchesForEmailFromApp));
  if (!prayer) {
    console.log("[_calculatePrayerPageTitle] Prayer object is null/undefined, returning default title info.");
    return {
      titleText: "Prayer not found",
      nameIsSuggested: false,
      phelpsIsSuggested: false,
      languageIsSuggested: false,
      phelpsToDisplay: null,
      languageToDisplay: null,
      nameToDisplay: null
    };
  }

  const initialDisplayPrayerLanguage = await getLanguageDisplayName(prayer.language);
  let prayerTitleText = prayer.name ||
    (prayer.phelps ? `${prayer.phelps} - ${initialDisplayPrayerLanguage}` : null) ||
    (prayer.text ? prayer.text.substring(0, 50) + "..." : `Prayer ${prayer.version}`);

  let phelpsToDisplay = prayer.phelps;
  let phelpsIsSuggested = false;
  let languageToDisplay = prayer.language;
  let languageIsSuggested = false;
  let nameToDisplay = prayer.name;
  let nameIsSuggested = false;

  for (const match of collectedMatchesForEmailFromApp) {
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
      }
    }
    if (match.type === "match_prayers") {
      const updatePhelpsIfNeeded = (targetVersion, suggestedPhelps) => {
        if (targetVersion === prayer.version && !prayer.phelps) {
          phelpsToDisplay = suggestedPhelps;
          phelpsIsSuggested = true;
        }
      };
      if (match.current.version === prayer.version && match.pinned.phelps) {
        updatePhelpsIfNeeded(prayer.version, match.pinned.phelps);
      }
      if (match.pinned.version === prayer.version && match.current.phelps) {
        updatePhelpsIfNeeded(prayer.version, match.current.phelps);
      }
      if (!prayer.phelps && (match.pinned.version === prayer.version || match.current.version === prayer.version) && !match.pinned.phelps && !match.current.phelps) {
        phelpsToDisplay = `TODO${uuidToBase36(match.pinned.version)}`;
        phelpsIsSuggested = true;
      }
    }
  }
  const displayLangForTitleAfterSuggestions = await getLanguageDisplayName(languageToDisplay);
  prayerTitleText = nameToDisplay || (phelpsToDisplay ? `${phelpsToDisplay} - ${displayLangForTitleAfterSuggestions}` : `Prayer ${prayer.version}`);

  console.log("[_calculatePrayerPageTitle] Calculated prayerTitleText:", prayerTitleText);
  return {
    titleText: prayerTitleText,
    nameIsSuggested,
    phelpsIsSuggested,
    languageIsSuggested,
    phelpsToDisplay,
    languageToDisplay,
    nameToDisplay
  };
}


async function _renderPrayerContent(prayerObject, phelpsCodeForNav, activeLangForNav, titleCalculationResults) {
  const staticPageHeaderFavoriteButton = document.getElementById('page-header-favorite-button');
  // const staticPageMainTitle = document.getElementById('page-main-title'); // Title is set by renderPageLayout

  if (!prayerObject) {
    // This case should ideally be handled before calling _renderPrayerContent,
    // e.g., in renderPrayer or resolveAndRenderPrayerByPhelpsAndLang.
    return `<p>Error: Prayer data not provided to content renderer.</p>`;
  }
  const prayer = prayerObject; // Use a consistent variable name internally
  const authorName = getAuthorFromPhelps(prayer.phelps);
  
  // These are needed for button prompts and context for static actions
  const initialDisplayPrayerLanguage = await getLanguageDisplayName(prayer.language);
  const phelpsToDisplay = titleCalculationResults.phelpsToDisplay; // Effective Phelps code for display
  const languageToDisplay = titleCalculationResults.languageToDisplay; // Effective language for display
  const nameToDisplay = titleCalculationResults.nameToDisplay; // Effective name for display
  const effectiveLanguageDisplayName = await getLanguageDisplayName(languageToDisplay); // Display name of effective language

  // Set context for static button event listeners
  // Ensure all potentially needed values from titleCalculationResults are included
  window.currentPrayerForStaticActions = {
    prayer: prayer,
    initialDisplayPrayerLanguage: initialDisplayPrayerLanguage,
    nameToDisplay: nameToDisplay,
    languageToDisplay: languageToDisplay,
    finalDisplayLanguageForPhelpsMeta: effectiveLanguageDisplayName, // Use the renamed variable here
    phelpsIsSuggested: titleCalculationResults.phelpsIsSuggested
  };

  // Update static page header elements - only favorite button here
  if (staticPageHeaderFavoriteButton) {
    staticPageHeaderFavoriteButton.style.display = 'inline-flex';
    staticPageHeaderFavoriteButton.innerHTML = isPrayerFavorite(prayer.version)
        ? '<i class="material-icons">star</i> Favorited'
        : '<i class="material-icons">star_border</i> Favorite';
    staticPageHeaderFavoriteButton.onclick = () => toggleFavoritePrayer(prayer, staticPageHeaderFavoriteButton);
    if (typeof componentHandler !== 'undefined' && componentHandler) {
        componentHandler.upgradeElement(staticPageHeaderFavoriteButton);
    }
  }
  
  cachePrayerText({ ...prayer });

  const fragment = document.createDocumentFragment();

  // 1. Translations Switcher
  const translationsAreaDiv = document.createElement('div');
  translationsAreaDiv.id = 'prayer-translations-switcher-area';
  translationsAreaDiv.className = 'translations-switcher';

  // --- BEGIN TEMPORARY LOGS for TranslationSwitcher ---
  console.log("[TranslationSwitcher] Input phelpsCodeForNav:", phelpsCodeForNav);
  console.log("[TranslationSwitcher] Input phelpsToDisplay:", phelpsToDisplay);
  // --- END TEMPORARY LOGS ---

  const phelpsCodeForSwitcher = phelpsCodeForNav || phelpsToDisplay;

  // --- BEGIN TEMPORARY LOGS for TranslationSwitcher ---
  console.log("[TranslationSwitcher] Determined phelpsCodeForSwitcher:", phelpsCodeForSwitcher);
  // --- END TEMPORARY LOGS ---

  if (phelpsCodeForSwitcher && !phelpsCodeForSwitcher.startsWith("TODO")) {
    const escapedPhelpsCode = phelpsCodeForSwitcher.replace(/'/g, "''"); // Correctly escape single quotes in the Phelps code
    const transSql = `SELECT DISTINCT language FROM writings WHERE phelps = '${escapedPhelpsCode}' AND phelps IS NOT NULL AND phelps != '' ORDER BY language`; // Use standard SQL string literals
    
    // --- BEGIN TEMPORARY LOGS for TranslationSwitcher ---
    console.log("[TranslationSwitcher] SQL for distinct languages:", transSql);
    // --- END TEMPORARY LOGS ---

    try {
      const distinctLangs = await executeQuery(transSql);

      // --- BEGIN TEMPORARY LOGS for TranslationSwitcher ---
      console.log("[TranslationSwitcher] Distinct languages found (raw):", JSON.stringify(distinctLangs));
      console.log("[TranslationSwitcher] Number of distinct languages:", distinctLangs ? distinctLangs.length : 'null/undefined');
      // --- END TEMPORARY LOGS ---

      if (distinctLangs && distinctLangs.length > 1) {
        let switcherHtml = `<span class="translations-switcher-label">Translations:</span>`;
        const translationLinkPromises = distinctLangs.map(async (langRow) => {
          const langDisplayName = await getLanguageDisplayName(langRow.language);
          const isActive = activeLangForNav ? (langRow.language === activeLangForNav) : (langRow.language === prayer.language);
          return `<a href="#prayercode/${phelpsCodeForSwitcher}/${langRow.language}" class="translation-link ${isActive ? 'is-active' : ''}">${langDisplayName}</a>`;
        });
        const translationLinksHtml = await Promise.all(translationLinkPromises);
        switcherHtml += translationLinksHtml.join(' ');
        translationsAreaDiv.innerHTML = switcherHtml;
      } else {
        // --- BEGIN TEMPORARY LOGS for TranslationSwitcher ---
        console.log("[TranslationSwitcher] Condition 'distinctLangs && distinctLangs.length > 1' is false. Not enough distinct languages to show switcher.");
        // --- END TEMPORARY LOGS ---
        translationsAreaDiv.innerHTML = '';
      }
    } catch (error) {
      console.error("[TranslationSwitcher] Error fetching translations for switcher:", error);
      translationsAreaDiv.innerHTML = '<span class="translations-switcher-label" style="color:red;">Error loading translations.</span>';
    }
  } else {
    // --- BEGIN TEMPORARY LOGS for TranslationSwitcher ---
    console.log("[TranslationSwitcher] Condition 'phelpsCodeForSwitcher && !phelpsCodeForSwitcher.startsWith(\"TODO\")' is false. Phelps code is invalid or TODO, not showing switcher.");
    // --- END TEMPORARY LOGS ---
    translationsAreaDiv.innerHTML = '';
  }
  fragment.appendChild(translationsAreaDiv);

  // 2. Prayer Details Container
  const prayerDetailsContainer = document.createElement('div');
  prayerDetailsContainer.id = 'prayer-details-area';
  
  const finalDisplayLanguageForPhelpsMeta = await getLanguageDisplayName(languageToDisplay);
  let phelpsDisplayHtml = "Not Assigned";
  if (phelpsToDisplay) {
    const textPart = titleCalculationResults.phelpsIsSuggested
      ? `<span style="font-weight: bold; color: red;">${phelpsToDisplay}</span>`
      : `<a href="#prayercode/${phelpsToDisplay}">${phelpsToDisplay}</a>`;
    phelpsDisplayHtml = `${textPart} (Lang: ${finalDisplayLanguageForPhelpsMeta})`;
  } else {
    phelpsDisplayHtml = `Not Assigned (Lang: ${finalDisplayLanguageForPhelpsMeta})`;
  }
  if (titleCalculationResults.languageIsSuggested) {
    phelpsDisplayHtml += ` <span style="font-weight: bold; color: red;">(New Lang: ${finalDisplayLanguageForPhelpsMeta})</span>`;
  }

  // Render prayer text with Markdown support
  const renderedPrayerText = renderMarkdown(prayer.text || "No text available.");
  
  // Try to extract prayer name from Markdown if not already set
  let displayName = prayer.name;
  if (!displayName && prayer.text) {
    const extractedName = extractPrayerNameFromMarkdown(prayer.text);
    if (extractedName) {
      displayName = extractedName;
      console.log(`[_renderPrayerContent] Extracted prayer name from Markdown: "${extractedName}"`);
    }
  }

  const prayerCoreHtml = `
    <div class="scripture">
        <div class="prayer markdown-content">${renderedPrayerText}</div>
        ${authorName ? `<div class="author">${authorName}</div>` : ""}
        ${prayer.source ? `<div style="font-size: 0.8em; margin-left: 2em; margin-top: 0.5em; font-style: italic;">Source: ${prayer.source} ${prayer.link ? `(<a href="${prayer.link}" target="_blank">${getDomain(prayer.link) || "link"}</a>)` : ""}</div>` : ""}
        <div style="font-size: 0.7em; margin-left: 2em; margin-top: 0.3em; color: #555;">Phelps ID: ${phelpsDisplayHtml}</div>
        <div style="font-size: 0.7em; margin-left: 2em; margin-top: 0.3em; color: #555;">Version ID: ${prayer.version}</div>
    </div>`;
  prayerDetailsContainer.innerHTML = prayerCoreHtml;

  // --- Prayer Actions Section (Managed Static Elements Approach) ---
  // window.currentPrayerForStaticActions is now an object, set earlier.

  const staticHost = document.getElementById('static-prayer-actions-host');
  console.log("[_renderPrayerContent] staticHost from getElementById:", staticHost); // DEBUG
  // These variables are retrieved but not used directly in this function
  // They're here for future use if needed
  // const staticPinBtn = document.getElementById('static-action-pin-this');
  // const staticAddMatchBtn = document.getElementById('static-action-add-match');
  // const staticReplacePinBtn = document.getElementById('static-action-replace-pin');
  // const staticIsPinnedMsg = document.getElementById('static-action-is-pinned-msg');
  // const staticUnpinBtn = document.getElementById('static-action-unpin-this');
  // const staticSuggestPhelpsBtn = document.getElementById('static-action-suggest-phelps');
  // const staticChangeLangBtn = document.getElementById('static-action-change-lang');
  // const staticChangeNameBtn = document.getElementById('static-action-change-name');
  // const staticAddNoteBtn = document.getElementById('static-action-add-note');

  if (staticHost) {
      // Don't move the staticHost - keep it in its original location
      // Just ensure it's visible and update button states
      staticHost.style.display = 'flex'; // Ensure the host container is visible

      console.log("[_renderPrayerContent] staticHost processed. Display:", staticHost.style.display); // DEBUG

      // Update button states
      updateStaticPrayerActionButtonStates(prayer);
  }
  // --- End Static Prayer Actions ---

  fragment.appendChild(prayerDetailsContainer);

  // 3. "Other versions in this language" (if applicable)
  if (phelpsToDisplay && (phelpsCodeForNav === phelpsToDisplay) && prayer.language) { // Ensure prayer.language exists
    const sameLangVersionsSql = `SELECT version, name, link FROM writings WHERE phelps = '${phelpsToDisplay.replace(/'/g, "''")}' AND language = '${prayer.language.replace(/'/g, "''")}' AND version != '${prayer.version}' ORDER BY name`;
    try {
        const sameLangVersions = await executeQuery(sameLangVersionsSql);
        if (sameLangVersions.length > 0) {
            const otherVersionsDiv = document.createElement("div");
            otherVersionsDiv.style.marginTop = "20px";
            otherVersionsDiv.style.paddingTop = "15px";
            otherVersionsDiv.style.borderTop = "1px solid #eee";
            const langForOtherVersionsTitle = await getLanguageDisplayName(prayer.language);
            let otherListHtml = `<h5>Other versions in ${langForOtherVersionsTitle} for ${phelpsToDisplay}:</h5><ul class="other-versions-in-lang-list">`;
            sameLangVersions.forEach((altVersion) => {
                const displayName = altVersion.name || `Version ${altVersion.version}`
                const domain = altVersion.link ? `(${getDomain(altVersion.link)})` : "";
                otherListHtml += `<li><a href="#prayer/${altVersion.version}">${displayName} ${domain}</a></li>`;
            });
            otherListHtml += `</ul>`;
            otherVersionsDiv.innerHTML = otherListHtml;
            prayerDetailsContainer.appendChild(otherVersionsDiv); // Appends to prayerDetailsContainer, which is part of fragment
        }
    } catch (e) {
        console.error("Error fetching other versions in same language:", e);
        // Optionally append an error message to prayerDetailsContainer
    }
  }
  return fragment; // Return the constructed DOM fragment
}

async function renderPrayer(
  versionId,
  phelpsCodeForNav = null,
  activeLangForNav = null,
) {
  console.log(`[renderPrayer] Called with versionId: ${versionId}, phelpsCodeForNav: ${phelpsCodeForNav}, activeLangForNav: ${activeLangForNav}`);
  updateHeaderNavigation([]); // Clear any language-specific header links from other views

  // 1. Fetch FULL prayer data
  const prayerSql = `SELECT version, text, language, phelps, name, source, link FROM writings WHERE version = '${versionId.replace(/'/g, "''")}'`;
  let prayerRows;
  try {
    prayerRows = await executeQuery(prayerSql);
  } catch (e) {
    console.error(`Error fetching prayer ${versionId}:`, e);
    await renderPageLayout({
        titleKey: "Error Loading Prayer",
        contentRenderer: () => `<div id="prayer-view-error"><p>Error loading prayer data: ${e.message}</p><p>Version ID: ${versionId}</p><p>Query: ${prayerSql}</p></div>`,
        showLanguageSwitcher: true, 
        showBackButton: true,
        activeLangCodeForPicker: activeLangForNav // Use activeLangForNav if available for picker context
    });
    return;
  }

  if (!prayerRows || prayerRows.length === 0) {
    await renderPageLayout({
        titleKey: "Prayer Not Found",
        contentRenderer: () => `<div id="prayer-not-found"><p>Prayer with ID ${versionId} not found.</p><p>Query: ${prayerSql}</p></div>`,
        showLanguageSwitcher: true, 
        showBackButton: true,
        activeLangCodeForPicker: activeLangForNav // Use activeLangForNav if available
    });
    return;
  }
  
  const prayerData = prayerRows[0];
  console.log("[renderPrayer] Fetched prayerData:", JSON.stringify(prayerData));
  
  // 2. Calculate title using the new helper function
  const titleInfo = await _calculatePrayerPageTitle(prayerData, collectedMatchesForEmail);
  console.log("[renderPrayer] titleInfo from _calculatePrayerPageTitle:", JSON.stringify(titleInfo));
  
  // Determine the active language for the language picker
  const activeLanguageForPicker = activeLangForNav || prayerData.language;

  // 3. Call renderPageLayout with the dynamically calculated title and content renderer
  const viewSpecForPrayer = {
    titleKey: titleInfo.titleText, // Use the fully calculated title from titleInfo
    contentRenderer: () => _renderPrayerContent(prayerData, phelpsCodeForNav, activeLangForNav, titleInfo),
    showLanguageSwitcher: true, 
    showBackButton: true,
    activeLangCodeForPicker: activeLanguageForPicker,
    isPrayerPage: true // Indicate this is a prayer page
  };
  console.log("[renderPrayer] ViewSpec being passed to renderPageLayout:", JSON.stringify(viewSpecForPrayer, (key, value) => typeof value === 'function' ? 'Function' : value));
  await renderPageLayout(viewSpecForPrayer);
}

async function _renderPrayersForLanguageContent(langCode, page, showOnlyUnmatched, languageDisplayName) {
  const offset = (page - 1) * ITEMS_PER_PAGE;

  let filterCondition = showOnlyUnmatched
    ? " AND (phelps IS NULL OR phelps = '')"
    : "";

  // Optimized: Get both metadata and text in a single query
  const metadataSql = `SELECT version, name, language, phelps, link, text FROM writings WHERE language = '${langCode}'${filterCondition} ORDER BY name, version LIMIT ${ITEMS_PER_PAGE} OFFSET ${offset}`;
  let prayersMetadata;
  try {
    prayersMetadata = await executeQuery(metadataSql);
  } catch (error) {
    console.error(`Error fetching prayers for language ${langCode}:`, error);
    return `<div id="language-content-area"><p style="color:red; text-align:center;">Error loading prayers: ${error.message}</p><pre>${metadataSql}</pre></div>`;
  }

  const countSql = `SELECT COUNT(*) as total FROM writings WHERE language = '${langCode}'${filterCondition}`;
  let countResult;
  try {
    countResult = await executeQuery(countSql);
  } catch (error) {
    console.error(`Error fetching count for language ${langCode}:`, error);
    // Continue with potentially incomplete data or show error alongside, for now, let metadata error handling dominate if it also fails.
    // If metadata succeeded, this error means pagination might be wrong.
    countResult = [{ total: 0 }]; // Fallback to prevent further errors
  }
  
  const totalPrayers = countResult.length > 0 ? countResult[0].total : 0;
  const totalPages = Math.ceil(totalPrayers / ITEMS_PER_PAGE);

  const filterSwitchId = `filter-unmatched-${langCode}`;
  const filterSwitchHtml = `<div class="filter-switch-container"><label class="mdl-switch mdl-js-switch mdl-js-ripple-effect" for="${filterSwitchId}"><input type="checkbox" id="${filterSwitchId}" class="mdl-switch__input" onchange="setLanguageView('${langCode}', 1, this.checked)" ${showOnlyUnmatched ? "checked" : ""}><span class="mdl-switch__label">Show only prayers without Phelps code</span></label></div>`;

  if (prayersMetadata.length === 0 && page === 1) {
    return `<div id="language-content-area">${filterSwitchHtml}<p>No prayers found for language: ${languageDisplayName}${showOnlyUnmatched ? " (matching filter)" : ""}.</p><p>Query for metadata:</p><pre>${metadataSql}</pre><p><a href="${DOLTHUB_REPO_QUERY_URL_BASE}${encodeURIComponent(metadataSql)}" target="_blank">Debug metadata query</a></p><p>Count query:</p><pre>${countSql}</pre><p><a href="${DOLTHUB_REPO_QUERY_URL_BASE}${encodeURIComponent(countSql)}" target="_blank">Debug count query</a></p></div>`;
  }
  if (prayersMetadata.length === 0 && page > 1) {
    // This indicates current page is out of bounds, redirect to a valid page.
    // The calling function `renderPrayersForLanguage` will handle navigation if needed,
    // but setLanguageView is more direct here.
    // However, contentRenderer should ideally not cause navigation side effects.
    // For now, we'll return a message, and handle re-routing in the main function or router.
    // The original did setLanguageView() which re-triggers routing. This is a tricky one.
    // To prevent re-render loop if totalPages is 0, ensure Math.max(1, totalPages).
    // For now, let the main function or router handle the navigation if page is invalid.
    // This might mean renderPrayersForLanguage needs to check totalPages before calling contentRenderer for a specific page.
    // Or, simply show a message here.
    console.warn(`Attempted to render page ${page} for ${langCode} which is out of bounds (total: ${totalPages}). Navigating to page ${Math.max(1,totalPages)}.`);
    setLanguageView(langCode, Math.max(1, totalPages), showOnlyUnmatched); // This will re-trigger the whole render.
    return '<div id="language-content-area"><p>Redirecting to a valid page...</p></div>'; // Placeholder until redirect happens.
  }

  // Separate cached and uncached prayers for efficient batch processing
  const cachedPrayers = [];
  const uncachedPrayers = [];
  
  prayersMetadata.forEach(pMeta => {
    const cached = getCachedPrayerText(pMeta.version);
    if (cached) {
      // Use cached data
      if (cached.name) pMeta.name = cached.name;
      if (cached.phelps) pMeta.phelps = cached.phelps;
      if (cached.link) pMeta.link = cached.link;
      const opening_text_for_card = cached.text
        ? cached.text.substring(0, MAX_PREVIEW_LENGTH) + (cached.text.length > MAX_PREVIEW_LENGTH ? "..." : "")
        : "No text preview available.";
      cachedPrayers.push({ ...pMeta, opening_text: opening_text_for_card });
    } else {
      uncachedPrayers.push(pMeta);
    }
  });

  // Batch fetch all uncached prayer texts in a single query
  let uncachedPrayersWithText = [];
  if (uncachedPrayers.length > 0) {
    // Limit batch size to avoid URL length issues
    const maxBatchSize = 50;
    const batches = [];
    for (let i = 0; i < uncachedPrayers.length; i += maxBatchSize) {
      batches.push(uncachedPrayers.slice(i, i + maxBatchSize));
    }
    
    const allTextRows = [];
    for (const batch of batches) {
      const versionIds = batch.map(p => `'${p.version.replace(/'/g, "''")}'`).join(',');
      const batchTextSql = `SELECT version, text FROM writings WHERE version IN (${versionIds})`;
      try {
        const textRows = await executeQuery(batchTextSql);
        allTextRows.push(...textRows);
      } catch (error) {
        console.error("Error fetching prayer texts in batch:", error);
        // Continue with other batches
      }
    }
    
    const textMap = {};
    allTextRows.forEach(row => {
      textMap[row.version] = row.text;
    });
    
    uncachedPrayersWithText = uncachedPrayers.map(pMeta => {
      const full_text_for_preview = textMap[pMeta.version] || null;
      if (full_text_for_preview) {
        // Cache the prayer text
        cachePrayerText({
          version: pMeta.version, text: full_text_for_preview, name: pMeta.name,
          language: pMeta.language, phelps: pMeta.phelps, link: pMeta.link
        });
      }
      const opening_text_for_card = full_text_for_preview
        ? full_text_for_preview.substring(0, MAX_PREVIEW_LENGTH) + (full_text_for_preview.length > MAX_PREVIEW_LENGTH ? "..." : "")
        : "No text preview available.";
      return { ...pMeta, opening_text: opening_text_for_card };
    });
  }
  
  const prayersForDisplay = [...cachedPrayers, ...uncachedPrayersWithText];

  let allPhelpsDetailsForCards = {};
  const phelpsCodesInList = [...new Set(prayersForDisplay.filter((p) => p.phelps).map((p) => p.phelps))];
  if (phelpsCodesInList.length > 0) {
    const phelpsInClause = phelpsCodesInList.map((p) => `'${p.replace(/'/g, "''")}'`).join(",");
    const translationsSql = `SELECT version, language, phelps, name, link FROM writings WHERE phelps IN (${phelpsInClause})`;
    try {
      const translationRows = await executeQuery(translationsSql);
      translationRows.forEach((row) => {
        if (!allPhelpsDetailsForCards[row.phelps]) allPhelpsDetailsForCards[row.phelps] = [];
        allPhelpsDetailsForCards[row.phelps].push({
          version: row.version, language: row.language, name: row.name, link: row.link,
        });
      });
    } catch (e) {
      console.error("Failed to fetch details for phelps codes:", e);
    }
  }

  // Pre-fetch all language display names for better performance
  const uniqueLanguages = [...new Set(prayersForDisplay.map(p => p.language))];
  const languageDisplayMap = {};
  
  // Batch fetch language display names in parallel
  const languageDisplayPromises = uniqueLanguages.map(async langCode => {
    const displayName = await getLanguageDisplayName(langCode);
    return [langCode, displayName];
  });
  
  const languageDisplayResults = await Promise.all(languageDisplayPromises);
  languageDisplayResults.forEach(([langCode, displayName]) => {
    languageDisplayMap[langCode] = displayName;
  });
  
  const listCardPromises = prayersForDisplay.map((pData) => createPrayerCardHtml(pData, allPhelpsDetailsForCards, languageDisplayMap));
  const listCardsHtmlArray = await Promise.all(listCardPromises);
  const listHtml = `<div class="favorite-prayer-grid">${listCardsHtmlArray.join("")}</div>`;

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
  
  const internalHeaderHtml = `<header><h2><span id="category">Prayers</span><span id="blocktitle">Language: ${languageDisplayName} (Page ${page})${showOnlyUnmatched ? " - Unmatched" : ""}</span></h2></header>`;
  return `<div id="language-content-area">${internalHeaderHtml}${filterSwitchHtml}${listHtml}${paginationHtml}</div>`;
}

async function renderPrayersForLanguage(
  langCode,
  page = 1,
  showOnlyUnmatched = false,
) {
  addRecentLanguage(langCode); 
  currentPageByLanguage[langCode] = { page, showOnlyUnmatched };
  
  // Clear main page header nav, as this view's primary navigation is via the language picker
  // and its own content (pagination, filters).
  updateHeaderNavigation([]);

  const languageDisplayName = await getLanguageDisplayName(langCode);
  const pageTitleForLayout = `Prayers in ${languageDisplayName}`;

  await renderPageLayout({
    titleKey: pageTitleForLayout, // Using dynamic string
    contentRenderer: async () => {
      // The languageDisplayName is passed to the content renderer for its internal header.
      return _renderPrayersForLanguageContent(langCode, page, showOnlyUnmatched, languageDisplayName);
    },
    showLanguageSwitcher: true,
    showBackButton: true, // A back button is useful when viewing a specific language list.
    activeLangCodeForPicker: langCode // Ensures the correct tab is active in the picker.
  });
}

async function _renderPrayerCodeViewContent(phelpsCode, page) {
  const offset = (page - 1) * ITEMS_PER_PAGE;

  // Fetch and set header navigation links for different languages of this Phelps code.
  const phelpsLangsSql = `SELECT DISTINCT language FROM writings WHERE phelps = '${phelpsCode.replace(/'/g, "''")}' ORDER BY language`;
  let navLinks = [];
  try {
    const distinctLangs = await executeQuery(phelpsLangsSql);
    navLinks = await Promise.all(distinctLangs.map(async (langRow) => ({
        text: await getLanguageDisplayName(langRow.language),
        href: `#prayercode/${phelpsCode}/${langRow.language}`,
        isActive: false, 
    })));
  } catch (error) {
      console.error("Error fetching languages for Phelps code navigation:", error);
      // Continue without these nav links, or display an error in header? For now, just log.
  }
  updateHeaderNavigation(navLinks); // This is specific to this view.

  // Fetch metadata for the prayer list
  const metadataSql = `SELECT version, name, language, text, phelps, link FROM writings WHERE phelps = '${phelpsCode.replace(/'/g, "''")}' AND phelps IS NOT NULL AND phelps != '' ORDER BY language, name LIMIT ${ITEMS_PER_PAGE} OFFSET ${offset}`;
  let prayersMetadata;
  try {
      prayersMetadata = await executeQuery(metadataSql);
  } catch (error) {
      console.error(`Error loading prayer data for Phelps code ${phelpsCode}:`, error);
      return `<div id="prayer-code-content-area"><p style="color:red;text-align:center;">Error loading prayer data: ${error.message}.</p><pre>${metadataSql}</pre></div>`;
  }

  const countSql = `SELECT COUNT(*) as total FROM writings WHERE phelps = '${phelpsCode.replace(/'/g, "''")}' AND phelps IS NOT NULL AND phelps != ''`;
  let countResult;
  try {
      countResult = await executeQuery(countSql);
  } catch (error) {
      console.error(`Error fetching count for Phelps code ${phelpsCode}:`, error);
      countResult = [{ total: 0 }]; // Fallback
  }
  const totalPrayers = countResult.length > 0 ? countResult[0].total : 0;
  const totalPages = Math.ceil(totalPrayers / ITEMS_PER_PAGE);

  if (prayersMetadata.length === 0 && page === 1) {
    const debugQueryUrl = `${DOLTHUB_REPO_QUERY_URL_BASE}${encodeURIComponent(metadataSql)}`;
    return `<div id="prayer-code-content-area"><p>No prayer versions found for Phelps Code: ${phelpsCode}.</p><p>Query used:</p><pre>${metadataSql}</pre><p><a href="${debugQueryUrl}" target="_blank">Debug this query</a></p></div>`;
  }
  if (prayersMetadata.length === 0 && page > 1) {
    console.warn(`Attempted to render page ${page} for Phelps ${phelpsCode} which is out of bounds (total: ${totalPages}). Navigating to page ${Math.max(1,totalPages)}.`);
    renderPrayerCodeView(phelpsCode, Math.max(1, totalPages)); // Re-trigger render for valid page
    return '<div id="prayer-code-content-area"><p>Redirecting to a valid page...</p></div>';
  }

  const prayersForDisplay = prayersMetadata.map((p) => {
    let full_text_for_preview = p.text;
    const cachedText = getCachedPrayerText(p.version);
    if (cachedText && cachedText.text) {
        full_text_for_preview = cachedText.text;
        p.name = cachedText.name || p.name;
        p.language = cachedText.language || p.language;
        p.link = cachedText.link || p.link;
    } else if (p.text) {
        cachePrayerText({ ...p });
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
    try {
        const allVersionRows = await executeQuery(allVersionsSql);
        if (allVersionRows.length > 0) {
            allPhelpsDetailsForCards[phelpsCode] = allVersionRows.map((r) => ({ ...r }));
        }
    } catch (error) {
        console.error("Error fetching all versions for Phelps code details:", error);
    }
  }

  const listCardPromises = prayersForDisplay.map((pData) =>
    createPrayerCardHtml(pData, allPhelpsDetailsForCards),
  );
  const listCardsHtmlArray = await Promise.all(listCardPromises);
  const listHtml = `<div class="favorite-prayer-grid">${listCardsHtmlArray.join("")}</div>`;

  let paginationHtml = "";
  if (totalPages > 1) {
    paginationHtml = '<div class="pagination">';
    if (page > 1)
      paginationHtml += `<button class="mdl-button mdl-js-button mdl-button--raised" onclick="renderPrayerCodeView('${phelpsCode.replace(/'/g, "'")}', ${page - 1})">Previous</button>`;
    paginationHtml += ` <span>Page ${page} of ${totalPages}</span> `;
    if (page < totalPages)
      paginationHtml += `<button class="mdl-button mdl-js-button mdl-button--raised" onclick="renderPrayerCodeView('${phelpsCode.replace(/'/g, "'")}', ${page + 1})">Next</button>`;
    paginationHtml += "</div>";
  }
  
  const internalHeaderHtml = `<header><h2><span id="category">Prayer Code</span><span id="blocktitle">${phelpsCode} (Page ${page}) - All Languages</span></h2></header>`;
  return `<div id="prayer-code-content-area">${internalHeaderHtml}${listHtml}${paginationHtml}</div>`;
}

async function renderPrayerCodeView(phelpsCode, page = 1) {
  currentPageByPhelpsCode[phelpsCode] = page;

  await renderPageLayout({
    titleKey: `Translations for ${phelpsCode}`,
    contentRenderer: async () => _renderPrayerCodeViewContent(phelpsCode, page),
    showLanguageSwitcher: true, // Original function included the language picker
    showBackButton: true,       // Useful for navigating back from this specific view
    activeLangCodeForPicker: null // No specific language is active in the picker for this general phelps view
  });
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
  // Initial spinner removed - renderPageLayout will handle loading states if needed via its contentRenderer.

  const displayTargetLanguage = await getLanguageDisplayName(targetLanguageCode);

  const sql = `SELECT version, name, text, language, phelps, source, link FROM writings WHERE phelps = '${phelpsCode.replace(/'/g, "''")}' AND language = '${targetLanguageCode.replace(/'/g, "''")}' ORDER BY name`;
  const prayerVersions = await executeQuery(sql);

  if (prayerVersions.length === 0) {
    // If no specific version found, still try to set up navigation by phelps code for other languages.
    const transSql = `SELECT DISTINCT language FROM writings WHERE phelps = '${phelpsCode.replace(/'/g, "''")}' ORDER BY language`;
    let navLinks = [];
    try {
        const distinctLangs = await executeQuery(transSql);
        navLinks = await Promise.all(distinctLangs.map(async (langRow) => ({
            text: await getLanguageDisplayName(langRow.language),
            href: `#prayercode/${phelpsCode}/${langRow.language}`,
            isActive: false, // No specific language is "active" if the target one wasn't found
        })));
    } catch(e) {
        console.error("Error fetching languages for Phelps code navigation during not-found scenario:", e);
    }
    updateHeaderNavigation(navLinks); // Update header nav with available languages for this Phelps code

    await renderPageLayout({
        titleKey: `Not Found: ${phelpsCode} in ${displayTargetLanguage}`,
        contentRenderer: () => `<div id="prayer-not-found"><p>No prayer version found for Phelps Code ${phelpsCode} in ${displayTargetLanguage}.</p><p>You can try other available languages for this Phelps code using the links in the header (if any) or by browsing.</p></div>`,
        showLanguageSwitcher: true,
        showBackButton: true,
        activeLangCodeForPicker: targetLanguageCode // Still show the target language as active context
    });
    return;
  }

  const primaryPrayer = prayerVersions[0];
  await renderPrayer(primaryPrayer.version, phelpsCode, targetLanguageCode);
  // The "Other versions in this language for this Phelps code" list, which was previously appended here,
  // should be integrated into _renderPrayerContent if desired.
  // For now, its direct DOM manipulation is removed to align with renderPageLayout pattern.
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
  // The menu will be completely rebuilt if otherLanguagesWithStats.length > 0
  // For now, just clear the message placeholder. Actual menu clearing/rebuilding happens below.
  messagePlaceholderElement.innerHTML = ''; // Clear previous message


  try {
    await fetchLanguageNames(); // Ensure names are loaded for display
  } catch (error) {
    console.warn("generateLanguageSelectionHtml: Failed to pre-load language names. Fallbacks may be used.", error.message);
  }

  const sql = `SELECT 
    language,
    SUM(CASE WHEN phelps IS NOT NULL AND phelps != '' THEN 1 ELSE 0 END) AS phelps_covered_count,
    SUM(CASE WHEN phelps IS NULL OR phelps = '' THEN 1 ELSE 0 END) AS versions_without_phelps_count
  FROM writings 
  WHERE language IS NOT NULL AND language != '' 
  GROUP BY language 
  ORDER BY language`;
  
  let allLangsWithStats = getCachedLanguageStats();
  let fetchedFreshData = false;
  let attemptedFetch = false;

  if (!allLangsWithStats) {
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
      // executeQuery now throws, so we catch it here.
      // allLangsWithStats will remain as it was (either null from initial getCachedLanguageStats or stale data if loaded later)
      // The subsequent logic will handle trying stale cache.
      allLangsWithStats = allLangsWithStats || []; // Ensure it's at least an empty array if it was null/undefined
    }
  } else {
    fetchedFreshData = true; // Technically it\'s fresh from cache
  }

  // If fetch failed or cache was empty initially, try to get stale cache
  if ((attemptedFetch && !fetchedFreshData) || !allLangsWithStats || allLangsWithStats.length === 0) {
    const staleStats = getCachedLanguageStats(true); // Allow stale
    if (staleStats && staleStats.length > 0) {
      allLangsWithStats = staleStats;
      // console.log("Using stale language stats from cache.");
      // Optionally, set a flag here to indicate to the UI that data is stale
      // For now, just using it transparently.
    } else if (!allLangsWithStats || allLangsWithStats.length === 0) {
      allLangsWithStats = []; // Ensure it\'s an array
    }
  }

  if (allLangsWithStats.length === 0) {
    // const debugQueryUrl = `${DOLTHUB_REPO_QUERY_URL_BASE}${encodeURIComponent(sql)}`;
    // return `<p>No languages found.</p><p>Query:</p><pre>${sql}</pre><p><a href=\"${debugQueryUrl}\" target=\"_blank\">Debug query</a></p>`;
    // Instead of returning HTML, manipulate DOM for error message
    if (messagePlaceholderElement) messagePlaceholderElement.innerHTML = '<p style="text-align:center;">No languages found to select.</p>';
    if (moreLanguagesWrapperElement) moreLanguagesWrapperElement.style.display = 'none'; // Hide more languages section
    if (tabBarElement) tabBarElement.innerHTML = ''; // Clear tab bar as well
    return; // Exit if no stats
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
    // Batch fetch all language display names for recent languages
    const recentLanguagesData = recentLanguageCodes.map(langCode => 
      allLangsWithStats.find(l => l.language.toLowerCase() === langCode)
    ).filter(Boolean);
    
    const recentLanguageNames = await Promise.all(
      recentLanguagesData.map(langData => getLanguageDisplayName(langData.language))
    );
    
    recentLanguagesData.forEach((langData, index) => {
      const displayName = recentLanguageNames[index];
      const phelpsCount = parseInt(langData.phelps_covered_count, 10) || 0;
      const nonPhelpsCount = parseInt(langData.versions_without_phelps_count, 10) || 0;
      const totalConceptualPrayers = phelpsCount + nonPhelpsCount;
      recentLangDetails.push({
        code: langData.language,
        display: displayName,
        phelps: phelpsCount,
        total: totalConceptualPrayers,
      });
    });

    if (recentLangDetails.length > 0) {
      const recentTabsHtml = recentLangDetails.map(lang => {
        // A recent language tab is active only if currentActiveLangCode is present and matches
        const isActive = currentActiveLangCode && (lang.code.toLowerCase() === currentActiveLangCode.toLowerCase());
        return `<a href="#language-tab-panel-all" 
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

  // let allLanguagesMenuHtml = ""; // This variable is not used to build the final string for the section anymore
  const searchInputId = "language-search-input"; // Used for filterLanguageMenu closure
  const allLanguagesMenuUlId = "all-languages-menu-ul"; // Used for filterLanguageMenu closure

  if (otherLanguagesWithStats.length > 0) {
    moreLanguagesWrapperElement.style.display = 'inline-block'; // Show the "More Languages" section

    // Augment languages with their display names for sorting
    const languagesWithDisplayNamesPromises = otherLanguagesWithStats.map(async (langData) => {
      const displayName = await getLanguageDisplayName(langData.language);
      return { ...langData, displayName }; // Keep original stats, add displayName
    });
    const augmentedLanguages = await Promise.all(languagesWithDisplayNamesPromises);

    // Sort by display name
    augmentedLanguages.sort((a, b) => a.displayName.localeCompare(b.displayName));

    // Generate HTML list items from the sorted list
    const menuItemsHtml = augmentedLanguages.map(langData => {
      const langCode = langData.language;
      const displayName = langData.displayName; // Already fetched
      const phelpsCount = parseInt(langData.phelps_covered_count, 10) || 0;
      const nonPhelpsCount = parseInt(langData.versions_without_phelps_count, 10) || 0;
      const totalConceptualPrayers = phelpsCount + nonPhelpsCount;
      return `<li class="mdl-menu__item" onclick="setLanguageView('${langCode}', 1, false)" data-val="${langCode}">${displayName} (${phelpsCount}/${totalConceptualPrayers})</li>`;
    }).join('\n');

    if (menuUlElement) {
        const searchLiHtml = `
            <li id="language-search-li" onclick="event.stopPropagation();">
                <div class="mdl-textfield mdl-js-textfield">
                    <input class="mdl-textfield__input" type="text" id="${searchInputId}" onkeyup="filterLanguageMenu()" onclick="event.stopPropagation();">
                    <label class="mdl-textfield__label" for="${searchInputId}">Search languages...</label>
                </div>
            </li>`;
        const dividerHtml = '<li class="mdl-menu__divider" style="margin-top:0;"></li>';
        
        menuUlElement.innerHTML = searchLiHtml + dividerHtml + menuItemsHtml; // Rebuild entire menu content
    }

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
// The menuItemsHtml (string of LIs) has already been used to rebuild menuUlElement.innerHTML
// No need for tempDiv and appending children one by one here anymore.

} // Closes 'if (otherLanguagesWithStats.length > 0)'
else if (allLangsWithStats.length > 0 && recentLangDetails.length === allLangsWithStats.length && recentLangDetails.length > 0) {
    if (messagePlaceholderElement) messagePlaceholderElement.innerHTML = `<p style="font-size:0.9em; color:#555;">All available languages are shown in "Recent".</p>`;
    if (menuUlElement) menuUlElement.innerHTML = ''; // Clear the menu UL if no "other" languages but this message is shown
    const moreButton = document.getElementById('all-languages-menu-btn');
    if(moreButton) moreButton.style.display = 'none'; // Hide the button
    if(moreLanguagesWrapperElement) moreLanguagesWrapperElement.style.display = 'inline-block'; // Show wrapper for the message

} else {
// No "other" languages and not all are recent (or no languages at all)
// Hide the "More Languages" section entirely if there are no other languages
  if (menuUlElement) menuUlElement.innerHTML = ''; // Clear the menu UL
  if (moreLanguagesWrapperElement) {
    moreLanguagesWrapperElement.style.display = 'none';
  }
}

// Populate tab bar:
if (tabBarElement) {
    // Clear existing <a> tabs (direct children, not in a wrapper)
    Array.from(tabBarElement.children).forEach(child => {
        if (child.tagName === 'A' && child.classList.contains('mdl-tabs__tab')) {
            child.remove();
        }
    });

    if (recentAndFavoritesTabsBarHtml) {
        const tempDiv = document.createElement('div');
        tempDiv.innerHTML = recentAndFavoritesTabsBarHtml;
        const newTabLinks = Array.from(tempDiv.children);
        const currentMoreLanguagesWrapper = document.getElementById('more-languages-wrapper'); // Re-fetch, DOM might have changed

        if (currentMoreLanguagesWrapper && currentMoreLanguagesWrapper.parentNode === tabBarElement) {
            newTabLinks.forEach(link => {
                tabBarElement.insertBefore(link, currentMoreLanguagesWrapper);
            });
        } else {
            console.warn("More Languages wrapper not correctly found as a direct child of tab bar during tab injection. Appending tabs to end.");
            newTabLinks.forEach(link => { // Fallback append
                tabBarElement.appendChild(link);
            });
        }
    }
}
// No redundant if(tabBarElement) checks needed here.

// Ensure MDL components in the picker are upgraded if they were dynamically added or modified.
// This is crucial for tabs, menu, button ripples, textfield in search.
if (typeof componentHandler !== 'undefined' && componentHandler) {
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

// Helper function to render the specific content for the language list view.

async function _fetchAndDisplayRandomPrayer(containerElement) { // Changed parameter name
  if (!containerElement) { // Check the element directly
    console.error("Random prayer container element not provided or invalid.");
    return;
  }
  containerElement.innerHTML = '<div class="mdl-spinner mdl-js-spinner is-active" style="margin: auto; display: block; padding: 20px 0;"></div>';
  if (typeof componentHandler !== 'undefined' && componentHandler) {
    componentHandler.upgradeDom(containerElement);
  }

  try {
    // Fetching random prayer in three steps for performance:
    // 1. Count total prayers in the database. This helps determine the range for a random offset for metadata retrieval.
    // 2. Get a random prayer's metadata (version, name, lang, phelps) using this offset.
    //    Ordering by an indexed column (e.g., 'version') is crucial for efficient OFFSET.
    //    This step does NOT initially filter by text presence to ensure the count and offset operations are fast.
    // 3. Fetch the actual TEXT field for the selected prayer's version in a separate, targeted query.
    //    This ensures the large TEXT field is only retrieved by its primary key/indexed 'version'.
    //    A check for text presence is done AFTER retrieval.

    const countSql = "SELECT COUNT(*) as total FROM writings"; // Count all prayers
    const countResult = await executeQuery(countSql);
    // Ensure countResult is valid and has a 'total' property before parsing
    const totalPrayers = countResult && countResult.length > 0 && typeof countResult[0].total !== 'undefined'
        ? parseInt(countResult[0].total, 10)
        : 0;

    if (totalPrayers === 0) {
      // This message now means no prayers in the entire table, which is unlikely but a good guard.
      containerElement.innerHTML = '<p style="text-align:center; padding:10px;">No prayers found in the database.</p>';
      return;
    }

    const randomOffset = Math.floor(Math.random() * totalPrayers);
    // Order by version (assuming it's indexed) for efficient OFFSET
    // No WHERE clause on 'text' here for performance; text presence will be checked after fetching.
    const prayerMetadataSql = `SELECT version, name, language, phelps FROM writings ORDER BY version LIMIT 1 OFFSET ${randomOffset}`;
    const prayerMetadataRows = await executeQuery(prayerMetadataSql);

    if (!prayerMetadataRows || prayerMetadataRows.length === 0) {
      containerElement.innerHTML = '<p style="color:red; text-align:center; padding:10px;">Could not load random prayer metadata.</p>';
      return;
    }

    const prayerMetadata = prayerMetadataRows[0];

    // Third step: Fetch the prayer text separately using its version ID
    const prayerTextSql = `SELECT text FROM writings WHERE version = '${prayerMetadata.version}'`;
    const prayerTextRows = await executeQuery(prayerTextSql);

    // Check if text was successfully fetched and is not empty
    if (!prayerTextRows || prayerTextRows.length === 0 || typeof prayerTextRows[0].text === 'undefined' || prayerTextRows[0].text.trim() === '') {
      // If the randomly selected prayer has no text or empty text, inform the user.
      // Avoid retrying here to prevent potential loops if many prayers lack text or if text fetching is problematic.
      containerElement.innerHTML = '<p style="text-align:center; padding:10px;">Prayer of the Moment: Selected prayer has no displayable text.</p>';
      return;
    }

    // Combine metadata with text
    const prayer = { ...prayerMetadata, text: prayerTextRows[0].text };
    const langDisplayName = await getLanguageDisplayName(prayer.language);

    let prayerHtml = `
      <div class="random-prayer-card" style="padding: 15px; margin-bottom: 20px; background-color: #fff; border: 1px solid #e0e0e0; border-radius: 4px; box-shadow: 0 1px 3px rgba(0,0,0,0.12), 0 1px 2px rgba(0,0,0,0.24);">
        <h4 style="margin-top:0; font-size: 1.2em; color: #3f51b5;">Prayer of the Moment</h4>
        <p style="font-size: 0.9em; color: #555; margin-bottom:10px;"><em>${prayer.name || (prayer.phelps ? `${prayer.phelps} - ${langDisplayName}` : `A prayer in ${langDisplayName}`)}</em></p>
        <div class="scripture markdown-content" style="max-height: 150px; overflow-y: auto; padding: 10px; border: 1px solid #eee; margin-bottom:15px; font-size: 0.95em; line-height:1.5;">
          ${renderMarkdown(prayer.text.substring(0, 400) + (prayer.text.length > 400 ? '...' : ''))}
        </div>
        <a href="#prayer/${prayer.version}" class="mdl-button mdl-js-button mdl-button--raised mdl-button--accent">
          <i class="material-icons" style="margin-right:4px;">open_in_new</i> Read Full Prayer
        </a>
      </div>
    `;
    containerElement.innerHTML = prayerHtml;
    if (typeof componentHandler !== 'undefined' && componentHandler) {
      componentHandler.upgradeDom(containerElement);
    }

  }
 catch (error) {
   console.error("Error fetching or displaying random prayer:", error);
   containerElement.innerHTML = '<p style="color:red; text-align:center; padding:10px;">Could not load Prayer of the Moment.</p>';
 }
}

async function _renderLanguageListContent() {
    const overallWrapper = document.createElement('div');

    // 1. Random Prayer Section (placeholder)
    const randomPrayerPlaceholder = document.createElement('div');
    randomPrayerPlaceholder.id = 'random-prayer-module'; // Changed ID for clarity
    randomPrayerPlaceholder.style.marginBottom = '20px';
    // Initial placeholder text, spinner will be added by _fetchAndDisplayRandomPrayer
    randomPrayerPlaceholder.innerHTML = '<p style="text-align:center; padding:10px;"><em>Loading Prayer of the Moment...</em></p>'; 
    overallWrapper.appendChild(randomPrayerPlaceholder);

    // 2. Main area for language list specific content (favorites currently)
    const langListSpecificContent = document.createElement('div');
    langListSpecificContent.innerHTML = `
      <div id="main-content-area-for-langlist" style="min-height: 100px;">
        <div class="mdl-tabs__panel is-active" id="language-tab-panel-favorites">
          <!-- Favorites content will be loaded here -->
        </div>
      </div>
      <p class="text-center" style="font-size:0.9em; color: #555; margin-top:20px;">
        Counts are (Unique Phelps Codes / Total Unique Prayers). Select a language to browse.
      </p>
    `;
    const favoritesPanel = langListSpecificContent.querySelector('#language-tab-panel-favorites');
    overallWrapper.appendChild(langListSpecificContent);
    
    // Logic to load and display favorites
    let localFavoritesDisplayHtml = "";
    if (favoritePrayers.length > 0) {
      localFavoritesDisplayHtml += `<h3>⭐ Your Favorite Prayers</h3>`; // Removed id="favorite-prayers-section" to avoid duplicate IDs if this content is embedded elsewhere.
      const phelpsCodesToFetchForFavs = [
        ...new Set(
          favoritePrayers.filter((fp) => fp.phelps).map((fp) => fp.phelps),
        ),
      ];
      let allPhelpsDetailsForFavCards = {};

      if (phelpsCodesToFetchForFavs.length > 0) {
        const phelpsInClause = phelpsCodesToFetchForFavs
          .map((p) => `'${p.replace(/'/g, "''")}'`)
          .join(",");
        const favTranslationsSql = `SELECT version, language, phelps, name, link FROM writings WHERE phelps IN (${phelpsInClause})`;
        try {
          const translationRows = await executeQuery(favTranslationsSql);
          translationRows.forEach((row) => {
            if (!allPhelpsDetailsForFavCards[row.phelps])
              allPhelpsDetailsForFavCards[row.phelps] = [];
            allPhelpsDetailsForFavCards[row.phelps].push({ ...row });
          });
        } catch (e) {
          console.error("Failed to fetch details for favorite phelps codes:", e);
          //Gracefully continue, cards will show fewer details
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
        
        if (cached && cached.text) {
          textForCardPreview =
            cached.text.substring(0, MAX_PREVIEW_LENGTH) +
            (cached.text.length > MAX_PREVIEW_LENGTH ? "..." : "");
        }
        
        favoriteCardPromises.push(
          createPrayerCardHtml(
            {
              version: fp.version,
              name: nameForCard || '',
              language: langForCard || 'N/A',
              phelps: phelpsForCard || '',
              opening_text: textForCardPreview || '',
              link: linkForCard || '',
              // source: cached ? cached.source : null // createPrayerCardHtml doesn't currently use 'source'
            },
            allPhelpsDetailsForFavCards,
          ),
        );
      }
      const favoriteCardsHtmlArray = await Promise.all(favoriteCardPromises);
      localFavoritesDisplayHtml += `<div class="favorite-prayer-grid">${favoriteCardsHtmlArray.join("")}</div>`;
    } else {
      localFavoritesDisplayHtml += `<div class="text-center" style="padding: 20px 0; margin-bottom:10px;"><p>You haven't favorited any prayers yet. <br/>Click the <i class="material-icons" style="vertical-align: bottom; font-size: 1.2em;">star_border</i> icon on a prayer's page to add it here!</p></div>`;
    }

    if (favoritesPanel) {
        favoritesPanel.innerHTML = localFavoritesDisplayHtml;
    }
    
    // Kick off the async fetch for the random prayer. 
    // It will update its placeholder div when ready.
    // No await here, let it load in background.
    _fetchAndDisplayRandomPrayer(randomPrayerPlaceholder); 

    return overallWrapper;
}

async function renderLanguageList() {

  // Clear header navigation specific to other views
  updateHeaderNavigation([]);

  // Reset page state for various views
  // Reset pagination state
  currentPageByLanguage = {};
  currentPageBySearchTerm = {};
  currentPageByPhelpsCode = {};
  // currentPageByPhelpsLangCode = {}; // Currently unused

  await renderPageLayout({
    titleKey: "Browse Prayers", // Using direct string as getLocalizedString might not be set up
    contentRenderer: _renderLanguageListContent,
    showLanguageSwitcher: true, // This view uses the language switcher extensively
    showBackButton: false,      // No back button on the main/home view
  });
}

async function _renderSearchResultsContent(searchTerm, page) {
  const saneSearchTermForSql = searchTerm.replace(/'/g, "''");
  const lowerSearchTerm = searchTerm.toLowerCase();
  const localFoundItems = [];
  const allCached = getAllCachedPrayers();
  allCached.forEach((cachedPrayer) => {
    let match = false;
    if (cachedPrayer.text && cachedPrayer.text.toLowerCase().includes(lowerSearchTerm)) match = true;
    if (!match && cachedPrayer.name && cachedPrayer.name.toLowerCase().includes(lowerSearchTerm)) match = true;
    if (match) {
      localFoundItems.push({
        ...cachedPrayer,
        opening_text: cachedPrayer.text
          ? cachedPrayer.text.substring(0, MAX_PREVIEW_LENGTH) + (cachedPrayer.text.length > MAX_PREVIEW_LENGTH ? "..." : "")
          : "No text preview.",
        source: "cache",
      });
    }
  });

  const dbNameSql = `SELECT version, name, language, phelps, link, source FROM writings WHERE name LIKE '%${saneSearchTermForSql}%' ORDER BY name, version`;
  let dbNameItems = [];
  try {
    const dbNameItemsRaw = await executeQuery(dbNameSql);
    dbNameItems = dbNameItemsRaw.map((item) => ({ ...item, source: "db_name" }));
  } catch (error) {
    console.error("Error fetching search results from DB (name match):", error);
    return `<div id="search-results-content-area"><p style="color:red;text-align:center;">Error loading search results: ${error.message}</p><pre>${dbNameSql}</pre></div>`;
  }

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
  let currentPage = page; // Use a local variable for current page in this render
  const totalPages = Math.ceil(totalResults / ITEMS_PER_PAGE);

  // Adjust currentPage if out of bounds (can happen if results change, e.g. cache update)
  if (currentPage > totalPages && totalPages > 0) {
      currentPage = totalPages;
      // Update the global state for next time this search is run
      currentPageBySearchTerm[searchTerm] = currentPage;
      // Re-trigger with corrected page. This is a side-effect, consider if there's a better way.
      // For now, mimicking existing pagination corrections by re-calling the main render.
      console.warn(`Search page out of bounds. Redirecting to page ${currentPage} for "${searchTerm}"`);
      renderSearchResults(searchTerm, currentPage);
      return '<div id="search-results-content-area"><p>Redirecting to a valid page...</p></div>';
  } else if (totalPages === 0 && currentPage > 1) {
      currentPage = 1;
      currentPageBySearchTerm[searchTerm] = currentPage;
      console.warn(`Search page out of bounds (no results). Redirecting to page ${currentPage} for "${searchTerm}"`);
      renderSearchResults(searchTerm, currentPage);
      return '<div id="search-results-content-area"><p>Redirecting to a valid page...</p></div>';
  }


  const startIndex = (currentPage - 1) * ITEMS_PER_PAGE;
  const paginatedCombinedResults = combinedResults.slice(startIndex, startIndex + ITEMS_PER_PAGE);
  const displayItems = [];

  for (const item of paginatedCombinedResults) {
    let displayItem = { ...item };
    if ((item.source === "db_name" || !item.opening_text) && !item.text) {
      const cached = getCachedPrayerText(item.version);
      if (cached && cached.text) {
        displayItem = { ...item, ...cached, text: cached.text };
      } else {
        const textQuerySql = `SELECT text, name, phelps, language, link, source FROM writings WHERE version = '${item.version}' LIMIT 1`;
        try {
          const textRows = await executeQuery(textQuerySql);
          if (textRows.length > 0) {
            const dbItemWithText = textRows[0];
            displayItem = { ...item, ...dbItemWithText, text: dbItemWithText.text };
            cachePrayerText({ ...dbItemWithText });
          } else {
            displayItem.text = null;
          }
        } catch (error) {
          console.error(`Error fetching text for search result item ${item.version}:`, error);
          displayItem.text = null;
        }
      }
    }
    
    if (displayItem.text) {
      displayItem.opening_text = displayItem.text.substring(0, MAX_PREVIEW_LENGTH) + (displayItem.text.length > MAX_PREVIEW_LENGTH ? "..." : "");
    } else {
      displayItem.opening_text = "No text preview available.";
    }
    displayItems.push(displayItem);
  }

  if (totalResults === 0) {
    const searchExplanationForNoResults = `<div style="margin-top: 15px; padding: 10px; background-color: #f0f0f0; border-radius: 4px; font-size: 0.9em;"><p style="margin-bottom: 5px;"><strong>Search Information:</strong></p><ul style="margin-top: 0; padding-left: 20px;"><li>This search looked for "${searchTerm}" in prayer <strong>titles</strong> from the main database.</li><li>It also checked the <strong>full text</strong> of ${allCached.length} prayer(s) stored in your browser's local cache.</li><li>Try Browse <a href="#languages">language lists</a> to add prayers to cache for full-text search.</li></ul></div>`;
    const internalHeaderHtml = `<header><h2><span id="category">Search Results</span><span id="blocktitle">For "${searchTerm ? searchTerm.replace(/"/g, '&quot;') : ''}"</span></h2></header>`;
    return `<div id="search-results-content-area">${internalHeaderHtml}<p>No prayers found matching "${searchTerm}".</p>${searchExplanationForNoResults}<p style="margin-top: 15px;">For comprehensive text search, try <a href="https://tiddly.holywritings.net/workspace" target="_blank">tiddly.holywritings.net/workspace</a>.</p><p>Debug: <a href="${DOLTHUB_REPO_QUERY_URL_BASE}${encodeURIComponent(dbNameSql)}" target="_blank">View DB Name Query</a></p></div>`;
  }

  let allPhelpsDetailsForCards = {};
  const phelpsCodesInList = [...new Set(displayItems.filter((p) => p.phelps).map((p) => p.phelps))];
  if (phelpsCodesInList.length > 0) {
    const phelpsInClause = phelpsCodesInList.map((p) => `'${p.replace(/'/g, "''")}'`).join(",");
    const translationsSql = `SELECT version, language, phelps, name, link FROM writings WHERE phelps IN (${phelpsInClause})`;
    try {
      const translationRows = await executeQuery(translationsSql);
      translationRows.forEach((row) => {
        if (!allPhelpsDetailsForCards[row.phelps]) allPhelpsDetailsForCards[row.phelps] = [];
        allPhelpsDetailsForCards[row.phelps].push({ ...row });
      });
    } catch (e) {
      console.error("Failed to fetch details for phelps codes in search:", e);
    }
  }

  const listCardPromises = displayItems.map((pData) => createPrayerCardHtml(pData, allPhelpsDetailsForCards));
  const listCardsHtmlArray = await Promise.all(listCardPromises);
  const listHtml = `<div class="favorite-prayer-grid">${listCardsHtmlArray.join("")}</div>`;

  let paginationHtml = "";
  if (totalPages > 1) {
    const escapedSearchTermForJs = searchTerm.replace(/'/g, "\\'").replace(/"/g, '\\"');
    paginationHtml = '<div class="pagination">';
    if (currentPage > 1) {
      paginationHtml += `<button class="mdl-button mdl-js-button mdl-button--raised" onclick="renderSearchResults('${escapedSearchTermForJs}', ${currentPage - 1})">Previous</button>`;
    }
    paginationHtml += ` <span>Page ${currentPage} of ${totalPages}</span> `;
    if (currentPage < totalPages) {
      paginationHtml += `<button class="mdl-button mdl-js-button mdl-button--raised" onclick="renderSearchResults('${escapedSearchTermForJs}', ${currentPage + 1})">Next</button>`;
    }
    paginationHtml += "</div>";
  }
  const searchInfoHtml = `<div style="margin-top: 15px; padding: 10px; background-color: #f0f0f0; border-radius: 4px; font-size: 0.9em;"><p style="margin-bottom: 5px;"><strong>How this search works:</strong></p><ul style="margin-top: 0; padding-left: 20px;"><li>Searches prayer <strong>titles</strong> in database.</li><li>Searches <strong>full text</strong> of cached prayers.</li><li>View prayers via <a href="#languages">language lists</a> to improve cache for full-text search.</li></ul></div>`;
  const tiddlySuggestionHtml = `<p style="margin-top: 20px; text-align: center; font-size: 0.9em;">For more comprehensive text search, try <a href="https://tiddly.holywritings.net/workspace" target="_blank">tiddly.holywritings.net/workspace</a>.</p>`;
  
  const internalHeaderHtml = `<header><h2><span id="category">Search Results</span><span id="blocktitle">For "${searchTerm ? searchTerm.replace(/"/g, '&quot;') : ''}" (Page ${currentPage})</span></h2></header>`;
  return `<div id="search-results-content-area">${internalHeaderHtml}${listHtml}${paginationHtml}${searchInfoHtml}${tiddlySuggestionHtml}</div>`;
}

async function renderSearchResults(searchTerm, page = 1) {
  currentPageBySearchTerm[searchTerm] = page;

  // Clear header/drawer navigation; search results don't have specific language nav
  updateHeaderNavigation([]);

  // Update search input fields
  const headerSearchInput = document.getElementById("header-search-field");
  if (headerSearchInput && headerSearchInput.value !== searchTerm) {
    headerSearchInput.value = searchTerm;
  }
  const drawerSearchInput = document.getElementById("drawer-search-field");
  if (drawerSearchInput && drawerSearchInput.value !== searchTerm) {
    drawerSearchInput.value = searchTerm;
  }

  await renderPageLayout({
    titleKey: `Search Results for "${searchTerm ? searchTerm.replace(/"/g, '&quot;') : ''}"`,
    contentRenderer: async () => _renderSearchResultsContent(searchTerm, page),
    showLanguageSwitcher: true, // Original function included the language picker
    showBackButton: true,       // Useful to go back from search results
    activeLangCodeForPicker: null,
    // isPrayerPage: false (default)
  });
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
          if (typeof componentHandler !== 'undefined' && componentHandler) {
              console.log('[renderPageLayout] Upgrading DOM for MDL components after new content rendering.');
              componentHandler.upgradeDom();
          }
          updatePrayerMatchingToolDisplay();
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
          })
        } // Correctly closing the try block now
        catch (err) {
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
          })
        } // Correctly closing the try block now
        catch (err) {
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
      } // Closing brace for the 'else' statement
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

  if (typeof componentHandler !== 'undefined' && componentHandler) {
    componentHandler.upgradeDom();
  }
  updatePrayerMatchingToolDisplay();
  handleRouteChange();
  
  // Start background caching of English prayers
  startBackgroundCaching();
});

// Helper function to update static prayer action button states
function updateStaticPrayerActionButtonStates(prayer) {
  const staticPinBtn = document.getElementById('static-action-pin-this');
  const staticUnpinBtn = document.getElementById('static-action-unpin-this');
  const staticAddMatchBtn = document.getElementById('static-action-add-match');
  const staticReplacePinBtn = document.getElementById('static-action-replace-pin');
  const staticIsPinnedMsg = document.getElementById('static-action-is-pinned-msg');
  const staticSuggestPhelpsBtn = document.getElementById('static-action-suggest-phelps');
  const staticChangeLangBtn = document.getElementById('static-action-change-lang');
  const staticChangeNameBtn = document.getElementById('static-action-change-name');
  const staticAddNoteBtn = document.getElementById('static-action-add-note');

  // Ensure all static buttons are made visible (overriding display:none from index.html)
  // and then set their initial disabled state for this render pass.
  const allStaticButtons = [staticPinBtn, staticUnpinBtn, staticAddMatchBtn, staticReplacePinBtn, staticSuggestPhelpsBtn, staticChangeLangBtn, staticChangeNameBtn, staticAddNoteBtn];
  allStaticButtons.forEach(btn => {
      if (btn) {
          btn.style.display = ''; // Make button visible
          btn.disabled = true;  // Default to disabled, specific logic will enable them
      }
  });
  
  // These buttons are always active (visible and enabled)
  if (staticChangeLangBtn) staticChangeLangBtn.disabled = false;
  if (staticChangeNameBtn) staticChangeNameBtn.disabled = false;
  if (staticAddNoteBtn) staticAddNoteBtn.disabled = false;

  // Hide "is pinned" message initially
  if (staticIsPinnedMsg) staticIsPinnedMsg.style.display = 'none';

  console.log(`[updateStaticPrayerActionButtonStates] Initial states: PinBtn disabled=${staticPinBtn?.disabled}; UnpinBtn disabled=${staticUnpinBtn?.disabled}; AddMatchBtn disabled=${staticAddMatchBtn?.disabled}`);

  // Logic to enable/disable buttons and show/hide message based on context
  console.log(`[updateStaticPrayerActionButtonStates] Context: prayer.version=${prayer?.version}, pinnedPrayerDetails.version=${pinnedPrayerDetails?.version}`);
  if (pinnedPrayerDetails) {
      if (pinnedPrayerDetails.version !== prayer?.version) { // A different prayer is pinned
          console.log("[updateStaticPrayerActionButtonStates] Branch: Different prayer is pinned.");
          if (staticAddMatchBtn) {
              const pinnedNameSnippet = (pinnedPrayerDetails.name || `Version ${pinnedPrayerDetails.version}`).substring(0, 20);
              const truncatedName = pinnedNameSnippet.length === 20 ? pinnedNameSnippet + "..." : pinnedNameSnippet;
              staticAddMatchBtn.innerHTML = `<i class="material-icons">playlist_add_check</i>Match with Pinned: ${truncatedName}`;
              staticAddMatchBtn.disabled = false;
          }
          if (staticReplacePinBtn) staticReplacePinBtn.disabled = false;
          // staticPinBtn remains disabled
          // staticUnpinBtn remains disabled
       } else { // The current prayer IS the pinned one
          console.log("[updateStaticPrayerActionButtonStates] Branch: Current prayer IS pinned.");
          if (staticIsPinnedMsg) staticIsPinnedMsg.style.display = 'block';
          if (staticUnpinBtn) staticUnpinBtn.disabled = false;
          // staticPinBtn, staticAddMatchBtn, staticReplacePinBtn remain disabled
       }
   } else { // No prayer is pinned
      console.log("[updateStaticPrayerActionButtonStates] Branch: No prayer pinned.");
      if (staticPinBtn) staticPinBtn.disabled = false;
      // staticUnpinBtn, staticAddMatchBtn, staticReplacePinBtn remain disabled
      if (staticAddMatchBtn) { // Reset text if no prayer is pinned
         staticAddMatchBtn.innerHTML = `<i class="material-icons">playlist_add_check</i> Match with Pinned`;
       }
       if (staticIsPinnedMsg) staticIsPinnedMsg.style.display = 'none';
   }

   // Enable/Disable "Suggest Phelps Code" button
  if (staticSuggestPhelpsBtn && prayer) {
      const context = window.currentPrayerForStaticActions;
      if (!prayer.phelps && !(context && context.phelpsIsSuggested)) {
          staticSuggestPhelpsBtn.disabled = false;
      } else {
          staticSuggestPhelpsBtn.disabled = true; // Already has phelps or is suggested
      }
  }

  // Update titles for always-enabled buttons (context should exist)
  const currentContext = window.currentPrayerForStaticActions;
  if (staticChangeLangBtn && currentContext) {
       staticChangeLangBtn.title = `Current effective language: ${currentContext.finalDisplayLanguageForPhelpsMeta}`;
  }
  if (staticChangeNameBtn && currentContext) {
       staticChangeNameBtn.title = `Current effective name: ${currentContext.nameToDisplay || "Not Set"}`;
  }
}

// Helper function to update button states without full page re-render
function updateStaticPrayerActionButtons() {
  const currentPrayer = window.currentPrayerForStaticActions?.prayer;
  if (currentPrayer) {
    updateStaticPrayerActionButtonStates(currentPrayer);
  }
}

window.addEventListener('hashchange', handleRouteChange);
initializeStaticPrayerActions();

// Clean up old cache entries periodically
setInterval(() => {
  const now = Date.now();
  for (const [key, value] of requestCache.entries()) {
    if (now - value.timestamp > REQUEST_CACHE_TTL) {
      requestCache.delete(key);
    }
  }
}, 60000); // Clean up every minute
