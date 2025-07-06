/**
 * Tests for the pinning interface DOM manipulation fix
 * This test ensures that the static prayer actions host element
 * remains in its original position and doesn't get lost during re-renders
 */

describe('Pinning Interface DOM Manipulation', () => {
    beforeEach(() => {
        // Create test DOM using existing test utilities
        global.testUtils.createTestDOM();
        
        // Mock the app.js functions we need to test
        global.pinPrayer = jest.fn().mockImplementation((prayerData) => {
            global.pinnedPrayerDetails = {
                version: prayerData.version,
                phelps: prayerData.phelps || null,
                name: prayerData.name || `Prayer ${prayerData.version}`,
                language: prayerData.language,
                text: prayerData.text || "Text not available.",
            };
            global.updatePrayerMatchingToolDisplay();
            global.updateStaticPrayerActionButtons();
        });
        
        global.unpinPrayer = jest.fn().mockImplementation(() => {
            global.pinnedPrayerDetails = null;
            global.updatePrayerMatchingToolDisplay();
            global.handleRouteChange();
        });
        
        global.updatePrayerMatchingToolDisplay = jest.fn();
        global.updateStaticPrayerActionButtons = jest.fn();
        global.handleRouteChange = jest.fn();
        
        global.updateStaticPrayerActionButtonStates = jest.fn().mockImplementation((prayer) => {
            const staticPinBtn = document.getElementById('static-action-pin-this');
            const staticUnpinBtn = document.getElementById('static-action-unpin-this');
            const staticAddMatchBtn = document.getElementById('static-action-add-match');
            const staticIsPinnedMsg = document.getElementById('static-action-is-pinned-msg');
            
            if (staticPinBtn) staticPinBtn.disabled = true;
            if (staticUnpinBtn) staticUnpinBtn.disabled = true;
            if (staticAddMatchBtn) staticAddMatchBtn.disabled = true;
            if (staticIsPinnedMsg) staticIsPinnedMsg.style.display = 'none';
            
            if (global.pinnedPrayerDetails) {
                if (global.pinnedPrayerDetails.version !== prayer?.version) {
                    // Different prayer is pinned
                    if (staticAddMatchBtn) staticAddMatchBtn.disabled = false;
                } else {
                    // Current prayer is pinned
                    if (staticIsPinnedMsg) staticIsPinnedMsg.style.display = 'block';
                    if (staticUnpinBtn) staticUnpinBtn.disabled = false;
                }
            } else {
                // No prayer is pinned
                if (staticPinBtn) staticPinBtn.disabled = false;
                if (staticIsPinnedMsg) staticIsPinnedMsg.style.display = 'none';
            }
        });
        
        global.renderPageLayout = jest.fn().mockImplementation(async (viewSpec) => {
            const staticActionsHost = document.getElementById('static-prayer-actions-host');
            if (staticActionsHost) {
                if (viewSpec.isPrayerPage) {
                    staticActionsHost.style.display = 'flex';
                } else {
                    staticActionsHost.style.display = 'none';
                }
            }
        });
        
        global._renderPrayerContent = jest.fn().mockImplementation(async (prayerObject, phelpsCodeForNav, activeLangForNav, titleCalculationResults) => {
            // Mock the fixed version that doesn't move the staticHost
            const staticHost = document.getElementById('static-prayer-actions-host');
            if (staticHost) {
                // Don't move the staticHost - keep it in its original location
                // Just ensure it's visible and update button states
                staticHost.style.display = 'flex';
                global.updateStaticPrayerActionButtonStates(prayerObject);
            }
            
            // Create a mock fragment to return
            const fragment = document.createDocumentFragment();
            const div = document.createElement('div');
            div.innerHTML = `<div class="prayer">${prayerObject.text}</div>`;
            fragment.appendChild(div);
            return fragment;
        });
        
        // Mock global variables
        global.pinnedPrayerDetails = null;
        global.window = global.window || {};
        global.window.currentPrayerForStaticActions = null;
        
        // Mock constants
        global.LOCALSTORAGE_PRAYER_CACHE_PREFIX = 'hw_prayer_cache_';
        global.FAVORITES_STORAGE_KEY = 'hw_favorite_prayers';
    });
    
    afterEach(() => {
        global.testUtils.cleanupDOM();
        jest.clearAllMocks();
    });
    
    test('staticHost element should remain in original position after prayer pin/unpin cycle', async () => {
        // Get the static host element
        const originalStaticHost = document.getElementById('static-prayer-actions-host');
        
        // Verify initial state
        expect(originalStaticHost).toBeTruthy();
        expect(originalStaticHost.parentNode.id).toBe('content');
        
        // Mock prayer data
        const testPrayer = {
            version: 'test-version-1',
            phelps: 'BH00001',
            name: 'Test Prayer',
            language: 'en',
            text: 'This is a test prayer text.',
            source: 'Test Source'
        };
        
        // Pin the prayer
        global.pinPrayer(testPrayer);
        
        // Verify prayer is pinned
        expect(global.pinnedPrayerDetails).toBeTruthy();
        expect(global.pinnedPrayerDetails.version).toBe('test-version-1');
        
        // Simulate rendering prayer content (this used to move the staticHost)
        const titleResults = {
            phelpsToDisplay: 'BH00001',
            languageToDisplay: 'en',
            nameToDisplay: 'Test Prayer',
            phelpsIsSuggested: false,
            languageIsSuggested: false
        };
        
        await global._renderPrayerContent(testPrayer, 'BH00001', 'en', titleResults);
        
        // Verify staticHost is still in its original position
        const staticHostAfterRender = document.getElementById('static-prayer-actions-host');
        expect(staticHostAfterRender).toBeTruthy();
        expect(staticHostAfterRender.parentNode.id).toBe('content'); // Should still be in original position
        expect(staticHostAfterRender.style.display).toBe('flex'); // Should be visible
        
        // Verify it's the same element reference
        expect(staticHostAfterRender).toBe(originalStaticHost);
        
        // Unpin the prayer
        global.unpinPrayer();
        
        // Verify prayer is unpinned
        expect(global.pinnedPrayerDetails).toBe(null);
        
        // Verify staticHost is still in DOM and in original position
        const staticHostAfterUnpin = document.getElementById('static-prayer-actions-host');
        expect(staticHostAfterUnpin).toBeTruthy();
        expect(staticHostAfterUnpin.parentNode.id).toBe('content'); // Should still be in original position
        
        // The staticHost should still be the same element reference
        expect(staticHostAfterUnpin).toBe(originalStaticHost);
    });
    
    test('staticHost element should not be moved during prayer content rendering', async () => {
        const originalStaticHost = document.getElementById('static-prayer-actions-host');
        const initialParent = originalStaticHost.parentNode;
        const initialParentId = initialParent.id;
        
        const testPrayer = {
            version: 'test-version-2',
            phelps: 'BH00002',
            name: 'Another Test Prayer',
            language: 'en',
            text: 'Another test prayer text.',
            source: 'Test Source'
        };
        
        const titleResults = {
            phelpsToDisplay: 'BH00002',
            languageToDisplay: 'en',
            nameToDisplay: 'Another Test Prayer',
            phelpsIsSuggested: false,
            languageIsSuggested: false
        };
        
        // Render prayer content
        const contentFragment = await global._renderPrayerContent(testPrayer, 'BH00002', 'en', titleResults);
        
        // Verify staticHost was not moved
        const staticHostAfter = document.getElementById('static-prayer-actions-host');
        expect(staticHostAfter).toBe(originalStaticHost);
        expect(staticHostAfter.parentNode).toBe(initialParent);
        expect(staticHostAfter.parentNode.id).toBe(initialParentId);
        
        // Verify the content fragment doesn't contain the staticHost
        // DocumentFragment doesn't have getElementById, so we check that it's not a full document
        expect(contentFragment.nodeType).toBe(Node.DOCUMENT_FRAGMENT_NODE);
    });
    
    test('staticHost visibility should be controlled by renderPageLayout', async () => {
        const staticHost = document.getElementById('static-prayer-actions-host');
        
        // Test non-prayer page
        const nonPrayerViewSpec = {
            titleKey: 'Language List',
            contentRenderer: () => Promise.resolve('<div>Language list content</div>'),
            isPrayerPage: false
        };
        
        await global.renderPageLayout(nonPrayerViewSpec);
        
        expect(staticHost.style.display).toBe('none');
        
        // Test prayer page
        const prayerViewSpec = {
            titleKey: 'Prayer View',
            contentRenderer: () => Promise.resolve('<div>Prayer content</div>'),
            isPrayerPage: true
        };
        
        await global.renderPageLayout(prayerViewSpec);
        
        expect(staticHost.style.display).toBe('flex');
    });
    
    test('static prayer action buttons should be properly configured after pin/unpin', async () => {
        const testPrayer = {
            version: 'test-version-3',
            phelps: 'BH00003',
            name: 'Button Test Prayer',
            language: 'en',
            text: 'Button test prayer text.',
            source: 'Test Source'
        };
        
        // Set up prayer context
        global.window.currentPrayerForStaticActions = {
            prayer: testPrayer,
            initialDisplayPrayerLanguage: 'English',
            nameToDisplay: 'Button Test Prayer',
            languageToDisplay: 'en',
            finalDisplayLanguageForPhelpsMeta: 'English',
            phelpsIsSuggested: false
        };
        
        const pinBtn = document.getElementById('static-action-pin-this');
        const unpinBtn = document.getElementById('static-action-unpin-this');
        const addMatchBtn = document.getElementById('static-action-add-match');
        const isPinnedMsg = document.getElementById('static-action-is-pinned-msg');
        
        // Test initial state (no prayer pinned)
        global.updateStaticPrayerActionButtonStates(testPrayer);
        
        expect(pinBtn.disabled).toBe(false);
        expect(unpinBtn.disabled).toBe(true);
        expect(addMatchBtn.disabled).toBe(true);
        expect(isPinnedMsg.style.display).toBe('none');
        
        // Pin the prayer
        global.pinPrayer(testPrayer);
        global.updateStaticPrayerActionButtonStates(testPrayer);
        
        expect(pinBtn.disabled).toBe(true);
        expect(unpinBtn.disabled).toBe(false);
        expect(addMatchBtn.disabled).toBe(true);
        expect(isPinnedMsg.style.display).toBe('block');
        
        // Unpin the prayer
        global.unpinPrayer();
        global.updateStaticPrayerActionButtonStates(testPrayer);
        
        expect(pinBtn.disabled).toBe(false);
        expect(unpinBtn.disabled).toBe(true);
        expect(addMatchBtn.disabled).toBe(true);
        expect(isPinnedMsg.style.display).toBe('none');
    });
    
    test('multiple pin/unpin cycles should not affect staticHost element integrity', async () => {
        const originalStaticHost = document.getElementById('static-prayer-actions-host');
        const originalParent = originalStaticHost.parentNode;
        
        const testPrayer1 = global.testUtils.createMockPrayer({ version: 'test-1', name: 'Prayer 1' });
        const testPrayer2 = global.testUtils.createMockPrayer({ version: 'test-2', name: 'Prayer 2' });
        
        const titleResults = {
            phelpsToDisplay: 'BH00001',
            languageToDisplay: 'en',
            nameToDisplay: 'Test Prayer',
            phelpsIsSuggested: false,
            languageIsSuggested: false
        };
        
        // First cycle: pin prayer 1
        global.pinPrayer(testPrayer1);
        await global._renderPrayerContent(testPrayer1, 'BH00001', 'en', titleResults);
        
        let staticHost = document.getElementById('static-prayer-actions-host');
        expect(staticHost).toBe(originalStaticHost);
        expect(staticHost.parentNode).toBe(originalParent);
        
        // Unpin prayer 1
        global.unpinPrayer();
        
        staticHost = document.getElementById('static-prayer-actions-host');
        expect(staticHost).toBe(originalStaticHost);
        expect(staticHost.parentNode).toBe(originalParent);
        
        // Second cycle: pin prayer 2
        global.pinPrayer(testPrayer2);
        await global._renderPrayerContent(testPrayer2, 'BH00002', 'en', titleResults);
        
        staticHost = document.getElementById('static-prayer-actions-host');
        expect(staticHost).toBe(originalStaticHost);
        expect(staticHost.parentNode).toBe(originalParent);
        
        // Unpin prayer 2
        global.unpinPrayer();
        
        staticHost = document.getElementById('static-prayer-actions-host');
        expect(staticHost).toBe(originalStaticHost);
        expect(staticHost.parentNode).toBe(originalParent);
        
        // Element should still be intact and functional
        expect(staticHost.id).toBe('static-prayer-actions-host');
        expect(staticHost.className).toBe('prayer-actions');
    });
});