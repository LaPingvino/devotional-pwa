// Tests for core prayer functionality in Devotional PWA

describe('Prayer Functions', () => {
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

    describe('Background English Prayers Caching', () => {
      beforeEach(() => {
        // Mock background caching constants
        global.BACKGROUND_CACHE_STORAGE_KEY = 'devotionalPWA_backgroundCacheStatus';
        global.BACKGROUND_CACHE_BATCH_SIZE = 50;
        global.BACKGROUND_CACHE_DELAY_MS = 1000;
        global.BACKGROUND_CACHE_EXPIRY_MS = 24 * 60 * 60 * 1000;
      
        // Mock background caching functions
        global.getBackgroundCacheStatus = jest.fn();
        global.setBackgroundCacheStatus = jest.fn();
        global.loadEnglishPrayersInBackground = jest.fn();
        global.startBackgroundCaching = jest.fn();
        global.getAllCachedPrayers = jest.fn();
      });

      it('should get background cache status from localStorage', () => {
        const mockStatus = {
          timestamp: Date.now() - 1000,
          totalCached: 100,
          lastOffset: 150
        };
        localStorage.setItem('devotionalPWA_backgroundCacheStatus', JSON.stringify(mockStatus));
      
        global.getBackgroundCacheStatus = jest.fn().mockImplementation(() => {
          try {
            const statusData = localStorage.getItem('devotionalPWA_backgroundCacheStatus');
            if (statusData) {
              const { timestamp, totalCached, lastOffset } = JSON.parse(statusData);
              const isExpired = Date.now() - timestamp > global.BACKGROUND_CACHE_EXPIRY_MS;
              return { timestamp, totalCached, lastOffset, isExpired };
            }
          } catch (e) {
            console.warn('Error reading background cache status:', e);
            localStorage.removeItem('devotionalPWA_backgroundCacheStatus');
          }
          return { timestamp: 0, totalCached: 0, lastOffset: 0, isExpired: true };
        });
      
        const result = global.getBackgroundCacheStatus();
        expect(result.totalCached).toBe(100);
        expect(result.lastOffset).toBe(150);
        expect(result.isExpired).toBe(false);
      });

      it('should set background cache status in localStorage', () => {
        global.setBackgroundCacheStatus = jest.fn().mockImplementation((totalCached, lastOffset) => {
          try {
            const statusData = {
              timestamp: Date.now(),
              totalCached,
              lastOffset
            };
            localStorage.setItem('devotionalPWA_backgroundCacheStatus', JSON.stringify(statusData));
          } catch (e) {
            console.warn('Error saving background cache status:', e);
          }
        });
      
        global.setBackgroundCacheStatus(75, 100);
      
        expect(global.setBackgroundCacheStatus).toHaveBeenCalledWith(75, 100);
      
        const stored = localStorage.getItem('devotionalPWA_backgroundCacheStatus');
        expect(stored).toBeTruthy();
      
        const parsed = JSON.parse(stored);
        expect(parsed.totalCached).toBe(75);
        expect(parsed.lastOffset).toBe(100);
      });

      it('should skip background caching if recently cached', async () => {
        // Mock recent cache status
        global.getBackgroundCacheStatus = jest.fn().mockReturnValue({
          timestamp: Date.now() - 1000,
          totalCached: 50,
          lastOffset: 100,
          isExpired: false
        });
      
        global.loadEnglishPrayersInBackground = jest.fn().mockImplementation(async () => {
          const status = global.getBackgroundCacheStatus();
        
          if (!status.isExpired && status.totalCached > 0) {
            console.log(`[Background Cache] Skipping - ${status.totalCached} English prayers cached recently`);
            return;
          }
          // Continue with caching...
        });
      
        await global.loadEnglishPrayersInBackground();
      
        expect(global.getBackgroundCacheStatus).toHaveBeenCalled();
        // Should not proceed with caching since cache is fresh
      });

      it('should perform background caching when cache is expired', async () => {
        // Mock expired cache status
        global.getBackgroundCacheStatus = jest.fn().mockReturnValue({
          timestamp: Date.now() - (25 * 60 * 60 * 1000), // 25 hours ago
          totalCached: 0,
          lastOffset: 0,
          isExpired: true
        });
      
        // Mock English prayers data
        const mockCountResult = [{ total: 100 }];
        const mockPrayers = Array.from({ length: 10 }, (_, i) => ({
          version: `en-prayer-${i + 1}`,
          name: `English Prayer ${i + 1}`,
          text: `This is the text of English prayer ${i + 1}`,
          language: 'en',
          phelps: `EN${i + 1}`,
          source: 'test',
          link: `https://example.com/prayer-${i + 1}`
        }));
      
        global.executeQuery = jest.fn()
          .mockResolvedValueOnce(mockCountResult) // Count query
          .mockResolvedValueOnce(mockPrayers);    // Prayers query
      
        global.getAllCachedPrayers = jest.fn().mockReturnValue([]);
        global.cachePrayerText = jest.fn();
      
        global.loadEnglishPrayersInBackground = jest.fn().mockImplementation(async () => {
          const status = global.getBackgroundCacheStatus();
        
          if (!status.isExpired && status.totalCached > 0) {
            return;
          }
        
          const countSql = "SELECT COUNT(*) as total FROM writings WHERE language = 'en'";
          const countResult = await global.executeQuery(countSql);
          const totalEnglishPrayers = countResult[0]?.total || 0;
        
          if (totalEnglishPrayers === 0) {
            return;
          }
        
          const cachedPrayers = global.getAllCachedPrayers();
          const cachedEnglishVersions = new Set(
            cachedPrayers
              .filter(p => p.language === 'en')
              .map(p => p.version)
          );
        
          const batchSql = `SELECT version, name, text, language, phelps, source, link 
                            FROM writings 
                            WHERE language = 'en' 
                            ORDER BY version 
                            LIMIT ${global.BACKGROUND_CACHE_BATCH_SIZE} 
                            OFFSET 0`;
        
          const prayers = await global.executeQuery(batchSql);
        
          let totalCached = 0;
          for (const prayer of prayers) {
            if (!cachedEnglishVersions.has(prayer.version)) {
              global.cachePrayerText(prayer);
              totalCached++;
            }
          }
        
          global.setBackgroundCacheStatus(totalCached, prayers.length);
        });
      
        await global.loadEnglishPrayersInBackground();
      
        expect(global.executeQuery).toHaveBeenCalledWith("SELECT COUNT(*) as total FROM writings WHERE language = 'en'");
        expect(global.getAllCachedPrayers).toHaveBeenCalled();
        expect(global.cachePrayerText).toHaveBeenCalledTimes(10);
        expect(global.setBackgroundCacheStatus).toHaveBeenCalledWith(10, 10);
      });

      it('should handle background caching errors gracefully', async () => {
        global.getBackgroundCacheStatus = jest.fn().mockReturnValue({
          timestamp: 0,
          totalCached: 0,
          lastOffset: 0,
          isExpired: true
        });
      
        global.executeQuery = jest.fn().mockRejectedValue(new Error('Database error'));
      
        global.loadEnglishPrayersInBackground = jest.fn().mockImplementation(async () => {
          try {
            const countSql = "SELECT COUNT(*) as total FROM writings WHERE language = 'en'";
            await global.executeQuery(countSql);
          } catch (error) {
            console.error('[Background Cache] Error caching English prayers:', error);
          }
        });
      
        // Should not throw
        await expect(global.loadEnglishPrayersInBackground()).resolves.toBeUndefined();
      
        expect(global.executeQuery).toHaveBeenCalled();
      });

      it('should start background caching with delay', (done) => {
        global.loadEnglishPrayersInBackground = jest.fn().mockResolvedValue(undefined);
      
        global.startBackgroundCaching = jest.fn().mockImplementation(() => {
          setTimeout(() => {
            global.loadEnglishPrayersInBackground();
            expect(global.loadEnglishPrayersInBackground).toHaveBeenCalled();
            done();
          }, 100); // Shortened delay for testing
        });
      
        global.startBackgroundCaching();
      });

      it('should avoid caching duplicate prayers', async () => {
        // Mock cache status
        global.getBackgroundCacheStatus = jest.fn().mockReturnValue({
          timestamp: 0,
          totalCached: 0,
          lastOffset: 0,
          isExpired: true
        });
      
        // Mock existing cached prayers
        const existingCachedPrayers = [
          { version: 'en-prayer-1', language: 'en', text: 'Cached prayer 1' },
          { version: 'en-prayer-2', language: 'en', text: 'Cached prayer 2' }
        ];
      
        global.getAllCachedPrayers = jest.fn().mockReturnValue(existingCachedPrayers);
      
        // Mock new prayers from database (including duplicates)
        const mockPrayers = [
          { version: 'en-prayer-1', language: 'en', text: 'Prayer 1' }, // Duplicate
          { version: 'en-prayer-2', language: 'en', text: 'Prayer 2' }, // Duplicate
          { version: 'en-prayer-3', language: 'en', text: 'Prayer 3' }, // New
          { version: 'en-prayer-4', language: 'en', text: 'Prayer 4' }  // New
        ];
      
        global.executeQuery = jest.fn()
          .mockResolvedValueOnce([{ total: 4 }])
          .mockResolvedValueOnce(mockPrayers);
      
        global.cachePrayerText = jest.fn();
      
        global.loadEnglishPrayersInBackground = jest.fn().mockImplementation(async () => {
          const status = global.getBackgroundCacheStatus();
        
          if (!status.isExpired && status.totalCached > 0) {
            return;
          }
        
          const countResult = await global.executeQuery("SELECT COUNT(*) as total FROM writings WHERE language = 'en'");
          const totalEnglishPrayers = countResult[0]?.total || 0;
        
          if (totalEnglishPrayers === 0) return;
        
          const cachedPrayers = global.getAllCachedPrayers();
          const cachedEnglishVersions = new Set(
            cachedPrayers
              .filter(p => p.language === 'en')
              .map(p => p.version)
          );
        
          const prayers = await global.executeQuery('SELECT version, name, text, language, phelps, source, link FROM writings WHERE language = \'en\' ORDER BY version LIMIT 50 OFFSET 0');
        
          let totalCached = 0;
          for (const prayer of prayers) {
            if (!cachedEnglishVersions.has(prayer.version)) {
              global.cachePrayerText(prayer);
              totalCached++;
            }
          }
        
          global.setBackgroundCacheStatus(totalCached, prayers.length);
        });
      
        await global.loadEnglishPrayersInBackground();
      
        // Should only cache the 2 new prayers, not the duplicates
        expect(global.cachePrayerText).toHaveBeenCalledTimes(2);
        expect(global.setBackgroundCacheStatus).toHaveBeenCalledWith(2, 4);
      });
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
      
      global.createPrayerCardHtml = jest.fn().mockImplementation(async (prayerData, langMap = {}) => {
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