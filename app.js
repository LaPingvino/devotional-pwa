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
const LANGUAGE_STATS_CACHE_KEY_SIMPLE = "devotionalPWA_languageStatsSimple";
const LANGUAGE_STATS_CACHE_EXPIRY_MS = 4 * 60 * 60 * 1000; // 4 hours
// --- End Language Statistics Cache ---

// --- Recent Language Storage ---
const RECENT_LANGUAGES_KEY = "devotionalPWA_recentLanguages";
const MAX_RECENT_LANGUAGES = 4; // Number of recent languages to store

// --- Markdown Rendering Functions ---
function renderMarkdown(text) {
  if (!text) return "";

  // Check if marked library is available
  if (typeof marked === "undefined") {
    console.warn(
      "Marked library not available, rendering text as-is with basic formatting",
    );
    return text.replace(/\n/g, "<br>");
  }

  // Configure marked for prayer content
  marked.setOptions({
    breaks: true, // Convert line breaks to <br>
    gfm: true, // Enable GitHub Flavored Markdown
    sanitize: false, // Allow HTML (we trust our prayer content)
    smartypants: true, // Use smart quotes and dashes
  });

  try {
    return marked.parse(text);
  } catch (error) {
    console.error("Error parsing Markdown:", error);
    // Fallback to basic HTML formatting
    return text.replace(/\n/g, "<br>");
  }
}

function extractPrayerNameFromMarkdown(text) {
  if (!text) return null;

  // Look for headers (## Header Name or # Header Name)
  const headerMatch = text.match(/^#{1,2}\s*(.+?)$/m);
  if (headerMatch) {
    let name = headerMatch[1].trim();
    // Remove markdown formatting from the extracted name
    name = name.replace(/\*\*(.*?)\*\*/g, "$1"); // Remove bold
    name = name.replace(/\*(.*?)\*/g, "$1"); // Remove italic
    name = name.replace(/`(.*?)`/g, "$1"); // Remove code
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
  if (!langCode || typeof langCode !== "string") return;

  const code = langCode.toLowerCase(); // Store consistently
  let recentLanguages = getRecentLanguages();

  // Remove the language if it already exists to move it to the front
  recentLanguages = recentLanguages.filter((l) => l !== code);

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
  console.log(
    "[initializeStaticPrayerActions] Initializing listeners for prayer action buttons.",
  );

  const informMistakeBtn = document.getElementById("inform-mistake-button");
  if (informMistakeBtn) {
    informMistakeBtn.addEventListener("click", () => {
      if (
        window.currentPrayerForStaticActions &&
        window.currentPrayerForStaticActions.prayer
      ) {
        const prayer = window.currentPrayerForStaticActions.prayer;
        const prayerName = prayer.name || `Prayer ${prayer.version}`;
        const language =
          window.currentPrayerForStaticActions.languageToDisplay ||
          prayer.language;

        const subject = encodeURIComponent(`Mistake Report: ${prayerName}`);
        const body = encodeURIComponent(
          `I would like to report a mistake in the following prayer:\n\n` +
            `Prayer: ${prayerName}\n` +
            `Version ID: ${prayer.version}\n` +
            `Language: ${language}\n` +
            `Phelps Code: ${prayer.phelps || "Not assigned"}\n` +
            `URL: ${window.location.href}\n\n` +
            `Description of the mistake:\n\n\n\n` +
            `Thank you for maintaining this resource!`,
        );

        window.location.href = `mailto:ikojba@gmail.com?subject=${subject}&body=${body}`;
      } else {
        console.warn("Inform Mistake: No prayer context available.");
      }
    });
  } else {
    console.warn("Inform mistake button not found.");
  }
}

const contentDiv = document.getElementById("content");
const prayerLanguageNav = document.getElementById("prayer-language-nav");

let currentPageByLanguage = {};
let currentPageBySearchTerm = {}; // { searchTerm: page }
let currentPageByPhelpsCode = {}; // { phelpsCode: page }
// let currentPageByPhelpsLangCode = {}; // { phelps/lang : page } - Currently unused

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
      console.warn(
        "No rows returned from language names query or invalid format. Language map may be empty.",
      );
      languageNamesMap = {}; // Set global map to empty if no data
      return languageNamesMap;
    }
  })();

  const timeoutPromise = new Promise((resolve, reject) => {
    setTimeout(() => {
      if (!fetchCompleted) {
        console.warn(
          `Fetching language names timed out after ${FETCH_LANG_NAMES_TIMEOUT_MS / 1000}s.`,
        );
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
      .then((names) => {
        // _fetchLanguageNamesInternal already updated the global languageNamesMap on its success.
        // console.log("Language names fetched successfully via promise.");
        return names; // Return the map
      })
      .catch((error) => {
        console.error(
          "fetchLanguageNames: Failed to fetch language names.",
          error.message,
        );
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
    console.warn(
      `getLanguageDisplayName: Display name not found for language code '${langCode}'. Returning original code.`,
    );
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
      lastOffset,
    };
    localStorage.setItem(
      BACKGROUND_CACHE_STORAGE_KEY,
      JSON.stringify(statusData),
    );
  } catch (e) {
    console.warn("Error saving background cache status:", e);
  }
}

async function loadEnglishPrayersInBackground() {
  const status = getBackgroundCacheStatus();

  // Skip if we've cached recently and it's not expired
  if (!status.isExpired && status.totalCached > 0) {
    console.log(
      `[Background Cache] Skipping - ${status.totalCached} English prayers cached recently`,
    );
    return;
  }

  console.log(
    "[Background Cache] Starting English prayers background caching...",
  );

  try {
    // First, get a count of English prayers to cache
    const countSql =
      "SELECT COUNT(*) as total FROM writings WHERE language = 'en'";
    const countResult = await executeQuery(countSql);
    const totalEnglishPrayers = countResult[0]?.total || 0;

    if (totalEnglishPrayers === 0) {
      console.log("[Background Cache] No English prayers found in database");
      return;
    }

    console.log(
      `[Background Cache] Found ${totalEnglishPrayers} English prayers to cache`,
    );

    // Get already cached English prayers to avoid duplicates
    const cachedPrayers = getAllCachedPrayers();
    const cachedEnglishVersions = new Set(
      cachedPrayers.filter((p) => p.language === "en").map((p) => p.version),
    );

    let totalCached = 0;
    let currentOffset = status.lastOffset;
    let batchCount = 0;
    const maxBatches = Math.ceil(
      totalEnglishPrayers / BACKGROUND_CACHE_BATCH_SIZE,
    );

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

      console.log(
        `[Background Cache] Batch ${batchCount}/${maxBatches}: cached ${batchCached} new prayers (${totalCached} total)`,
      );

      // Update status periodically
      setBackgroundCacheStatus(totalCached, currentOffset);

      // Add delay between batches to avoid overwhelming the API
      if (currentOffset < totalEnglishPrayers) {
        await new Promise((resolve) =>
          setTimeout(resolve, BACKGROUND_CACHE_DELAY_MS),
        );
      }
    }

    console.log(
      `[Background Cache] Completed! Cached ${totalCached} English prayers for better search performance`,
    );

    // Show a subtle notification if we cached a significant number
    if (totalCached > 10) {
      const snackbarContainer = document.querySelector(".mdl-js-snackbar");
      if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
        snackbarContainer.MaterialSnackbar.showSnackbar({
          message: `✨ Cached ${totalCached} English prayers for faster search`,
          timeout: 4000,
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

function getCachedLanguageStats(
  allowStale = false,
  cacheKey = LANGUAGE_STATS_CACHE_KEY,
) {
  try {
    const cachedData = localStorage.getItem(cacheKey);
    if (cachedData) {
      const { timestamp, data } = JSON.parse(cachedData);
      if (
        allowStale ||
        Date.now() - timestamp < LANGUAGE_STATS_CACHE_EXPIRY_MS
      ) {
        // console.log(`Language stats loaded from cache ${allowStale && (Date.now() - timestamp >= LANGUAGE_STATS_CACHE_EXPIRY_MS) ? '(stale)' : '(fresh)'}.`);
        return data;
      } else {
        // console.log("Language stats cache expired.");
        localStorage.removeItem(cacheKey);
      }
    }
  } catch (e) {
    console.error("Error reading language stats from cache:", e);
    localStorage.removeItem(cacheKey); // Clear corrupted cache
  }
  return null;
}

function cacheLanguageStats(data, cacheKey = LANGUAGE_STATS_CACHE_KEY) {
  if (!data || !Array.isArray(data)) {
    console.error("Attempted to cache invalid language stats data:", data);
    return;
  }
  try {
    localStorage.setItem(
      cacheKey,
      JSON.stringify({ timestamp: Date.now(), data: data }),
    );
    // console.log("Language stats cached.");
  } catch (e) {
    console.error("Error caching language stats:", e);
  }
}

/**
 * Clears all language-related caches and refreshes the language picker
 */
function clearLanguageCaches() {
  try {
    // Clear language names cache
    localStorage.removeItem(LANGUAGE_NAMES_CACHE_KEY);
    console.log("Language names cache cleared.");

    // Clear language statistics cache
    localStorage.removeItem(LANGUAGE_STATS_CACHE_KEY);
    console.log("Language statistics cache cleared.");

    // Reset the global language names map
    languageNamesMap = {};

    // Refresh the language picker if it exists
    const languagePickerContainer = document.getElementById(
      "language-picker-container",
    );
    if (languagePickerContainer) {
      // Re-populate the language selection to show fresh data
      populateLanguageSelection();
      console.log("Language picker refreshed.");

      // Apply Safari-specific fixes after refresh
      setTimeout(() => {
        applySafariMdlMenuFixes();
      }, 150);
    }

    return true;
  } catch (e) {
    console.error("Error clearing language caches:", e);
    return false;
  }
}

async function executeQuery(sql) {
  const cacheKey = sql;
  const now = Date.now();

  // Check cache first
  if (requestCache.has(cacheKey)) {
    const cached = requestCache.get(cacheKey);
    if (now - cached.timestamp < REQUEST_CACHE_TTL) {
      console.log(
        "[executeQuery] Returning cached result for:",
        sql.substring(0, 100),
      );
      return cached.data;
    } else {
      requestCache.delete(cacheKey);
    }
  }

  // Debounce identical requests
  if (requestDebounce.has(cacheKey)) {
    console.log(
      "[executeQuery] Waiting for existing request:",
      sql.substring(0, 100),
    );
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
        throw new Error(
          `HTTP error! status: ${response.status}, body: ${responseText}`,
        );
      }

      let data;
      try {
        data = JSON.parse(responseText);
        console.log("[executeQuery] Parsed JSON data:", data);
      } catch (jsonError) {
        console.error(
          "[executeQuery] Failed to parse response text as JSON:",
          jsonError,
        );
        console.error(
          "[executeQuery] Response text that failed parsing was:",
          responseText,
        );
        throw new Error(
          `Failed to parse API response as JSON. HTTP status: ${response.status}`,
        );
      }

      const result = data.rows || [];

      // Cache the result
      requestCache.set(cacheKey, {
        data: result,
        timestamp: now,
      });

      return result;
    } catch (error) {
      console.error(
        "[executeQuery] Error during executeQuery fetch or processing:",
        error,
      );
      if (!(error.message && error.message.startsWith("HTTP error!"))) {
        console.error(
          "[executeQuery] SQL that failed (network or other error):",
          sql,
        );
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
async function createPrayerCardHtml(
  prayerData,
  allPhelpsDetails = {},
  languageDisplayMap = {},
) {
  const {
    version,
    name,
    language = "N/A",
    phelps,
    opening_text,
    link,
  } = prayerData;

  const displayLanguageForTitle =
    languageDisplayMap[language] || (await getLanguageDisplayName(language));
  let displayTitle =
    name ||
    (phelps
      ? `${phelps} - ${displayLanguageForTitle}`
      : `${version} - ${displayLanguageForTitle}`);
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

    if (typeof componentHandler !== "undefined" && componentHandler) {
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
    isPrayerPage = false, // New flag to identify prayer pages
  } = viewSpec;
  console.log(
    "[renderPageLayout] Received viewSpec.titleKey:",
    titleKey,
    "isPrayerPage:",
    isPrayerPage,
  );
  console.log(
    "[renderPageLayout] Received viewSpec.activeLangCodeForPicker:",
    activeLangCodeForPicker,
  );

  // Hide static prayer actions host if not on a prayer page
  const staticActionsHostGlobal = document.getElementById(
    "static-prayer-actions-host",
  );
  if (staticActionsHostGlobal) {
    if (!isPrayerPage) {
      staticActionsHostGlobal.style.display = "none";
      console.log(
        "[renderPageLayout] Hiding static-prayer-actions-host as not on a prayer page.",
      );
    } else {
      // Show it on prayer pages - it will be properly configured by _renderPrayerContent
      staticActionsHostGlobal.style.display = "flex";
      console.log(
        "[renderPageLayout] Showing static-prayer-actions-host on prayer page.",
      );
    }
  }

  const headerTitleSpan = document.getElementById("page-main-title");
  const headerFavoriteButton = document.getElementById(
    "page-header-favorite-button",
  );
  // Define the specific area for dynamic content, leaving the header (#page-header-container) intact.
  const dynamicContentHostElement = document.getElementById(
    "dynamic-content-area",
  );

  if (!dynamicContentHostElement) {
    console.error(
      "Critical: #dynamic-content-area not found in DOM. Page layout cannot proceed.",
    );
    // Fallback: Clear the main contentDiv if dynamic area is missing, and show error.
    // This might still wipe the title if it's inside contentDiv, but it's a fallback.
    if (contentDiv) {
      contentDiv.innerHTML =
        '<p style="color:red; text-align:center; padding:20px;">Error: Page structure is broken (#dynamic-content-area missing).</p>';
    }
    return;
  }

  // 1. Reset Header elements
  if (headerFavoriteButton) {
    headerFavoriteButton.style.display = "none"; // Individual views can re-enable and configure it.
  }

  // Clean up elements potentially added by previous renderPageLayout calls
  const headerRow = headerTitleSpan
    ? headerTitleSpan.closest(".mdl-layout__header-row")
    : null;
  if (headerRow) {
    const existingBackButton = headerRow.querySelector(
      ".page-layout-back-button",
    );
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
      if (typeof customContent === "string") {
        headerTitleSpan.innerHTML = customContent;
      } else if (customContent instanceof Node) {
        headerTitleSpan.innerHTML = ""; // Clear previous
        headerTitleSpan.appendChild(customContent);
      } else {
        // Fallback if custom renderer returns nothing or unexpected type
        // Attempt to use titleKey as a safeguard
        const titleText = titleKey || "Untitled";
        headerTitleSpan.textContent = titleText;
      }
    }
  } else {
    // Default header: Title + optional Back Button
    if (headerTitleSpan) {
      const titleText = titleKey || "Untitled";
      console.log(
        "[renderPageLayout] Setting headerTitleSpan.textContent to:",
        titleText,
      );
      headerTitleSpan.textContent = titleText;
    }

    if (showBackButton) {
      if (headerRow) {
        const backButton = document.createElement("button");
        backButton.className =
          "mdl-button mdl-js-button mdl-button--icon page-layout-back-button";
        backButton.innerHTML = '<i class="material-icons">arrow_back</i>';
        const backButtonTitle = "Back";
        backButton.title = backButtonTitle;
        backButton.onclick = () => window.history.back();

        // Insert before the title, or after drawer button if present.
        // The drawer button is usually the first interactive element.
        const drawerButton = headerRow.querySelector(
          ".mdl-layout__drawer-button",
        );
        if (drawerButton) {
          // Hide the drawer button when a back button is shown.
          // This is a design choice; adjust if both should be visible.
          drawerButton.style.display = "none";
          headerRow.insertBefore(backButton, drawerButton); // effectively replacing it
        } else if (headerTitleSpan) {
          // If no drawer button, insert before title.
          headerTitleSpan.parentNode.insertBefore(backButton, headerTitleSpan);
        } else {
          // Fallback: insert as first child of header row
          headerRow.insertBefore(backButton, headerRow.firstChild);
        }

        if (typeof componentHandler !== "undefined" && componentHandler) {
          componentHandler.upgradeElement(backButton);
        }
      }
    } else {
      // If not showing back button, ensure drawer button (if exists and was hidden by us) is visible
      if (headerRow) {
        const drawerButton = headerRow.querySelector(
          ".mdl-layout__drawer-button",
        );
        if (drawerButton && drawerButton.style.display === "none") {
          drawerButton.style.display = ""; // Make it visible again
        }
      }
    }
  }

  // 2. Clear the specific dynamic content area
  dynamicContentHostElement.innerHTML = "";

  // 3. Prepare container for view-specific content within the dynamic area
  const viewContentContainer = document.createElement("div");
  viewContentContainer.id = "view-specific-content-container";
  dynamicContentHostElement.appendChild(viewContentContainer);

  // 5. Render view-specific content with a loading spinner (Bahá'í star)
  const tempSpinner = document.createElement("div");
  tempSpinner.className = "bahai-loading-spinner";
  tempSpinner.innerHTML = "&#x1f7d9;"; // Bahá'í star
  viewContentContainer.appendChild(tempSpinner);

  try {
    const renderedViewContent = await contentRenderer(); // Call the callback

    // Clear spinner before adding new content
    // (viewContentContainer still exists, tempSpinner was its child)
    tempSpinner.remove();

    if (typeof renderedViewContent === "string") {
      viewContentContainer.innerHTML = renderedViewContent;
    } else if (renderedViewContent instanceof Node) {
      // Catches HTMLElement, DocumentFragment
      // If contentRenderer returns a Node, ensure viewContentContainer is empty before appending
      viewContentContainer.innerHTML = "";
      viewContentContainer.appendChild(renderedViewContent);
    }
    // If renderedViewContent is null/undefined (void return), viewContentContainer remains empty.
    // This assumes contentRenderer does not directly manipulate viewContentContainer if it returns void.
    // It's safer if contentRenderer always returns content to be placed.

    // 6. Upgrade MDL components in the newly added view-specific content
    if (typeof componentHandler !== "undefined" && componentHandler) {
      console.log(
        "[renderPageLayout] Upgrading DOM for view-specific content in #view-specific-content-container.",
      );
      componentHandler.upgradeDom(viewContentContainer);
    }

    // 7. Render Language Switcher AFTER content (at the bottom) if requested
    if (showLanguageSwitcher) {
      const bottomSelector = await _renderBottomLanguageSelector();
      viewContentContainer.appendChild(bottomSelector);

      // Upgrade MDL components in the language selector
      if (typeof componentHandler !== "undefined" && componentHandler) {
        componentHandler.upgradeDom(bottomSelector);
      }
    }
  } catch (error) {
    console.error(
      "Error during contentRenderer execution or processing:",
      error,
    );
    if (tempSpinner.parentNode) {
      // Check if spinner is still in DOM
      tempSpinner.remove();
    }
    viewContentContainer.innerHTML = `<p style="color:red; text-align:center; padding: 20px;">Error loading view content: ${error.message}</p>`;
    // Optionally, upgrade this error message if it contains MDL classes (though unlikely here)
    if (typeof componentHandler !== "undefined" && componentHandler) {
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
async function _calculatePrayerPageTitle(prayer) {
  console.log(
    "[_calculatePrayerPageTitle] Input prayer:",
    JSON.stringify(prayer),
  );
  if (!prayer) {
    console.log(
      "[_calculatePrayerPageTitle] Prayer object is null/undefined, returning default title info.",
    );
    return {
      titleText: "Prayer not found",
      nameIsSuggested: false,
      phelpsIsSuggested: false,
      languageIsSuggested: false,
      phelpsToDisplay: null,
      languageToDisplay: null,
      nameToDisplay: null,
    };
  }

  const initialDisplayPrayerLanguage = await getLanguageDisplayName(
    prayer.language,
  );
  let prayerTitleText =
    prayer.name ||
    (prayer.phelps
      ? `${prayer.phelps} - ${initialDisplayPrayerLanguage}`
      : null) ||
    (prayer.text
      ? prayer.text.substring(0, 50) + "..."
      : `Prayer ${prayer.version}`);

  let phelpsToDisplay = prayer.phelps;
  let phelpsIsSuggested = false;
  let languageToDisplay = prayer.language;
  let languageIsSuggested = false;
  let nameToDisplay = prayer.name;
  let nameIsSuggested = false;
  const displayLangForTitleAfterSuggestions =
    await getLanguageDisplayName(languageToDisplay);
  prayerTitleText =
    nameToDisplay ||
    (phelpsToDisplay
      ? `${phelpsToDisplay} - ${displayLangForTitleAfterSuggestions}`
      : `Prayer ${prayer.version}`);

  console.log(
    "[_calculatePrayerPageTitle] Calculated prayerTitleText:",
    prayerTitleText,
  );
  return {
    titleText: prayerTitleText,
    nameIsSuggested,
    phelpsIsSuggested,
    languageIsSuggested,
    phelpsToDisplay,
    languageToDisplay,
    nameToDisplay,
  };
}

async function _renderPrayerContent(
  prayerObject,
  phelpsCodeForNav,
  activeLangForNav,
  titleCalculationResults,
) {
  const staticPageHeaderFavoriteButton = document.getElementById(
    "page-header-favorite-button",
  );
  // const staticPageMainTitle = document.getElementById('page-main-title'); // Title is set by renderPageLayout

  if (!prayerObject) {
    // This case should ideally be handled before calling _renderPrayerContent,
    // e.g., in renderPrayer or resolveAndRenderPrayerByPhelpsAndLang.
    return `<p>Error: Prayer data not provided to content renderer.</p>`;
  }
  const prayer = prayerObject; // Use a consistent variable name internally
  const authorName = getAuthorFromPhelps(prayer.phelps);

  // These are needed for button prompts and context for static actions
  const initialDisplayPrayerLanguage = await getLanguageDisplayName(
    prayer.language,
  );
  const phelpsToDisplay = titleCalculationResults.phelpsToDisplay; // Effective Phelps code for display
  const languageToDisplay = titleCalculationResults.languageToDisplay; // Effective language for display
  const nameToDisplay = titleCalculationResults.nameToDisplay; // Effective name for display
  const effectiveLanguageDisplayName =
    await getLanguageDisplayName(languageToDisplay); // Display name of effective language

  // Set context for static button event listeners
  // Ensure all potentially needed values from titleCalculationResults are included
  window.currentPrayerForStaticActions = {
    prayer: prayer,
    initialDisplayPrayerLanguage: initialDisplayPrayerLanguage,
    nameToDisplay: nameToDisplay,
    languageToDisplay: languageToDisplay,
    finalDisplayLanguageForPhelpsMeta: effectiveLanguageDisplayName, // Use the renamed variable here
    phelpsIsSuggested: titleCalculationResults.phelpsIsSuggested,
  };

  // Update static page header elements - only favorite button here
  if (staticPageHeaderFavoriteButton) {
    staticPageHeaderFavoriteButton.style.display = "inline-flex";
    staticPageHeaderFavoriteButton.innerHTML = isPrayerFavorite(prayer.version)
      ? '<i class="material-icons">star</i> Favorited'
      : '<i class="material-icons">star_border</i> Favorite';
    staticPageHeaderFavoriteButton.onclick = () =>
      toggleFavoritePrayer(prayer, staticPageHeaderFavoriteButton);
    if (typeof componentHandler !== "undefined" && componentHandler) {
      componentHandler.upgradeElement(staticPageHeaderFavoriteButton);
    }
  }

  cachePrayerText({ ...prayer });

  const fragment = document.createDocumentFragment();

  // 1. Translations Switcher
  const translationsAreaDiv = document.createElement("div");
  translationsAreaDiv.id = "prayer-translations-switcher-area";
  translationsAreaDiv.className = "translations-switcher";

  // --- BEGIN TEMPORARY LOGS for TranslationSwitcher ---
  console.log(
    "[TranslationSwitcher] Input phelpsCodeForNav:",
    phelpsCodeForNav,
  );
  console.log("[TranslationSwitcher] Input phelpsToDisplay:", phelpsToDisplay);
  // --- END TEMPORARY LOGS ---

  const phelpsCodeForSwitcher = phelpsCodeForNav || phelpsToDisplay;

  // --- BEGIN TEMPORARY LOGS for TranslationSwitcher ---
  console.log(
    "[TranslationSwitcher] Determined phelpsCodeForSwitcher:",
    phelpsCodeForSwitcher,
  );
  console.log("[TranslationSwitcher] activeLangForNav:", activeLangForNav);
  console.log("[TranslationSwitcher] prayer.language:", prayer.language);
  // --- END TEMPORARY LOGS ---

  if (phelpsCodeForSwitcher && !phelpsCodeForSwitcher.startsWith("TODO")) {
    const escapedPhelpsCode = phelpsCodeForSwitcher.replace(/'/g, "''"); // Correctly escape single quotes in the Phelps code
    const transSql = `SELECT DISTINCT language FROM writings WHERE phelps = '${escapedPhelpsCode}' AND phelps IS NOT NULL AND phelps != '' ORDER BY language`; // Use standard SQL string literals

    // --- BEGIN TEMPORARY LOGS for TranslationSwitcher ---
    console.log("[TranslationSwitcher] SQL for distinct languages:", transSql);
    console.log("[TranslationSwitcher] Executing query for translations...");
    // --- END TEMPORARY LOGS ---

    try {
      const distinctLangs = await executeQuery(transSql);
      console.log(
        "[TranslationSwitcher] Query completed, result:",
        distinctLangs,
      );

      // --- BEGIN TEMPORARY LOGS for TranslationSwitcher ---
      console.log(
        "[TranslationSwitcher] Distinct languages found (raw):",
        JSON.stringify(distinctLangs),
      );
      console.log(
        "[TranslationSwitcher] Number of distinct languages:",
        distinctLangs ? distinctLangs.length : "null/undefined",
      );
      // --- END TEMPORARY LOGS ---

      if (distinctLangs && distinctLangs.length > 1) {
        console.log(
          "[TranslationSwitcher] Creating menu with " +
            distinctLangs.length +
            " languages",
        );
        let switcherHtml = `<button id="translations-menu-btn" class="mdl-button mdl-js-button mdl-button--icon" title="View translations in other languages" style="margin-right: 8px;">
          <i class="material-icons">language</i>
        </button>
        <div class="mdl-menu mdl-menu--bottom-left mdl-js-menu mdl-js-ripple-effect" for="translations-menu-btn" id="translations-menu">`;

        const translationLinkPromises = distinctLangs.map(async (langRow) => {
          const langDisplayName = await getLanguageDisplayName(
            langRow.language,
          );
          const isActive = activeLangForNav
            ? langRow.language === activeLangForNav
            : langRow.language === prayer.language;
          return `<button class="mdl-menu__item ${isActive ? "is-active" : ""}" onclick="window.location.hash='#prayercode/${phelpsCodeForSwitcher}/${langRow.language}'">${langDisplayName}</button>`;
        });
        const translationLinksHtml = await Promise.all(translationLinkPromises);
        switcherHtml += translationLinksHtml.join("");
        switcherHtml += `</div>`;
        translationsAreaDiv.innerHTML = switcherHtml;

        // Upgrade MDL components
        if (typeof componentHandler !== "undefined" && componentHandler) {
          console.log(
            "[TranslationSwitcher] Upgrading MDL components for translations menu",
          );
          componentHandler.upgradeDom(translationsAreaDiv);
          console.log(
            "[TranslationSwitcher] MDL upgrade completed for translations area",
          );

          // Force upgrade of button and menu specifically
          const menuBtn = translationsAreaDiv.querySelector(
            "#translations-menu-btn",
          );
          const menu = translationsAreaDiv.querySelector("#translations-menu");

          if (menuBtn && menuBtn.MaterialButton) {
            console.log(
              "[TranslationSwitcher] Button already has MaterialButton",
            );
          }
          if (menu && menu.MaterialMenu) {
            console.log("[TranslationSwitcher] Menu already has MaterialMenu");
          }

          // Add fallback click handler in case MDL doesn't work
          if (menuBtn) {
            menuBtn.addEventListener("click", function (e) {
              console.log("[TranslationSwitcher] Menu button clicked");
              if (menu) {
                console.log(
                  "[TranslationSwitcher] Menu element found, classes:",
                  menu.className,
                );
                console.log(
                  "[TranslationSwitcher] Menu element in DOM:",
                  document.body.contains(menu),
                );
                console.log(
                  "[TranslationSwitcher] Menu children count:",
                  menu.children.length,
                );

                // Force MDL menu to open if it has the MaterialMenu interface
                if (menu.MaterialMenu) {
                  console.log(
                    "[TranslationSwitcher] Forcing MDL menu.show() via MaterialMenu",
                  );
                  menu.MaterialMenu.show(e);
                } else {
                  // Fallback: manually set display
                  console.log(
                    "[TranslationSwitcher] No MaterialMenu found, showing menu manually",
                  );
                  menu.style.display = "block";
                  menu.style.visibility = "visible";
                }
              } else {
                console.warn("[TranslationSwitcher] Menu element not found");
              }
            });
          }
        } else {
          console.warn(
            "[TranslationSwitcher] componentHandler not available, cannot upgrade MDL",
          );
        }
      } else {
        // --- BEGIN TEMPORARY LOGS for TranslationSwitcher ---
        console.log(
          "[TranslationSwitcher] Condition 'distinctLangs && distinctLangs.length > 1' is false. Not enough distinct languages to show switcher.",
        );
        // --- END TEMPORARY LOGS ---
        translationsAreaDiv.innerHTML = "";
      }
    } catch (error) {
      console.error(
        "[TranslationSwitcher] Error fetching translations for switcher:",
        error,
      );
      translationsAreaDiv.innerHTML =
        '<span class="translations-switcher-label" style="color:red;">Error loading translations.</span>';
    }
  } else {
    // --- BEGIN TEMPORARY LOGS for TranslationSwitcher ---
    console.log(
      "[TranslationSwitcher] Condition 'phelpsCodeForSwitcher && !phelpsCodeForSwitcher.startsWith(\"TODO\")' is false. Phelps code is invalid or TODO, not showing switcher.",
    );
    // --- END TEMPORARY LOGS ---
    translationsAreaDiv.innerHTML = "";
  }
  fragment.appendChild(translationsAreaDiv);

  // 2. Prayer Details Container
  const prayerDetailsContainer = document.createElement("div");
  prayerDetailsContainer.id = "prayer-details-area";

  const finalDisplayLanguageForPhelpsMeta =
    await getLanguageDisplayName(languageToDisplay);
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
  const renderedPrayerText = renderMarkdown(
    prayer.text || "No text available.",
  );

  // Try to extract prayer name from Markdown if not already set
  let displayName = prayer.name;
  if (!displayName && prayer.text) {
    const extractedName = extractPrayerNameFromMarkdown(prayer.text);
    if (extractedName) {
      displayName = extractedName;
      console.log(
        `[_renderPrayerContent] Extracted prayer name from Markdown: "${extractedName}"`,
      );
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

  const staticHost = document.getElementById("static-prayer-actions-host");
  console.log(
    "[_renderPrayerContent] staticHost from getElementById:",
    staticHost,
  ); // DEBUG
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
    staticHost.style.display = "flex"; // Ensure the host container is visible

    console.log(
      "[_renderPrayerContent] staticHost processed. Display:",
      staticHost.style.display,
    ); // DEBUG

    // Update button states
    updateStaticPrayerActionButtonStates(prayer);
  }
  // --- End Static Prayer Actions ---

  fragment.appendChild(prayerDetailsContainer);

  // 3. "Other versions in this language" (if applicable)
  if (
    phelpsToDisplay &&
    phelpsCodeForNav === phelpsToDisplay &&
    prayer.language
  ) {
    // Ensure prayer.language exists
    const sameLangVersionsSql = `SELECT version, name, link FROM writings WHERE phelps = '${phelpsToDisplay.replace(/'/g, "''")}' AND language = '${prayer.language.replace(/'/g, "''")}' AND version != '${prayer.version}' ORDER BY name`;
    try {
      const sameLangVersions = await executeQuery(sameLangVersionsSql);
      if (sameLangVersions.length > 0) {
        const otherVersionsDiv = document.createElement("div");
        otherVersionsDiv.style.marginTop = "20px";
        otherVersionsDiv.style.paddingTop = "15px";
        otherVersionsDiv.style.borderTop = "1px solid #eee";
        const langForOtherVersionsTitle = await getLanguageDisplayName(
          prayer.language,
        );
        let otherListHtml = `<h5>Other versions in ${langForOtherVersionsTitle} for ${phelpsToDisplay}:</h5><ul class="other-versions-in-lang-list">`;
        sameLangVersions.forEach((altVersion) => {
          const displayName =
            altVersion.name || `Version ${altVersion.version}`;
          const domain = altVersion.link
            ? `(${getDomain(altVersion.link)})`
            : "";
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
  // Apply Safari fixes to translations menu if present
  const translationsMenuBtn = fragment.querySelector("#translations-menu-btn");
  if (translationsMenuBtn) {
    setTimeout(() => {
      // Apply Safari-specific fixes for the translations menu
      const isSafari = /^((?!chrome|android).)*safari/i.test(
        navigator.userAgent,
      );
      if (isSafari) {
        const translationsMenu = document.getElementById("translations-menu");
        if (translationsMenu) {
          // Ensure menu has proper styling
          translationsMenu.style.backgroundColor = "#fff";
          translationsMenu.style.border = "1px solid rgba(0,0,0,0.12)";
          translationsMenu.style.webkitTransform = "translateZ(0)";
          translationsMenu.style.transform = "translateZ(0)";
          console.log(
            "[TranslationSwitcher] Applied Safari fixes to translations menu",
          );
        }
      }
    }, 50);
  }

  return fragment; // Return the constructed DOM fragment
}

async function renderPrayer(
  versionId,
  phelpsCodeForNav = null,
  activeLangForNav = null,
) {
  console.log(
    `[renderPrayer] Called with versionId: ${versionId}, phelpsCodeForNav: ${phelpsCodeForNav}, activeLangForNav: ${activeLangForNav}`,
  );
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
      contentRenderer: () =>
        `<div id="prayer-view-error"><p>Error loading prayer data: ${e.message}</p><p>Version ID: ${versionId}</p><p>Query: ${prayerSql}</p></div>`,
      showLanguageSwitcher: true,
      showBackButton: true,
      activeLangCodeForPicker: activeLangForNav, // Use activeLangForNav if available for picker context
    });
    return;
  }

  if (!prayerRows || prayerRows.length === 0) {
    await renderPageLayout({
      titleKey: "Prayer Not Found",
      contentRenderer: () =>
        `<div id="prayer-not-found"><p>Prayer with ID ${versionId} not found.</p><p>Query: ${prayerSql}</p></div>`,
      showLanguageSwitcher: true,
      showBackButton: true,
      activeLangCodeForPicker: activeLangForNav, // Use activeLangForNav if available
    });
    return;
  }

  const prayerData = prayerRows[0];
  console.log("[renderPrayer] Fetched prayerData:", JSON.stringify(prayerData));

  // 2. Calculate title using the new helper function
  const titleInfo = await _calculatePrayerPageTitle(prayerData);
  console.log(
    "[renderPrayer] titleInfo from _calculatePrayerPageTitle:",
    JSON.stringify(titleInfo),
  );

  // Determine the active language for the language picker
  const activeLanguageForPicker = activeLangForNav || prayerData.language;

  // 3. Call renderPageLayout with the dynamically calculated title and content renderer
  const viewSpecForPrayer = {
    titleKey: titleInfo.titleText, // Use the fully calculated title from titleInfo
    contentRenderer: () =>
      _renderPrayerContent(
        prayerData,
        phelpsCodeForNav,
        activeLangForNav,
        titleInfo,
      ),
    showLanguageSwitcher: true,
    showBackButton: true,
    activeLangCodeForPicker: activeLanguageForPicker,
    isPrayerPage: true, // Indicate this is a prayer page
  };
  console.log(
    "[renderPrayer] ViewSpec being passed to renderPageLayout:",
    JSON.stringify(viewSpecForPrayer, (key, value) =>
      typeof value === "function" ? "Function" : value,
    ),
  );
  await renderPageLayout(viewSpecForPrayer);
}

async function _renderPrayersForLanguageContent(
  langCode,
  page,
  showUnmatched,
  languageDisplayName,
) {
  const offset = (page - 1) * ITEMS_PER_PAGE;

  // Default: hide prayers without Phelps codes
  // If showUnmatched is true, show all prayers
  let filterCondition = !showUnmatched
    ? " AND phelps IS NOT NULL AND phelps != ''"
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

  const filterSwitchId = `filter-show-unmatched-${langCode}`;
  const filterSwitchHtml = `<div class="filter-switch-container"><label class="mdl-switch mdl-js-switch mdl-js-ripple-effect" for="${filterSwitchId}"><input type="checkbox" id="${filterSwitchId}" class="mdl-switch__input" onchange="setLanguageView('${langCode}', 1, this.checked)" ${showUnmatched ? "checked" : ""}><span class="mdl-switch__label">Also show prayers without Phelps code</span></label></div>`;

  if (prayersMetadata.length === 0 && page === 1) {
    return `<div id="language-content-area">${filterSwitchHtml}<p>No prayers found for language: ${languageDisplayName}${!showUnmatched ? " (only showing prayers with Phelps codes)" : ""}.</p><p>Query for metadata:</p><pre>${metadataSql}</pre><p><a href="${DOLTHUB_REPO_QUERY_URL_BASE}${encodeURIComponent(metadataSql)}" target="_blank">Debug metadata query</a></p><p>Count query:</p><pre>${countSql}</pre><p><a href="${DOLTHUB_REPO_QUERY_URL_BASE}${encodeURIComponent(countSql)}" target="_blank">Debug count query</a></p></div>`;
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
    console.warn(
      `Attempted to render page ${page} for ${langCode} which is out of bounds (total: ${totalPages}). Navigating to page ${Math.max(1, totalPages)}.`,
    );
    setLanguageView(langCode, Math.max(1, totalPages), showUnmatched); // This will re-trigger the whole render.
    return '<div id="language-content-area"><p>Redirecting to a valid page...</p></div>'; // Placeholder until redirect happens.
  }

  // Separate cached and uncached prayers for efficient batch processing
  const cachedPrayers = [];
  const uncachedPrayers = [];

  prayersMetadata.forEach((pMeta) => {
    const cached = getCachedPrayerText(pMeta.version);
    if (cached) {
      // Use cached data
      if (cached.name) pMeta.name = cached.name;
      if (cached.phelps) pMeta.phelps = cached.phelps;
      if (cached.link) pMeta.link = cached.link;
      const opening_text_for_card = cached.text
        ? cached.text.substring(0, MAX_PREVIEW_LENGTH) +
          (cached.text.length > MAX_PREVIEW_LENGTH ? "..." : "")
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
      const versionIds = batch
        .map((p) => `'${p.version.replace(/'/g, "''")}'`)
        .join(",");
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
    allTextRows.forEach((row) => {
      textMap[row.version] = row.text;
    });

    uncachedPrayersWithText = uncachedPrayers.map((pMeta) => {
      const full_text_for_preview = textMap[pMeta.version] || null;
      if (full_text_for_preview) {
        // Cache the prayer text
        cachePrayerText({
          version: pMeta.version,
          text: full_text_for_preview,
          name: pMeta.name,
          language: pMeta.language,
          phelps: pMeta.phelps,
          link: pMeta.link,
        });
      }
      const opening_text_for_card = full_text_for_preview
        ? full_text_for_preview.substring(0, MAX_PREVIEW_LENGTH) +
          (full_text_for_preview.length > MAX_PREVIEW_LENGTH ? "..." : "")
        : "No text preview available.";
      return { ...pMeta, opening_text: opening_text_for_card };
    });
  }

  const prayersForDisplay = [...cachedPrayers, ...uncachedPrayersWithText];

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

  // Pre-fetch all language display names for better performance
  const uniqueLanguages = [
    ...new Set(prayersForDisplay.map((p) => p.language)),
  ];
  const languageDisplayMap = {};

  // Batch fetch language display names in parallel
  const languageDisplayPromises = uniqueLanguages.map(async (langCode) => {
    const displayName = await getLanguageDisplayName(langCode);
    return [langCode, displayName];
  });

  const languageDisplayResults = await Promise.all(languageDisplayPromises);
  languageDisplayResults.forEach(([langCode, displayName]) => {
    languageDisplayMap[langCode] = displayName;
  });

  const listCardPromises = prayersForDisplay.map((pData) =>
    createPrayerCardHtml(pData, allPhelpsDetailsForCards, languageDisplayMap),
  );
  const listCardsHtmlArray = await Promise.all(listCardPromises);
  const listHtml = `<div class="favorite-prayer-grid">${listCardsHtmlArray.join("")}</div>`;

  let paginationHtml = "";
  if (totalPages > 1) {
    paginationHtml = '<div class="pagination">';
    if (page > 1)
      paginationHtml += `<button class="mdl-button mdl-js-button mdl-button--raised" onclick="setLanguageView('${langCode}', ${page - 1}, ${showUnmatched})">Previous</button>`;
    paginationHtml += ` <span>Page ${page} of ${totalPages}</span> `;
    if (page < totalPages)
      paginationHtml += `<button class="mdl-button mdl-js-button mdl-button--raised" onclick="setLanguageView('${langCode}', ${page + 1}, ${showUnmatched})">Next</button>`;
    paginationHtml += "</div>";
  }

  const internalHeaderHtml = `<header><h2><span id="category">Prayers</span><span id="blocktitle">Language: ${languageDisplayName} (Page ${page})${showUnmatched ? " - Including Unmatched" : ""}</span></h2></header>`;
  return `<div id="language-content-area">${internalHeaderHtml}${filterSwitchHtml}${listHtml}${paginationHtml}</div>`;
}

async function renderPrayersForLanguage(
  langCode,
  page = 1,
  showUnmatched = false,
) {
  addRecentLanguage(langCode);
  currentPageByLanguage[langCode] = { page, showUnmatched };

  // Clear main page header nav, as this view's primary navigation is via the language picker
  // and its own content (pagination, filters).
  updateHeaderNavigation([]);

  const languageDisplayName = await getLanguageDisplayName(langCode);
  const pageTitleForLayout = `Prayers in ${languageDisplayName}`;

  await renderPageLayout({
    titleKey: pageTitleForLayout, // Using dynamic string
    contentRenderer: async () => {
      // The languageDisplayName is passed to the content renderer for its internal header.
      return _renderPrayersForLanguageContent(
        langCode,
        page,
        showUnmatched,
        languageDisplayName,
      );
    },
    showLanguageSwitcher: true,
    showBackButton: true, // A back button is useful when viewing a specific language list.
    activeLangCodeForPicker: langCode, // Ensures the correct tab is active in the picker.
  });
}

async function _renderPrayerCodeViewContent(phelpsCode, page) {
  const offset = (page - 1) * ITEMS_PER_PAGE;

  // Fetch and set header navigation links for different languages of this Phelps code.
  const phelpsLangsSql = `SELECT DISTINCT language FROM writings WHERE phelps = '${phelpsCode.replace(/'/g, "''")}' ORDER BY language`;
  let navLinks = [];
  try {
    const distinctLangs = await executeQuery(phelpsLangsSql);
    navLinks = await Promise.all(
      distinctLangs.map(async (langRow) => ({
        text: await getLanguageDisplayName(langRow.language),
        href: `#prayercode/${phelpsCode}/${langRow.language}`,
        isActive: false,
      })),
    );
  } catch (error) {
    console.error(
      "Error fetching languages for Phelps code navigation:",
      error,
    );
    // Continue without these nav links, or display an error in header? For now, just log.
  }
  updateHeaderNavigation(navLinks); // This is specific to this view.

  // Fetch metadata for the prayer list
  const metadataSql = `SELECT version, name, language, text, phelps, link FROM writings WHERE phelps = '${phelpsCode.replace(/'/g, "''")}' AND phelps IS NOT NULL AND phelps != '' ORDER BY language, name LIMIT ${ITEMS_PER_PAGE} OFFSET ${offset}`;
  let prayersMetadata;
  try {
    prayersMetadata = await executeQuery(metadataSql);
  } catch (error) {
    console.error(
      `Error loading prayer data for Phelps code ${phelpsCode}:`,
      error,
    );
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
    console.warn(
      `Attempted to render page ${page} for Phelps ${phelpsCode} which is out of bounds (total: ${totalPages}). Navigating to page ${Math.max(1, totalPages)}.`,
    );
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
        allPhelpsDetailsForCards[phelpsCode] = allVersionRows.map((r) => ({
          ...r,
        }));
      }
    } catch (error) {
      console.error(
        "Error fetching all versions for Phelps code details:",
        error,
      );
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
    showBackButton: true, // Useful for navigating back from this specific view
    activeLangCodeForPicker: null, // No specific language is active in the picker for this general phelps view
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
            <button id="refresh-language-cache-btn" class="mdl-button mdl-js-button mdl-button--icon mdl-js-ripple-effect"
                    title="Refresh language cache" style="margin-left: 4px;">
              <i class="material-icons">refresh</i>
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

  const displayTargetLanguage =
    await getLanguageDisplayName(targetLanguageCode);

  const sql = `SELECT version, name, text, language, phelps, source, link FROM writings WHERE phelps = '${phelpsCode.replace(/'/g, "''")}' AND language = '${targetLanguageCode.replace(/'/g, "''")}' ORDER BY name`;
  const prayerVersions = await executeQuery(sql);

  if (prayerVersions.length === 0) {
    // If no specific version found, still try to set up navigation by phelps code for other languages.
    const transSql = `SELECT DISTINCT language FROM writings WHERE phelps = '${phelpsCode.replace(/'/g, "''")}' ORDER BY language`;
    let navLinks = [];
    try {
      const distinctLangs = await executeQuery(transSql);
      navLinks = await Promise.all(
        distinctLangs.map(async (langRow) => ({
          text: await getLanguageDisplayName(langRow.language),
          href: `#prayercode/${phelpsCode}/${langRow.language}`,
          isActive: false, // No specific language is "active" if the target one wasn't found
        })),
      );
    } catch (e) {
      console.error(
        "Error fetching languages for Phelps code navigation during not-found scenario:",
        e,
      );
    }
    updateHeaderNavigation(navLinks); // Update header nav with available languages for this Phelps code

    await renderPageLayout({
      titleKey: `Not Found: ${phelpsCode} in ${displayTargetLanguage}`,
      contentRenderer: () =>
        `<div id="prayer-not-found"><p>No prayer version found for Phelps Code ${phelpsCode} in ${displayTargetLanguage}.</p><p>You can try other available languages for this Phelps code using the links in the header (if any) or by browsing.</p></div>`,
      showLanguageSwitcher: true,
      showBackButton: true,
      activeLangCodeForPicker: targetLanguageCode, // Still show the target language as active context
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
  const tabBarElement = document.getElementById("language-picker-tab-bar");
  const moreLanguagesWrapperElement = document.getElementById(
    "more-languages-wrapper",
  );
  const menuUlElement = document.getElementById("all-languages-menu-ul");
  const messagePlaceholderElement = document.getElementById(
    "all-languages-message-placeholder",
  );

  if (
    !tabBarElement ||
    !menuUlElement ||
    !moreLanguagesWrapperElement ||
    !messagePlaceholderElement
  ) {
    console.error(
      "Language picker shell elements not found in DOM. Cannot populate.",
    );
    return;
  }

  // Clear previous dynamic content (safer than replacing entire innerHTML of tab bar)
  // Find existing tab links (not the more-languages-wrapper) and remove them
  const existingTabLinks = tabBarElement.querySelectorAll("a.mdl-tabs__tab");
  existingTabLinks.forEach((link) => link.remove());

  // Clear previous menu items (children of ul, skipping search and divider)
  // The menu will be completely rebuilt if otherLanguagesWithStats.length > 0
  // For now, just clear the message placeholder. Actual menu clearing/rebuilding happens below.
  messagePlaceholderElement.innerHTML = ""; // Clear previous message

  try {
    await fetchLanguageNames(); // Ensure names are loaded for display
  } catch (error) {
    console.warn(
      "generateLanguageSelectionHtml: Failed to pre-load language names. Fallbacks may be used.",
      error.message,
    );
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
        cacheLanguageStats(allLangsWithStats, LANGUAGE_STATS_CACHE_KEY);
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
  if (
    (attemptedFetch && !fetchedFreshData) ||
    !allLangsWithStats ||
    allLangsWithStats.length === 0
  ) {
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
    if (messagePlaceholderElement)
      messagePlaceholderElement.innerHTML =
        '<p style="text-align:center;">No languages found to select.</p>';
    if (moreLanguagesWrapperElement)
      moreLanguagesWrapperElement.style.display = "none"; // Hide more languages section
    if (tabBarElement) tabBarElement.innerHTML = ""; // Clear tab bar as well
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
  recentAndFavoritesTabsBarHtml += `<a href="#language-tab-panel-favorites"
        class="mdl-tabs__tab ${favoritesTabIsActive ? "is-active" : ""}"
        onclick="event.preventDefault(); if (window.location.hash !== '#languages') window.location.hash = '#languages';">
        ⭐ Favorites
     </a>`;
  // When on a non-#languages page, clicking "Favorites" tab navigates to #languages (where its panel is shown)

  if (recentLanguageCodes.length > 0 && allLangsWithStats.length > 0) {
    // Batch fetch all language display names for recent languages
    const recentLanguagesData = recentLanguageCodes
      .map((langCode) =>
        allLangsWithStats.find((l) => l.language.toLowerCase() === langCode),
      )
      .filter(Boolean);

    const recentLanguageNames = await Promise.all(
      recentLanguagesData.map((langData) =>
        getLanguageDisplayName(langData.language),
      ),
    );

    recentLanguagesData.forEach((langData, index) => {
      const displayName = recentLanguageNames[index];
      const phelpsCount = parseInt(langData.phelps_covered_count, 10) || 0;
      const nonPhelpsCount =
        parseInt(langData.versions_without_phelps_count, 10) || 0;
      const totalConceptualPrayers = phelpsCount + nonPhelpsCount;
      recentLangDetails.push({
        code: langData.language,
        display: displayName,
        phelps: phelpsCount,
        total: totalConceptualPrayers,
      });
    });

    if (recentLangDetails.length > 0) {
      const recentTabsHtml = recentLangDetails
        .map((lang) => {
          // A recent language tab is active only if currentActiveLangCode is present and matches
          const isActive =
            currentActiveLangCode &&
            lang.code.toLowerCase() === currentActiveLangCode.toLowerCase();
          return `<a href="#language-tab-panel-all"
                   class="mdl-tabs__tab ${isActive ? "is-active" : ""}"
                   onclick="event.preventDefault(); setLanguageView('${lang.code}', 1, false);">
                   ${lang.display} <span style="font-size:0.8em; opacity:0.7;">(${lang.phelps}/${lang.total})</span>
                 </a>`;
        })
        .join("");
      recentAndFavoritesTabsBarHtml += recentTabsHtml;
    }
  }

  // Filter out recent languages from the main list for the "All Languages" menu
  // This logic remains the same: languages in recent tabs are not repeated in "More"
  const otherLanguagesWithStats = allLangsWithStats.filter(
    (lang) =>
      !recentLanguageCodes.find((rlc) => rlc === lang.language.toLowerCase()),
  );

  // let allLanguagesMenuHtml = ""; // This variable is not used to build the final string for the section anymore
  const searchInputId = "language-search-input"; // Used for filterLanguageMenu closure
  const allLanguagesMenuUlId = "all-languages-menu-ul"; // Used for filterLanguageMenu closure

  if (otherLanguagesWithStats.length > 0) {
    moreLanguagesWrapperElement.style.display = "inline-block"; // Show the "More Languages" section

    // Augment languages with their display names for sorting
    const languagesWithDisplayNamesPromises = otherLanguagesWithStats.map(
      async (langData) => {
        const displayName = await getLanguageDisplayName(langData.language);
        return { ...langData, displayName }; // Keep original stats, add displayName
      },
    );
    const augmentedLanguages = await Promise.all(
      languagesWithDisplayNamesPromises,
    );

    // Sort by display name
    augmentedLanguages.sort((a, b) =>
      a.displayName.localeCompare(b.displayName),
    );

    // Generate HTML list items from the sorted list
    const menuItemsHtml = augmentedLanguages
      .map((langData) => {
        const langCode = langData.language;
        const displayName = langData.displayName; // Already fetched
        const phelpsCount = parseInt(langData.phelps_covered_count, 10) || 0;
        const nonPhelpsCount =
          parseInt(langData.versions_without_phelps_count, 10) || 0;
        const totalConceptualPrayers = phelpsCount + nonPhelpsCount;
        return `<li class="mdl-menu__item" onclick="setLanguageView('${langCode}', 1, false)" data-val="${langCode}">${displayName} (${phelpsCount}/${totalConceptualPrayers})</li>`;
      })
      .join("\n");

    if (menuUlElement) {
      const searchLiHtml = `
            <li id="language-search-li" onclick="event.stopPropagation();">
                <div class="mdl-textfield mdl-js-textfield">
                    <input class="mdl-textfield__input" type="text" id="${searchInputId}" onkeyup="filterLanguageMenu()" onclick="event.stopPropagation();">
                    <label class="mdl-textfield__label" for="${searchInputId}">Search languages...</label>
                </div>
            </li>`;
      const dividerHtml =
        '<li class="mdl-menu__divider" style="margin-top:0;"></li>';

      menuUlElement.innerHTML = searchLiHtml + dividerHtml + menuItemsHtml; // Rebuild entire menu content
    }

    // Define filterLanguageMenu within the scope or ensure it's globally available
    // Attaching to window is a way to make it available for inline onkeyup
    // Ensure it's only defined once per app lifecycle or managed appropriately if this function is called multiple times.
    if (typeof window.filterLanguageMenu !== "function") {
      // Define only if not already defined
      window.filterLanguageMenu = function () {
        const input = document.getElementById(searchInputId);
        if (!input) return;
        const filter = input.value.toLowerCase();
        const ul = document.getElementById(allLanguagesMenuUlId);
        if (!ul) return;
        const items = ul.getElementsByTagName("li");
        for (let i = 0; i < items.length; i++) {
          if (
            items[i].querySelector(`#${searchInputId}`) ||
            items[i].classList.contains("mdl-menu__divider")
          ) {
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
  else if (
    allLangsWithStats.length > 0 &&
    recentLangDetails.length === allLangsWithStats.length &&
    recentLangDetails.length > 0
  ) {
    if (messagePlaceholderElement)
      messagePlaceholderElement.innerHTML = `<p style="font-size:0.9em; color:#555;">All available languages are shown in "Recent".</p>`;
    if (menuUlElement) menuUlElement.innerHTML = ""; // Clear the menu UL if no "other" languages but this message is shown
    const moreButton = document.getElementById("all-languages-menu-btn");
    if (moreButton) moreButton.style.display = "none"; // Hide the button
    if (moreLanguagesWrapperElement)
      moreLanguagesWrapperElement.style.display = "inline-block"; // Show wrapper for the message
  } else {
    // No "other" languages and not all are recent (or no languages at all)
    // Hide the "More Languages" section entirely if there are no other languages
    if (menuUlElement) menuUlElement.innerHTML = ""; // Clear the menu UL
    if (moreLanguagesWrapperElement) {
      moreLanguagesWrapperElement.style.display = "none";
    }
  }

  // Populate tab bar:
  if (tabBarElement) {
    // Clear existing <a> tabs (direct children, not in a wrapper)
    Array.from(tabBarElement.children).forEach((child) => {
      if (child.tagName === "A" && child.classList.contains("mdl-tabs__tab")) {
        child.remove();
      }
    });

    if (recentAndFavoritesTabsBarHtml) {
      const tempDiv = document.createElement("div");
      tempDiv.innerHTML = recentAndFavoritesTabsBarHtml;
      const newTabLinks = Array.from(tempDiv.children);
      const currentMoreLanguagesWrapper = document.getElementById(
        "more-languages-wrapper",
      ); // Re-fetch, DOM might have changed

      if (
        currentMoreLanguagesWrapper &&
        currentMoreLanguagesWrapper.parentNode === tabBarElement
      ) {
        newTabLinks.forEach((link) => {
          tabBarElement.insertBefore(link, currentMoreLanguagesWrapper);
        });
      } else {
        console.warn(
          "More Languages wrapper not correctly found as a direct child of tab bar during tab injection. Appending tabs to end.",
        );
        newTabLinks.forEach((link) => {
          // Fallback append
          tabBarElement.appendChild(link);
        });
      }
    }
  }
  // No redundant if(tabBarElement) checks needed here.

  // Ensure MDL components in the picker are upgraded if they were dynamically added or modified.
  // This is crucial for tabs, menu, button ripples, textfield in search.
  if (typeof componentHandler !== "undefined" && componentHandler) {
    const pickerElement = tabBarElement.closest(".mdl-tabs");
    if (pickerElement) {
      componentHandler.upgradeElement(pickerElement); // Upgrade tabs
      const menuButton = pickerElement.querySelector("#all-languages-menu-btn");
      const menuUL = pickerElement.querySelector("#all-languages-menu-ul");
      const searchTf = pickerElement.querySelector(
        "#language-search-li .mdl-js-textfield",
      );

      if (menuButton) componentHandler.upgradeElement(menuButton);
      if (menuUL) componentHandler.upgradeElement(menuUL);
      if (searchTf) componentHandler.upgradeElement(searchTf);

      // Safari-specific fixes for MDL menu rendering issues
      const isSafari = /^((?!chrome|android).)*safari/i.test(
        navigator.userAgent,
      );
      if (isSafari && menuUL) {
        // Force Safari to properly initialize the menu
        setTimeout(() => {
          try {
            // Force re-upgrade for Safari
            componentHandler.upgradeDom(menuUL);

            // Ensure menu has proper background and visibility
            menuUL.style.backgroundColor = "#fff";
            menuUL.style.border = "1px solid rgba(0,0,0,0.12)";

            // Force GPU acceleration
            menuUL.style.webkitTransform = "translateZ(0)";
            menuUL.style.transform = "translateZ(0)";

            // Fix for Safari menu container positioning
            const menuContainer = menuUL.closest(".mdl-menu__container");
            if (menuContainer) {
              menuContainer.style.webkitTransform = "translateZ(0)";
              menuContainer.style.transform = "translateZ(0)";
            }

            console.log("[Safari Fix] Applied MDL menu fixes for Safari");
          } catch (e) {
            console.warn("[Safari Fix] Error applying Safari menu fixes:", e);
          }
        }, 100);
      }

      // MDL menus might need specific re-initialization if items are added dynamically
      // For simplicity, upgradeDom on the menu might be easiest if specific upgrade isn't clean
      // componentHandler.upgradeDom(menuUL); // Alternative
    } else {
      componentHandler.upgradeDom(); // Broader upgrade if specific element not found
    }
  }

  // Apply Safari-specific menu fixes if needed
  applySafariMdlMenuFixes();
} // Closes populateLanguageSelection

/**
 * Utility function to fix Safari MDL menu rendering issues
 * Call this after creating or modifying MDL menus
 */
function applySafariMdlMenuFixes() {
  const isSafari = /^((?!chrome|android).)*safari/i.test(navigator.userAgent);

  if (!isSafari) return;

  try {
    // Find all MDL menus in language picker
    const languageMenus = document.querySelectorAll(
      ".language-picker-tabs .mdl-menu",
    );

    languageMenus.forEach((menu) => {
      // Force proper background and visibility
      menu.style.backgroundColor = "#fff";
      menu.style.border = "1px solid rgba(0,0,0,0.12)";
      menu.style.boxShadow =
        "0 2px 2px 0 rgba(0,0,0,0.14), 0 3px 1px -2px rgba(0,0,0,0.2), 0 1px 5px 0 rgba(0,0,0,0.12)";

      // Force GPU acceleration for smooth rendering
      menu.style.webkitTransform = "translateZ(0)";
      menu.style.transform = "translateZ(0)";
      menu.style.webkitBackfaceVisibility = "hidden";
      menu.style.backfaceVisibility = "hidden";
      menu.style.willChange = "transform";

      // Fix menu container positioning
      const menuContainer = menu.closest(".mdl-menu__container");
      if (menuContainer) {
        menuContainer.style.position = "fixed";
        menuContainer.style.webkitTransform = "translateZ(0)";
        menuContainer.style.transform = "translateZ(0)";
        menuContainer.style.zIndex = "10005";
      }

      // Ensure menu items are properly styled
      const menuItems = menu.querySelectorAll(".mdl-menu__item");
      menuItems.forEach((item) => {
        item.style.backgroundColor = "transparent";
        item.style.color = "rgba(0,0,0,0.87)";
        item.style.webkitTransform = "translateZ(0)";
        item.style.transform = "translateZ(0)";
      });
    });

    console.log(
      `[Safari Fix] Applied MDL menu fixes to ${languageMenus.length} menus`,
    );
  } catch (e) {
    console.warn("[Safari Fix] Error in applySafariMdlMenuFixes:", e);
  }
}

/**
 * Safari-specific manual trigger for menu positioning fixes
 * Call this when menus appear blank or mispositioned
 */
function fixSafariMenuPositioning() {
  const isSafari = /^((?!chrome|android).)*safari/i.test(navigator.userAgent);

  if (!isSafari) return;

  try {
    const languageMenus = document.querySelectorAll(
      ".language-picker-tabs .mdl-menu",
    );

    languageMenus.forEach((menu) => {
      // Force reflow by temporarily changing display
      const originalDisplay = menu.style.display;
      menu.style.display = "none";
      // Force reflow
      menu.offsetHeight;
      menu.style.display = originalDisplay || "";

      // Re-apply positioning
      const rect = menu.getBoundingClientRect();
      if (rect.width === 0 || rect.height === 0) {
        // Menu is not visible, force visibility
        menu.style.visibility = "visible";
        menu.style.opacity = "1";
        menu.style.transform = "translateZ(0) scale(1)";
        menu.style.webkitTransform = "translateZ(0) scale(1)";
      }
    });

    console.log("[Safari Fix] Manual menu positioning fix applied");
  } catch (e) {
    console.warn("[Safari Fix] Error in fixSafariMenuPositioning:", e);
  }
}

/**
 * Global debug function for manual Safari menu fixes
 * Call this from browser console: window.fixSafariMenus()
 */
window.fixSafariMenus = function () {
  console.log("[Debug] Manual Safari menu fix triggered");

  try {
    // Apply all Safari fixes
    applySafariMdlMenuFixes();
    fixSafariMenuPositioning();

    // Force refresh of all MDL components
    if (typeof componentHandler !== "undefined" && componentHandler) {
      componentHandler.upgradeDom();
    }

    // Additional manual fixes
    const languageMenus = document.querySelectorAll(
      ".language-picker-tabs .mdl-menu",
    );
    languageMenus.forEach((menu, index) => {
      console.log(
        `[Debug] Fixing menu ${index + 1}: visible=${menu.offsetWidth > 0}, background=${menu.style.backgroundColor}`,
      );

      // Force visibility
      menu.style.display = "block";
      menu.style.visibility = "visible";
      menu.style.opacity = "1";
      menu.style.backgroundColor = "#fff";

      // Check if menu items are visible
      const items = menu.querySelectorAll(".mdl-menu__item");
      console.log(`[Debug] Menu ${index + 1} has ${items.length} items`);
    });

    console.log("[Debug] Safari menu fixes completed successfully");
    return "Safari menu fixes applied. Check console for details.";
  } catch (error) {
    console.error("[Debug] Error applying Safari menu fixes:", error);
    return "Error applying fixes. Check console for details.";
  }
};

// This function now manipulates DOM directly, doesn't return HTML for the whole picker.

// Helper function to render the specific content for the language list view.

async function _fetchAndDisplayRandomPrayer(containerElement) {
  // Changed parameter name
  if (!containerElement) {
    // Check the element directly
    console.error("Random prayer container element not provided or invalid.");
    return;
  }
  containerElement.innerHTML =
    '<div class="bahai-loading-spinner">&#x1f7d9;</div>';

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
    const totalPrayers =
      countResult &&
      countResult.length > 0 &&
      typeof countResult[0].total !== "undefined"
        ? parseInt(countResult[0].total, 10)
        : 0;

    if (totalPrayers === 0) {
      // This message now means no prayers in the entire table, which is unlikely but a good guard.
      containerElement.innerHTML =
        '<p style="text-align:center; padding:10px;">No prayers found in the database.</p>';
      return;
    }

    const randomOffset = Math.floor(Math.random() * totalPrayers);
    // Order by version (assuming it's indexed) for efficient OFFSET
    // No WHERE clause on 'text' here for performance; text presence will be checked after fetching.
    const prayerMetadataSql = `SELECT version, name, language, phelps FROM writings ORDER BY version LIMIT 1 OFFSET ${randomOffset}`;
    const prayerMetadataRows = await executeQuery(prayerMetadataSql);

    if (!prayerMetadataRows || prayerMetadataRows.length === 0) {
      containerElement.innerHTML =
        '<p style="color:red; text-align:center; padding:10px;">Could not load random prayer metadata.</p>';
      return;
    }

    const prayerMetadata = prayerMetadataRows[0];

    // Third step: Fetch the prayer text separately using its version ID
    const prayerTextSql = `SELECT text FROM writings WHERE version = '${prayerMetadata.version}'`;
    const prayerTextRows = await executeQuery(prayerTextSql);

    // Check if text was successfully fetched and is not empty
    if (
      !prayerTextRows ||
      prayerTextRows.length === 0 ||
      typeof prayerTextRows[0].text === "undefined" ||
      prayerTextRows[0].text.trim() === ""
    ) {
      // If the randomly selected prayer has no text or empty text, inform the user.
      // Avoid retrying here to prevent potential loops if many prayers lack text or if text fetching is problematic.
      containerElement.innerHTML =
        '<p style="text-align:center; padding:10px;">Prayer of the Moment: Selected prayer has no displayable text.</p>';
      return;
    }

    // Combine metadata with text
    const prayer = { ...prayerMetadata, text: prayerTextRows[0].text };
    const langDisplayName = await getLanguageDisplayName(prayer.language);

    let prayerHtml = `
      <div class="random-prayer-card" style="padding: 15px; margin-bottom: 20px; border-radius: 4px;">
        <h4 style="margin-top:0; margin-bottom:10px; font-size: 1.2em;">Prayer of the Moment</h4>
        <p style="font-size: 0.9em; margin-bottom:10px; opacity: 0.85;"><em>${prayer.name || (prayer.phelps ? `${prayer.phelps} - ${langDisplayName}` : `A prayer in ${langDisplayName}`)}</em></p>
        <div class="scripture markdown-content" style="max-height: 150px; overflow-y: auto; padding: 10px; margin-bottom:15px; font-size: 0.95em; line-height:1.5;">
          ${renderMarkdown(prayer.text.substring(0, 400) + (prayer.text.length > 400 ? "..." : ""))}
        </div>
        <a href="#prayer/${prayer.version}" class="mdl-button mdl-js-button mdl-button--raised mdl-button--accent">
          <i class="material-icons" style="margin-right:4px;">open_in_new</i> Read Full Prayer
        </a>
      </div>
    `;
    containerElement.innerHTML = prayerHtml;
    if (typeof componentHandler !== "undefined" && componentHandler) {
      componentHandler.upgradeDom(containerElement);
    }
  } catch (error) {
    console.error("Error fetching or displaying random prayer:", error);
    containerElement.innerHTML =
      '<p style="color:red; text-align:center; padding:10px;">Could not load Prayer of the Moment.</p>';
  }
}

async function _renderLanguageListContent() {
  const overallWrapper = document.createElement("div");

  // 1. Random Prayer Section (Prayer of the Moment)
  const randomPrayerPlaceholder = document.createElement("div");
  randomPrayerPlaceholder.id = "random-prayer-module";
  randomPrayerPlaceholder.style.marginBottom = "30px";
  randomPrayerPlaceholder.innerHTML =
    '<div class="bahai-loading-spinner">&#x1f7d9;</div>';
  overallWrapper.appendChild(randomPrayerPlaceholder);

  // 2. Favorites Section
  const langListSpecificContent = document.createElement("div");
  langListSpecificContent.innerHTML = `
      <div id="main-content-area-for-langlist" style="min-height: 100px;">
        <div class="mdl-tabs__panel is-active" id="language-tab-panel-favorites">
          <!-- Favorites content will be loaded here -->
        </div>
      </div>
    `;
  const favoritesPanel = langListSpecificContent.querySelector(
    "#language-tab-panel-favorites",
  );
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
            name: nameForCard || "",
            language: langForCard || "N/A",
            phelps: phelpsForCard || "",
            opening_text: textForCardPreview || "",
            link: linkForCard || "",
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

  // 3. Add Simple Language Selector at bottom
  const languageSelectorSection = document.createElement("div");
  languageSelectorSection.className = "simple-language-selector";
  languageSelectorSection.innerHTML = `
    <div class="simple-language-selector-header">
      <span class="bahai-star">&#x1f7d9;</span>
      <h3>Browse Prayers by Language</h3>
    </div>
    <div class="simple-language-buttons">
      <div class="bahai-loading-spinner" style="font-size: 2em; margin: 20px auto;">&#x1f7d9;</div>
    </div>
  `;
  overallWrapper.appendChild(languageSelectorSection);

  // Load language buttons asynchronously (fire and forget, but with error handling)
  const buttonsContainerHome = languageSelectorSection.querySelector(
    ".simple-language-buttons",
  );
  _loadSimpleLanguageButtons(buttonsContainerHome).catch((error) => {
    console.error("Error loading simple language buttons on home page:", error);
    if (buttonsContainerHome) {
      buttonsContainerHome.innerHTML =
        '<p style="text-align: center; color: #999;">Error loading languages</p>';
    }
  });

  // Kick off the async fetch for the random prayer.
  // It will update its placeholder div when ready.
  // No await here, let it load in background.
  _fetchAndDisplayRandomPrayer(randomPrayerPlaceholder);

  return overallWrapper;
}

async function _loadSimpleLanguageButtons(containerElement = null) {
  // If no container element provided, try to find it by ID (for backward compatibility)
  let container = containerElement;
  if (!container) {
    container = document.getElementById("language-buttons-container");
  }
  if (!container) return;

  try {
    // Get language statistics - try cached first, then fetch fresh
    let langStats = getCachedLanguageStats(
      false,
      LANGUAGE_STATS_CACHE_KEY_SIMPLE,
    );

    if (!langStats) {
      // Fetch fresh language stats
      const sql = `SELECT language, COUNT(DISTINCT phelps) as uniquePhelps, COUNT(*) as totalPrayers FROM writings WHERE phelps IS NOT NULL AND phelps != '' GROUP BY language ORDER BY totalPrayers DESC`;
      langStats = await executeQuery(sql);
      if (langStats && langStats.length > 0) {
        cacheLanguageStats(langStats, LANGUAGE_STATS_CACHE_KEY_SIMPLE);
      }
    }

    if (!langStats || langStats.length === 0) {
      container.innerHTML =
        '<p style="text-align: center; color: #999;">No languages available</p>';
      return;
    }

    // Detect browser language
    const browserLangCode = (
      navigator.language ||
      navigator.userLanguage ||
      "en"
    )
      .split("-")[0]
      .toLowerCase();

    // Sort: browser language first, then by prayer count
    langStats.sort((a, b) => {
      if (a.language.toLowerCase() === browserLangCode) return -1;
      if (b.language.toLowerCase() === browserLangCode) return 1;
      return b.totalPrayers - a.totalPrayers;
    });

    let buttonsHtml = "";
    for (const lang of langStats) {
      const displayName = await getLanguageDisplayName(lang.language);
      const isBrowserLang = lang.language.toLowerCase() === browserLangCode;
      const suggestionBadge = isBrowserLang
        ? '<span class="lang-suggestion-badge">Suggested</span>'
        : "";

      buttonsHtml += `
        <a href="#prayers/${lang.language}" class="simple-language-button ${isBrowserLang ? "suggested" : ""}">
          <span class="language-code">${lang.language.toUpperCase()}</span>
          <span class="language-name">${displayName}</span>
          <span class="language-count">${lang.uniquePhelps} prayers</span>
          ${suggestionBadge}
        </a>
      `;
    }

    container.innerHTML = buttonsHtml;
  } catch (error) {
    console.error("Error loading language buttons:", error);
    container.innerHTML =
      '<p style="text-align: center; color: #999;">Error loading languages</p>';
  }
}

async function _renderBottomLanguageSelector() {
  const container = document.createElement("div");
  container.className = "simple-language-selector";
  container.innerHTML = `
    <div class="simple-language-selector-header">
      <span class="bahai-star">&#x1f7d9;</span>
      <h3>Browse Prayers by Language</h3>
    </div>
    <div class="simple-language-buttons">
      <div class="bahai-loading-spinner" style="font-size: 2em; margin: 20px auto;">&#x1f7d9;</div>
    </div>
  `;

  // Load language buttons asynchronously and wait for completion
  try {
    const buttonsContainer = container.querySelector(
      ".simple-language-buttons",
    );
    await _loadSimpleLanguageButtons(buttonsContainer);
  } catch (error) {
    console.error("Error loading simple language buttons:", error);
    const buttonContainerForError = container.querySelector(
      ".simple-language-buttons",
    );
    if (buttonContainerForError) {
      buttonContainerForError.innerHTML =
        '<p style="text-align: center; color: #999;">Error loading languages</p>';
    }
  }

  return container;
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
    titleKey: "Holy Writings Reader", // Updated title
    contentRenderer: _renderLanguageListContent,
    showLanguageSwitcher: false, // No longer using the complex language switcher
    showBackButton: false, // No back button on the main/home view
  });
}

async function _renderSearchResultsContent(searchTerm, page) {
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
    if (match) {
      localFoundItems.push({
        ...cachedPrayer,
        opening_text: cachedPrayer.text
          ? cachedPrayer.text.substring(0, MAX_PREVIEW_LENGTH) +
            (cachedPrayer.text.length > MAX_PREVIEW_LENGTH ? "..." : "")
          : "No text preview.",
        source: "cache",
      });
    }
  });

  const dbNameSql = `SELECT version, name, language, phelps, link, source FROM writings WHERE name LIKE '%${saneSearchTermForSql}%' ORDER BY name, version`;
  let dbNameItems = [];
  try {
    const dbNameItemsRaw = await executeQuery(dbNameSql);
    dbNameItems = dbNameItemsRaw.map((item) => ({
      ...item,
      source: "db_name",
    }));
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
    console.warn(
      `Search page out of bounds. Redirecting to page ${currentPage} for "${searchTerm}"`,
    );
    renderSearchResults(searchTerm, currentPage);
    return '<div id="search-results-content-area"><p>Redirecting to a valid page...</p></div>';
  } else if (totalPages === 0 && currentPage > 1) {
    currentPage = 1;
    currentPageBySearchTerm[searchTerm] = currentPage;
    console.warn(
      `Search page out of bounds (no results). Redirecting to page ${currentPage} for "${searchTerm}"`,
    );
    renderSearchResults(searchTerm, currentPage);
    return '<div id="search-results-content-area"><p>Redirecting to a valid page...</p></div>';
  }

  const startIndex = (currentPage - 1) * ITEMS_PER_PAGE;
  const paginatedCombinedResults = combinedResults.slice(
    startIndex,
    startIndex + ITEMS_PER_PAGE,
  );
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
            displayItem = {
              ...item,
              ...dbItemWithText,
              text: dbItemWithText.text,
            };
            cachePrayerText({ ...dbItemWithText });
          } else {
            displayItem.text = null;
          }
        } catch (error) {
          console.error(
            `Error fetching text for search result item ${item.version}:`,
            error,
          );
          displayItem.text = null;
        }
      }
    }

    if (displayItem.text) {
      displayItem.opening_text =
        displayItem.text.substring(0, MAX_PREVIEW_LENGTH) +
        (displayItem.text.length > MAX_PREVIEW_LENGTH ? "..." : "");
    } else {
      displayItem.opening_text = "No text preview available.";
    }
    displayItems.push(displayItem);
  }

  if (totalResults === 0) {
    const searchExplanationForNoResults = `<div style="margin-top: 15px; padding: 10px; background-color: #f0f0f0; border-radius: 4px; font-size: 0.9em;"><p style="margin-bottom: 5px;"><strong>Search Information:</strong></p><ul style="margin-top: 0; padding-left: 20px;"><li>This search looked for "${searchTerm}" in prayer <strong>titles</strong> from the main database.</li><li>It also checked the <strong>full text</strong> of ${allCached.length} prayer(s) stored in your browser's local cache.</li><li>Try Browse <a href="#languages">language lists</a> to add prayers to cache for full-text search.</li></ul></div>`;
    const internalHeaderHtml = `<header><h2><span id="category">Search Results</span><span id="blocktitle">For "${searchTerm ? searchTerm.replace(/"/g, "&quot;") : ""}"</span></h2></header>`;
    return `<div id="search-results-content-area">${internalHeaderHtml}<p>No prayers found matching "${searchTerm}".</p>${searchExplanationForNoResults}<p style="margin-top: 15px;">For comprehensive text search, try <a href="https://tiddly.holywritings.net/workspace" target="_blank">tiddly.holywritings.net/workspace</a>.</p><p>Debug: <a href="${DOLTHUB_REPO_QUERY_URL_BASE}${encodeURIComponent(dbNameSql)}" target="_blank">View DB Name Query</a></p></div>`;
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
        allPhelpsDetailsForCards[row.phelps].push({ ...row });
      });
    } catch (e) {
      console.error("Failed to fetch details for phelps codes in search:", e);
    }
  }

  const listCardPromises = displayItems.map((pData) =>
    createPrayerCardHtml(pData, allPhelpsDetailsForCards),
  );
  const listCardsHtmlArray = await Promise.all(listCardPromises);
  const listHtml = `<div class="favorite-prayer-grid">${listCardsHtmlArray.join("")}</div>`;

  let paginationHtml = "";
  if (totalPages > 1) {
    const escapedSearchTermForJs = searchTerm
      .replace(/'/g, "\\'")
      .replace(/"/g, '\\"');
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

  const internalHeaderHtml = `<header><h2><span id="category">Search Results</span><span id="blocktitle">For "${searchTerm ? searchTerm.replace(/"/g, "&quot;") : ""}" (Page ${currentPage})</span></h2></header>`;
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
    titleKey: `Search Results for "${searchTerm ? searchTerm.replace(/"/g, "&quot;") : ""}"`,
    contentRenderer: async () => _renderSearchResultsContent(searchTerm, page),
    showLanguageSwitcher: true, // Original function included the language picker
    showBackButton: true, // Useful to go back from search results
    activeLangCodeForPicker: null,
    // isPrayerPage: false (default)
  });
}

async function handleRouteChange() {
  await fetchLanguageNames(); // Ensure names are loaded/attempted
  const hash = window.location.hash;
  const [mainHashPath, queryParamsStr] = hash.split("?");
  let pageParam = 1,
    showUnmatchedParam = false; // Default: hide prayers without Phelps codes

  if (queryParamsStr) {
    const params = new URLSearchParams(queryParamsStr);
    if (params.has("page")) {
      const parsedPage = parseInt(params.get("page"), 10);
      if (!isNaN(parsedPage) && parsedPage > 0) pageParam = parsedPage;
    }
    // If filter=showunmatched is present, also show prayers without Phelps codes
    if (params.has("filter") && params.get("filter") === "showunmatched")
      showUnmatchedParam = true;
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
      renderPrayersForLanguage(langCode, pageParam, showUnmatchedParam);
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

function setLanguageView(langCode, page, showUnmatched) {
  // showUnmatched = false means hide prayers without Phelps codes (default)
  // showUnmatched = true means also show prayers without Phelps codes
  window.location.hash = `#prayers/${langCode}?page=${page}${showUnmatched ? "&filter=showunmatched" : ""}`;
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

  // Prayer matcher UI elements have been removed

  // Add event listener for refresh language cache button
  const refreshLanguageCacheButton = document.getElementById(
    "refresh-language-cache-btn",
  );
  if (refreshLanguageCacheButton) {
    refreshLanguageCacheButton.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();

      console.log("[DEBUG] Refresh language cache button clicked");

      // Show loading state
      refreshLanguageCacheButton.disabled = true;
      const icon = refreshLanguageCacheButton.querySelector("i");
      if (icon) {
        icon.style.animation = "spin 1s linear infinite";
      }

      // Clear caches and refresh
      const success = clearLanguageCaches();

      // Show feedback to user
      const snackbarContainer = document.querySelector(".mdl-js-snackbar");
      if (snackbarContainer && snackbarContainer.MaterialSnackbar) {
        const message = success
          ? "Language cache refreshed!"
          : "Error refreshing cache. Check console.";
        snackbarContainer.MaterialSnackbar.showSnackbar({
          message: message,
        });
      }

      // Reset button state after a short delay
      setTimeout(() => {
        refreshLanguageCacheButton.disabled = false;
        if (icon) {
          icon.style.animation = "";
        }
      }, 1000);
    });
    console.log("[DEBUG] Refresh language cache button event listener added");
  }

  // Apply Safari-specific MDL menu fixes on page load
  setTimeout(() => {
    applySafariMdlMenuFixes();
  }, 500);

  // Add Safari menu positioning fix trigger on menu button clicks
  const isSafari = /^((?!chrome|android).)*safari/i.test(navigator.userAgent);
  if (isSafari) {
    document.addEventListener("click", function (event) {
      const menuButton = event.target.closest("#all-languages-menu-btn");
      if (menuButton) {
        // Delay the fix to allow MDL to process the menu opening
        setTimeout(() => {
          fixSafariMenuPositioning();
        }, 50);
      }
    });
    console.log("[DEBUG] Safari menu positioning fix triggers added");
  }

  // Prayer matcher dialogs have been removed

  if (typeof componentHandler !== "undefined" && componentHandler) {
    componentHandler.upgradeDom();
  }
  handleRouteChange();

  // Start background caching of English prayers
  startBackgroundCaching();
});

// Helper function to show/hide inform mistake button based on prayer context
function updateStaticPrayerActionButtonStates(prayer) {
  const informMistakeBtn = document.getElementById("inform-mistake-button");

  if (informMistakeBtn && prayer) {
    informMistakeBtn.style.display = "inline-block";
    informMistakeBtn.disabled = false;
  } else if (informMistakeBtn) {
    informMistakeBtn.style.display = "none";
  }

  console.log(
    `[updateStaticPrayerActionButtonStates] Inform mistake button visible for prayer: ${prayer?.version}`,
  );
}

// Clear corrupted/incompatible language stats cache on app initialization
try {
  const oldCache = localStorage.getItem(LANGUAGE_STATS_CACHE_KEY);
  if (oldCache) {
    try {
      const parsed = JSON.parse(oldCache);
      // If the cached data doesn't have uniquePhelps field, it's the old incompatible format
      if (
        parsed.data &&
        parsed.data.length > 0 &&
        !parsed.data[0].uniquePhelps
      ) {
        console.log("[Init] Clearing incompatible language stats cache");
        localStorage.removeItem(LANGUAGE_STATS_CACHE_KEY);
      }
    } catch (e) {
      // If we can't parse it, remove it
      localStorage.removeItem(LANGUAGE_STATS_CACHE_KEY);
    }
  }
} catch (e) {
  console.warn("[Init] Error checking cache compatibility:", e);
}

window.addEventListener("hashchange", handleRouteChange);
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
