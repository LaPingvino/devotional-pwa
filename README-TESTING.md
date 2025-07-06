# Testing Guide for Devotional PWA

This guide explains how to set up and run tests for the Holy Writings Reader application.

## Prerequisites

- Node.js (version 14 or higher)
- npm (comes with Node.js)

## Installation

1. **Install Node.js dependencies:**
   ```bash
   cd prayercodes/devotional-pwa
   npm install
   ```

2. **Install development dependencies:**
   ```bash
   npm install --save-dev jest@^29.0.0 jest-environment-jsdom@^29.0.0 eslint@^8.0.0 fetch-mock@^9.11.0 @testing-library/jest-dom@^5.16.0 @testing-library/dom@^8.19.0
   ```

## Running Tests

### Basic Test Commands

```bash
# Run all tests once
npm test

# Run tests in watch mode (re-runs when files change)
npm run test:watch

# Run tests with coverage report
npm run test:coverage

# Run tests with debugging
npm run test:debug

# Run linting
npm run lint
```

### Test Structure

```
tests/
├── setup.js              # Test configuration and global mocks
├── prayer-functions.test.js # Tests for core prayer functionality
└── ui-components.test.js   # Tests for UI components (to be added)
```

### What's Being Tested

1. **Core Prayer Functions:**
   - `executeQuery()` - API calls to DoltHub
   - Prayer caching (`cachePrayerText`, `getCachedPrayerText`)
   - Favorite prayers management
   - Prayer pinning functionality
   - Language display names
   - Author extraction from Phelps codes

2. **Utility Functions:**
   - URL domain extraction
   - UUID to base36 conversion
   - Recent languages storage

3. **Error Handling:**
   - Network errors
   - Malformed JSON responses
   - Corrupted cache data

### Test Coverage

The test suite aims for:
- **70% line coverage**
- **70% function coverage**
- **70% branch coverage**
- **70% statement coverage**

View coverage report:
```bash
npm run test:coverage
```

## Writing New Tests

### Test File Structure

```javascript
describe('Feature Name', () => {
  beforeEach(() => {
    // Setup before each test
    global.testUtils.createTestDOM();
    // Mock functions
  });

  afterEach(() => {
    // Cleanup after each test
    global.testUtils.cleanupDOM();
    jest.clearAllMocks();
  });

  it('should do something specific', () => {
    // Test implementation
  });
});
```

### Available Test Utilities

The `global.testUtils` object provides:

- `createMockPrayer(overrides)` - Creates mock prayer data
- `createMockApiResponse(rows, status)` - Creates mock API response
- `waitForPromises()` - Waits for async operations
- `createTestDOM()` - Sets up DOM for testing
- `cleanupDOM()` - Cleans up DOM after tests
- `restoreConsole()` - Restores console methods

### Mock Data Examples

```javascript
// Mock prayer with proper Phelps code
const prayer = global.testUtils.createMockPrayer({
  phelps: 'BH12345', // Bahá'u'lláh, 5 digits
  language: 'en'
});

// Mock prayer with utterance code
const utterance = global.testUtils.createMockPrayer({
  phelps: 'ABU1234', // 'Abdu'l-Bahá utterance, 4 digits
  language: 'fa'
});
```

### Phelps Code Format

Phelps codes follow these patterns:
- **Author code + 5 digits**: `BH12345`, `AB67890`, `BB54321`
- **Author code + U + 4 digits**: `BHU1234`, `ABU5678` (for utterances)

Author codes:
- `BB` - The Báb
- `BH` - Bahá'u'lláh  
- `AB` - 'Abdu'l-Bahá

## Debugging Tests

### Debug Mode
```bash
npm run test:debug
```

Then open Chrome and navigate to `chrome://inspect`

### Verbose Output
```bash
npm test -- --verbose
```

### Run Specific Test
```bash
npm test -- --testNamePattern="executeQuery"
```

### Run Specific File
```bash
npm test tests/prayer-functions.test.js
```

## Continuous Integration

### GitHub Actions Example

```yaml
name: Tests
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v2
      - uses: actions/setup-node@v2
        with:
          node-version: '18'
      - run: npm install
      - run: npm test
      - run: npm run test:coverage
```

## Common Issues

### DOM Not Available
If you get DOM-related errors, ensure:
```javascript
beforeEach(() => {
  global.testUtils.createTestDOM();
});
```

### Fetch Not Mocked
If API calls fail, ensure fetch is mocked:
```javascript
global.fetch.mockResolvedValueOnce({
  ok: true,
  json: () => Promise.resolve(mockData)
});
```

### LocalStorage Errors
LocalStorage is mocked globally, but you can reset it:
```javascript
afterEach(() => {
  localStorage.clear();
});
```

## Performance Testing

For performance testing the prayer loading optimizations:

```javascript
it('should load prayers efficiently', async () => {
  const startTime = performance.now();
  
  // Test the optimized prayer loading
  await global.renderPrayersForLanguage('en', 1);
  
  const endTime = performance.now();
  const duration = endTime - startTime;
  
  // Should complete within reasonable time
  expect(duration).toBeLessThan(2000); // 2 seconds
});
```

## Test Data

Tests use realistic but fake data:
- Prayer texts are sample content
- Phelps codes follow proper format
- Language codes are standard ISO codes
- URLs are example.com domains

## Contributing

When adding new features:

1. **Write tests first** (TDD approach)
2. **Test both success and error cases**
3. **Mock external dependencies** (API calls, DOM, localStorage)
4. **Keep tests focused** (one concept per test)
5. **Use descriptive test names** 
6. **Maintain coverage above 70%**

Run tests before submitting:
```bash
npm test && npm run lint
```
