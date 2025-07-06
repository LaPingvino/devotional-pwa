// Jest setup file for Devotional PWA tests

// Import testing utilities
require('@testing-library/jest-dom');

// Mock fetch globally
global.fetch = jest.fn();

// Mock localStorage
const localStorageMock = (() => {
  let store = {};
  return {
    getItem: jest.fn((key) => store[key] || null),
    setItem: jest.fn((key, value) => {
      store[key] = value.toString();
    }),
    removeItem: jest.fn((key) => {
      delete store[key];
    }),
    clear: jest.fn(() => {
      store = {};
      localStorageMock.getItem.mockClear();
      localStorageMock.setItem.mockClear();
      localStorageMock.removeItem.mockClear();
    }),
  };
})();
global.localStorage = localStorageMock;

// Mock sessionStorage
const sessionStorageMock = (() => {
  let store = {};
  return {
    getItem: jest.fn((key) => store[key] || null),
    setItem: jest.fn((key, value) => {
      store[key] = value.toString();
    }),
    removeItem: jest.fn((key) => {
      delete store[key];
    }),
    clear: jest.fn(() => {
      store = {};
      sessionStorageMock.getItem.mockClear();
      sessionStorageMock.setItem.mockClear();
      sessionStorageMock.removeItem.mockClear();
    }),
  };
})();
global.sessionStorage = sessionStorageMock;

// Mock window.location
delete window.location;
window.location = {
  href: 'http://localhost:8000/',
  hash: '',
  pathname: '/',
  search: '',
  origin: 'http://localhost:8000',
  protocol: 'http:',
  hostname: 'localhost',
  port: '8000',
  assign: jest.fn(),
  replace: jest.fn(),
  reload: jest.fn(),
};

// Mock window.navigator
Object.defineProperty(window, 'navigator', {
  value: {
    userAgent: 'Mozilla/5.0 (Test Environment)',
    language: 'en-US',
    clipboard: {
      writeText: jest.fn(() => Promise.resolve()),
      readText: jest.fn(() => Promise.resolve('')),
    },
  },
  writable: true,
});

// Mock crypto for fingerprinting
Object.defineProperty(window, 'crypto', {
  value: {
    subtle: {
      digest: jest.fn(() => Promise.resolve(new ArrayBuffer(20))),
    },
  },
  writable: true,
});

// Mock Material Design Lite
global.componentHandler = {
  upgradeDom: jest.fn(),
  upgradeElement: jest.fn(),
  downgradeElements: jest.fn(),
};

// Mock TextEncoder/TextDecoder
global.TextEncoder = global.TextEncoder || class TextEncoder {
  encode(str) {
    return new Uint8Array(Buffer.from(str, 'utf8'));
  }
};

global.TextDecoder = global.TextDecoder || class TextDecoder {
  decode(bytes) {
    return Buffer.from(bytes).toString('utf8');
  }
};

// Mock console methods to reduce noise in tests
const originalError = console.error;
const originalWarn = console.warn;
const originalLog = console.log;

console.error = jest.fn();
console.warn = jest.fn();
console.log = jest.fn();

// Restore console methods after each test
afterEach(() => {
  jest.clearAllMocks();
  localStorage.clear();
  sessionStorage.clear();
  window.location.hash = '';
  
  // Reset fetch mock
  if (global.fetch.mockReset) {
    global.fetch.mockReset();
  }
});

// Global test utilities
global.testUtils = {
  // Helper to create mock prayer data
  createMockPrayer: (overrides = {}) => ({
    version: 'test-version-1',
    name: 'Test Prayer',
    text: 'This is a test prayer text for testing purposes.',
    language: 'en',
    phelps: 'BH12345',
    source: 'Test Source',
    link: 'https://example.com/test-prayer',
    ...overrides,
  }),

  // Helper to create mock API response
  createMockApiResponse: (rows = [], status = 'Success') => ({
    query_execution_status: status,
    query_execution_message: '',
    repository_owner: 'holywritings',
    repository_name: 'bahaiwritings',
    commit_ref: 'main',
    sql_query: 'SELECT * FROM writings',
    schema: [
      { columnName: 'version', columnType: 'varchar(255)' },
      { columnName: 'name', columnType: 'varchar(255)' },
      { columnName: 'text', columnType: 'text' },
      { columnName: 'language', columnType: 'varchar(16)' },
      { columnName: 'phelps', columnType: 'varchar(32)' },
      { columnName: 'source', columnType: 'varchar(255)' },
      { columnName: 'link', columnType: 'varchar(255)' },
    ],
    rows,
  }),

  // Helper to wait for promises to resolve
  waitForPromises: () => new Promise(resolve => setTimeout(resolve, 0)),

  // Helper to create DOM elements for testing
  createTestDOM: () => {
    document.body.innerHTML = `
      <div class="mdl-layout mdl-js-layout mdl-layout--fixed-header">
        <header class="mdl-layout__header">
          <div class="mdl-layout__header-row">
            <a href="#" class="mdl-layout-title">Holy Writings Reader</a>
            <div class="mdl-layout-spacer"></div>
            <div class="mdl-textfield mdl-js-textfield mdl-textfield--expandable">
              <input class="mdl-textfield__input" type="text" id="header-search-field" placeholder="Search Prayers" />
            </div>
          </div>
        </header>
        <main class="mdl-layout__content">
          <div id="main-view-container">
            <div id="content-column">
              <div id="content">
                <header id="page-header-container">
                  <button id="page-header-favorite-button" class="mdl-button mdl-js-button mdl-button--icon favorite-toggle-button">
                    <i class="material-icons">star_border</i>
                  </button>
                  <h2 id="page-main-title"></h2>
                </header>
                <div id="static-prayer-actions-host" class="prayer-actions">
                  <button id="static-action-pin-this" class="mdl-button mdl-js-button mdl-button--raised mdl-button--colored">
                    <i class="material-icons">push_pin</i> Pin this Prayer
                  </button>
                  <button id="static-action-add-match" class="mdl-button mdl-js-button mdl-button--raised mdl-button--accent">
                    <i class="material-icons">playlist_add_check</i> Match with Pinned
                  </button>
                  <button id="static-action-replace-pin" class="mdl-button mdl-js-button mdl-button--raised">
                    <i class="material-icons">swap_horiz</i> Replace Pin
                  </button>
                  <p id="static-action-is-pinned-msg"><em>This prayer is currently pinned.</em></p>
                  <button id="static-action-unpin-this" class="mdl-button mdl-js-button mdl-button--raised mdl-button--accent">
                    <i class="material-icons">highlight_off</i> Unpin this Prayer
                  </button>
                  <button id="static-action-suggest-phelps" class="mdl-button mdl-js-button mdl-button--raised">
                    <i class="material-icons">library_add</i> Add/Suggest Phelps Code
                  </button>
                  <button id="static-action-change-lang" class="mdl-button mdl-js-button mdl-button--raised">
                    <i class="material-icons">translate</i> Change Language
                  </button>
                  <button id="static-action-change-name" class="mdl-button mdl-js-button mdl-button--raised">
                    <i class="material-icons">edit</i> Change Name
                  </button>
                  <button id="static-action-add-note" class="mdl-button mdl-js-button mdl-button--raised">
                    <i class="material-icons">note_add</i> Add Note
                  </button>
                </div>
              </div>
            </div>
          </div>
        </main>
      </div>
      <div class="mdl-js-snackbar mdl-snackbar">
        <div class="mdl-snackbar__text"></div>
        <button class="mdl-snackbar__action" type="button"></button>
      </div>
    `;
  },

  // Helper to clean up DOM
  cleanupDOM: () => {
    document.body.innerHTML = '';
  },

  // Helper to restore console methods
  restoreConsole: () => {
    console.error = originalError;
    console.warn = originalWarn;
    console.log = originalLog;
  },
};

// Clean up after all tests
afterAll(() => {
  global.testUtils.restoreConsole();
});