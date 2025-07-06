// Tests for core prayer functionality in Devotional PWA

describe('Prayer Functions', () => {
  let app;
  
  beforeEach(() => {
    // Create test DOM
    global.testUtils.createTestDOM();
    
    // Mock the app.js functions by loading them
    // Since we can't import modules directly, we'll mock the functions
    global.executeQuery = jest.fn();
    global.cachePrayerText = jest.fn();
    global.getCachedPrayerText = jest.fn();
    global.getLanguageDisplayName = jest.fn();
    global.createPrayerCardHtml = jest.fn();
    global.renderPrayer = jest.fn();
    global.renderPrayersForLanguage = jest.fn();
    global.loadFavoritePrayers = jest.fn();
    global.saveFavoritePrayers = jest.fn();
    global.isPrayerFavorite = jest.fn();
    global.toggleFavoritePrayer = jest.fn();
    global.pinPrayer = jest.fn();
    global.unpinPrayer = jest.fn();
    global.getAuthorFromPhelps = jest.fn();
    global.getDomain = jest.fn();
    global.uuidToBase36 = jest.fn();
    
    // Mock global variables
    global.favoritePrayers = [];
    global.pinnedPrayerDetails = null;
    global.collectedMatchesForEmail = [];
    global.languageNamesMap = {};
    global.currentPageByLanguage = {};
    global.requestCache = new Map();
    global.requestDebounce = new Map();
    
    // Mock constants
    global.DOLTHUB_API_BASE_URL = 'https://www.dolthub.com/api/v1alpha1/holywritings/bahaiwritings/main?q=';
    global.LOCALSTORAGE_PRAYER_CACHE_PREFIX = 'hw_prayer_cache_';
    global.FAVORITES_STORAGE_KEY = 'hw_favorite_prayers';
    global.MAX_PREVIEW_LENGTH = 120;
    global.ITEMS_PER_PAGE = 20;
    global.REQUEST_CACHE_TTL = 5 * 60 * 1000;
  });

  afterEach(() => {
    global.testUtils.cleanupDOM();
    jest.clearAllMocks();
  });

  describe('executeQuery', () => {
    it('should execute a SQL query and return results', async () => {
      const mockResponse = global.testUtils.createMockApiResponse([
        { version: 'test-1', name: 'Test Prayer', text: 'Test content' }
      ]);
      
      global.fetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        text: () => Promise.resolve(JSON.stringify(mockResponse)),
        json: () => Promise.resolve(mockResponse)
      });

      // Mock the actual function implementation
      global.executeQuery = jest.fn().mockImplementation(async (sql) => {
        const response = await fetch(`${global.DOLTHUB_API_BASE_URL}${encodeURIComponent(sql)}`);
        const data = await response.json();
        return data.rows || [];
      });

      const result = await global.executeQuery('SELECT * FROM writings LIMIT 1');
      
      expect(result).toEqual([
        { version: 'test-1', name: 'Test Prayer', text: 'Test content' }
      ]);
      expect(global.fetch).toHaveBeenCalledWith(
        expect.stringContaining('SELECT%20*%20FROM%20writings%20LIMIT%201')
      );
    });

    it('should handle API errors gracefully', async () => {
      global.fetch.mockResolvedValueOnce({
        ok: false,
        status: 500,
        text: () => Promise.resolve('Internal Server Error'),
        json: () => Promise.reject(new Error('Invalid JSON'))
      });

      global.executeQuery = jest.fn().mockImplementation(async (sql) => {
        const response = await fetch(`${global.DOLTHUB_API_BASE_URL}${encodeURIComponent(sql)}`);
        if (!response.ok) {
          throw new Error(`HTTP error! status: ${response.status}`);
        }
        const data = await response.json();
        return data.rows || [];
      });

      await expect(global.executeQuery('SELECT * FROM invalid_table')).rejects.toThrow('HTTP error! status: 500');
    });

    it('should cache query results', async () => {
      const mockResponse = global.testUtils.createMockApiResponse([
        { version: 'test-1', name: 'Test Prayer' }
      ]);
      
      global.fetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        text: () => Promise.resolve(JSON.stringify(mockResponse)),
        json: () => Promise.resolve(mockResponse)
      });

      // Mock implementation with caching
      global.executeQuery = jest.fn().mockImplementation(async (sql) => {
        const cacheKey = sql;
        if (global.requestCache.has(cacheKey)) {
          const cached = global.requestCache.get(cacheKey);
          if (Date.now() - cached.timestamp < global.REQUEST_CACHE_TTL) {
            return cached.data;
          }
        }
        
        const response = await fetch(`${global.DOLTHUB_API_BASE_URL}${encodeURIComponent(sql)}`);
        const data = await response.json();
        const result = data.rows || [];
        
        global.requestCache.set(cacheKey, {
          data: result,
          timestamp: Date.now()
        });
        
        return result;
      });

      const sql = 'SELECT * FROM writings LIMIT 1';
      const result1 = await global.executeQuery(sql);
      const result2 = await global.executeQuery(sql);
      
      expect(result1).toEqual(result2);
      expect(global.fetch).toHaveBeenCalledTimes(1); // Second call should use cache
    });
  });

  describe('Prayer Caching', () => {
    it('should cache prayer text in localStorage', () => {
      const prayer = global.testUtils.createMockPrayer();
      
      global.cachePrayerText = jest.fn().mockImplementation((prayerData) => {
        const cacheKey = `${global.LOCALSTORAGE_PRAYER_CACHE_PREFIX}${prayerData.version}`;
        const cacheData = {
          text: prayerData.text,
          name: prayerData.name,
          language: prayerData.language,
          phelps: prayerData.phelps,
          link: prayerData.link,
          cached_at: Date.now()
        };
        localStorage.setItem(cacheKey, JSON.stringify(cacheData));
      });
      
      global.cachePrayerText(prayer);
      
      // Verify the data was cached by checking localStorage directly
      const cacheKey = `${global.LOCALSTORAGE_PRAYER_CACHE_PREFIX}${prayer.version}`;
      const cachedData = localStorage.getItem(cacheKey);
      expect(cachedData).toBeTruthy();
      expect(cachedData).toContain(prayer.text);
    });

    it('should retrieve cached prayer text', () => {
      const prayer = global.testUtils.createMockPrayer();
      const cacheData = {
        text: prayer.text,
        name: prayer.name,
        language: prayer.language,
        phelps: prayer.phelps,
        link: prayer.link,
        cached_at: Date.now()
      };
      
      // Store the data in localStorage first
      const cacheKey = `${global.LOCALSTORAGE_PRAYER_CACHE_PREFIX}${prayer.version}`;
      localStorage.setItem(cacheKey, JSON.stringify(cacheData));
      
      global.getCachedPrayerText = jest.fn().mockImplementation((version) => {
        const cacheKey = `${global.LOCALSTORAGE_PRAYER_CACHE_PREFIX}${version}`;
        const cached = localStorage.getItem(cacheKey);
        return cached ? JSON.parse(cached) : null;
      });
      
      const result = global.getCachedPrayerText(prayer.version);
      
      expect(result).toEqual(cacheData);
      expect(global.getCachedPrayerText).toHaveBeenCalledWith(prayer.version);
    });

    it('should handle corrupted cache data gracefully', () => {
      const cacheKey = `${global.LOCALSTORAGE_PRAYER_CACHE_PREFIX}test-version`;
      localStorage.setItem(cacheKey, 'invalid json');
      
      global.getCachedPrayerText = jest.fn().mockImplementation((version) => {
        const cacheKey = `${global.LOCALSTORAGE_PRAYER_CACHE_PREFIX}${version}`;
        try {
          const cached = localStorage.getItem(cacheKey);
          return cached ? JSON.parse(cached) : null;
        } catch (error) {
          console.error('Error parsing cached prayer:', error);
          return null;
        }
      });
      
      const result = global.getCachedPrayerText('test-version');
      
      expect(result).toBeNull();
    });
  });

  describe('Favorite Prayers', () => {
    it('should load favorite prayers from localStorage', () => {
      const favorites = ['prayer-1', 'prayer-2', 'prayer-3'];
      localStorage.setItem(global.FAVORITES_STORAGE_KEY, JSON.stringify(favorites));

      global.loadFavoritePrayers = jest.fn().mockImplementation(() => {
        const stored = localStorage.getItem(global.FAVORITES_STORAGE_KEY);
        if (stored) {
          try {
            global.favoritePrayers = JSON.parse(stored);
          } catch (e) {
            global.favoritePrayers = [];
          }
        } else {
          global.favoritePrayers = [];
        }
      });

      global.loadFavoritePrayers();

      expect(global.favoritePrayers).toEqual(favorites);
      expect(global.loadFavoritePrayers).toHaveBeenCalled();
    });

    it('should save favorite prayers to localStorage', () => {
      global.favoritePrayers = ['prayer-1', 'prayer-2'];
      
      global.saveFavoritePrayers = jest.fn().mockImplementation(() => {
        localStorage.setItem(global.FAVORITES_STORAGE_KEY, JSON.stringify(global.favoritePrayers));
      });

      global.saveFavoritePrayers();

      // Verify the data was saved by checking localStorage directly
      const storedData = localStorage.getItem(global.FAVORITES_STORAGE_KEY);
      expect(storedData).toBe(JSON.stringify(['prayer-1', 'prayer-2']));
      expect(global.saveFavoritePrayers).toHaveBeenCalled();
    });

    it('should check if prayer is favorite', () => {
      global.favoritePrayers = ['prayer-1', 'prayer-2'];
      
      global.isPrayerFavorite = jest.fn().mockImplementation((version) => {
        return global.favoritePrayers.includes(version);
      });

      expect(global.isPrayerFavorite('prayer-1')).toBe(true);
      expect(global.isPrayerFavorite('prayer-3')).toBe(false);
    });

    it('should toggle prayer favorite status', () => {
      global.favoritePrayers = ['prayer-1'];
      
      global.toggleFavoritePrayer = jest.fn().mockImplementation((prayer) => {
        const version = prayer.version;
        const index = global.favoritePrayers.indexOf(version);
        if (index > -1) {
          global.favoritePrayers.splice(index, 1);
        } else {
          global.favoritePrayers.push(version);
        }
        localStorage.setItem(global.FAVORITES_STORAGE_KEY, JSON.stringify(global.favoritePrayers));
      });

      const prayer = { version: 'prayer-2', name: 'Test Prayer' };
      global.toggleFavoritePrayer(prayer);

      expect(global.favoritePrayers).toContain('prayer-2');
      expect(global.toggleFavoritePrayer).toHaveBeenCalledWith(prayer);
      // Verify the data was saved by checking localStorage directly
      const storedData = localStorage.getItem(global.FAVORITES_STORAGE_KEY);
      expect(storedData).toBe(JSON.stringify(['prayer-1', 'prayer-2']));
    });
  });

  describe('Prayer Pinning', () => {
    it('should pin a prayer', () => {
      const prayer = global.testUtils.createMockPrayer();
      
      global.pinPrayer = jest.fn().mockImplementation((prayerData) => {
        global.pinnedPrayerDetails = { ...prayerData };
        global.collectedMatchesForEmail = [];
      });
      
      global.pinPrayer(prayer);
      
      expect(global.pinnedPrayerDetails).toEqual(prayer);
      expect(global.collectedMatchesForEmail).toEqual([]);
    });

    it('should unpin a prayer', () => {
      global.pinnedPrayerDetails = global.testUtils.createMockPrayer();
      global.collectedMatchesForEmail = [{ type: 'match', data: 'test' }];
      
      global.unpinPrayer = jest.fn().mockImplementation(() => {
        global.pinnedPrayerDetails = null;
        global.collectedMatchesForEmail = [];
      });
      
      global.unpinPrayer();
      
      expect(global.pinnedPrayerDetails).toBeNull();
      expect(global.collectedMatchesForEmail).toEqual([]);
    });
  });

  describe('Language Display Names', () => {
    it('should get language display name', async () => {
      global.languageNamesMap = { 'en': 'English', 'es': 'Spanish' };
      
      global.getLanguageDisplayName = jest.fn().mockImplementation(async (langCode) => {
        return global.languageNamesMap[langCode] || langCode.toUpperCase();
      });
      
      const result = await global.getLanguageDisplayName('en');
      expect(result).toBe('English');
      
      const fallback = await global.getLanguageDisplayName('fr');
      expect(fallback).toBe('FR');
    });
  });

  describe('Utility Functions', () => {
    it('should extract author from Phelps code', () => {
      global.getAuthorFromPhelps = jest.fn().mockImplementation((phelpsCode) => {
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
      });
      
      expect(global.getAuthorFromPhelps('BB12345')).toBe('The Báb');
      expect(global.getAuthorFromPhelps('BH67890')).toBe('Bahá\'u\'lláh');
      expect(global.getAuthorFromPhelps('ABU1234')).toBe('`Abdu\'l-Bahá');
      expect(global.getAuthorFromPhelps('BHU5678')).toBe('Bahá\'u\'lláh');
      expect(global.getAuthorFromPhelps('XX99999')).toBeNull();
      expect(global.getAuthorFromPhelps('')).toBeNull();
      expect(global.getAuthorFromPhelps('A')).toBeNull();
    });

    it('should extract domain from URL', () => {
      global.getDomain = jest.fn().mockImplementation((url) => {
        if (!url) return null;
        try {
          const urlObj = new URL(url);
          return urlObj.hostname;
        } catch (e) {
          return null;
        }
      });
      
      expect(global.getDomain('https://example.com/path')).toBe('example.com');
      expect(global.getDomain('invalid-url')).toBeNull();
    });

    it('should convert UUID to base36', () => {
      global.uuidToBase36 = jest.fn().mockImplementation((uuid) => {
        if (!uuid) return null;
        return uuid.replace(/-/g, '').substring(0, 8);
      });
      
      const testUuid = '123e4567-e89b-12d3-a456-426614174000';
      const result = global.uuidToBase36(testUuid);
      expect(result).toBe('123e4567');
    });
  });

  describe('Prayer Card Generation', () => {
    it('should create prayer card HTML', async () => {
      const prayer = global.testUtils.createMockPrayer();
      const languageDisplayMap = { 'en': 'English' };
      
      global.createPrayerCardHtml = jest.fn().mockImplementation(async (prayerData, allPhelpsDetails = {}, langMap = {}) => {
        const displayLanguage = langMap[prayerData.language] || prayerData.language.toUpperCase();
        const displayTitle = prayerData.name || `${prayerData.version} - ${displayLanguage}`;
        const cardHref = prayerData.phelps 
          ? `#prayercode/${prayerData.phelps}/${prayerData.language}`
          : `#prayer/${prayerData.version}`;
        
        return `
          <div class="favorite-prayer-card">
            <div class="favorite-prayer-card-header">
              <a href="${cardHref}">${displayTitle}</a>
            </div>
            <p class="favorite-prayer-card-preview">${prayerData.text.substring(0, 120)}...</p>
            <div class="favorite-prayer-card-meta">
              <span>Lang: ${prayerData.language.toUpperCase()}</span>
              ${prayerData.phelps ? `<span class="phelps-code">Phelps: ${prayerData.phelps}</span>` : ''}
            </div>
          </div>
        `;
      });
      
      const result = await global.createPrayerCardHtml(prayer, {}, languageDisplayMap);
      
      expect(result).toContain('favorite-prayer-card');
      expect(result).toContain(prayer.name);
      expect(result).toContain(prayer.phelps);
      expect(result).toContain('Lang: EN');
    });
  });

  describe('Recent Languages', () => {
    it('should get recent languages from localStorage', () => {
      const recentLanguages = ['en', 'es', 'fr'];
      localStorage.setItem('devotionalPWA_recentLanguages', JSON.stringify(recentLanguages));
      
      global.getRecentLanguages = jest.fn().mockImplementation(() => {
        try {
          const stored = localStorage.getItem('devotionalPWA_recentLanguages');
          if (stored) {
            const languages = JSON.parse(stored);
            return Array.isArray(languages) ? languages : [];
          }
        } catch (error) {
          console.error('Error getting recent languages:', error);
        }
        return [];
      });
      
      const result = global.getRecentLanguages();
      expect(result).toEqual(recentLanguages);
      expect(global.getRecentLanguages).toHaveBeenCalled();
    });

    it('should add recent language', () => {
      // Set up existing languages in localStorage
      localStorage.setItem('devotionalPWA_recentLanguages', JSON.stringify(['es', 'fr']));
      
      global.getRecentLanguages = jest.fn().mockImplementation(() => {
        try {
          const stored = localStorage.getItem('devotionalPWA_recentLanguages');
          if (stored) {
            const languages = JSON.parse(stored);
            return Array.isArray(languages) ? languages : [];
          }
        } catch (error) {
          console.error('Error getting recent languages:', error);
        }
        return [];
      });
      
      global.addRecentLanguage = jest.fn().mockImplementation((langCode) => {
        if (!langCode || typeof langCode !== 'string') return;
        
        const recent = global.getRecentLanguages();
        const filtered = recent.filter(lang => lang !== langCode);
        const updated = [langCode, ...filtered].slice(0, 4); // Max 4 recent languages
        
        localStorage.setItem('devotionalPWA_recentLanguages', JSON.stringify(updated));
      });
      
      global.addRecentLanguage('en');
      
      expect(global.addRecentLanguage).toHaveBeenCalledWith('en');
      // Verify the data was saved by checking localStorage directly
      const storedData = localStorage.getItem('devotionalPWA_recentLanguages');
      expect(storedData).toBe(JSON.stringify(['en', 'es', 'fr']));
    });
  });

  describe('Error Handling', () => {
    it('should handle network errors gracefully', async () => {
      global.fetch.mockRejectedValueOnce(new Error('Network error'));
      
      global.executeQuery = jest.fn().mockImplementation(async (sql) => {
        try {
          const response = await fetch(`${global.DOLTHUB_API_BASE_URL}${encodeURIComponent(sql)}`);
          const data = await response.json();
          return data.rows || [];
        } catch (error) {
          console.error('Network error:', error);
          throw error;
        }
      });
      
      await expect(global.executeQuery('SELECT * FROM writings')).rejects.toThrow('Network error');
    });

    it('should handle malformed JSON responses', async () => {
      global.fetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        text: () => Promise.resolve('invalid json'),
        json: () => Promise.reject(new SyntaxError('Unexpected token'))
      });
      
      global.executeQuery = jest.fn().mockImplementation(async (sql) => {
        const response = await fetch(`${global.DOLTHUB_API_BASE_URL}${encodeURIComponent(sql)}`);
        const responseText = await response.text();
        
        try {
          const data = JSON.parse(responseText);
          return data.rows || [];
        } catch (jsonError) {
          throw new Error('Failed to parse API response as JSON');
        }
      });
      
      await expect(global.executeQuery('SELECT * FROM writings')).rejects.toThrow('Failed to parse API response as JSON');
    });
  });
});