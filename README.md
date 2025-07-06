# Holy Writings Reader - Devotional PWA

A Progressive Web App for reading prayers and religious texts from the Bahá'í Faith. This application provides an intuitive interface for browsing, searching, and managing holy writings with advanced caching capabilities.

## Features

### Core Functionality
- **Prayer Browsing**: Browse prayers by language and category
- **Advanced Search**: Search through prayer titles and cached full text
- **Favorites Management**: Save and organize favorite prayers
- **Prayer Pinning**: Pin prayers for comparison and matching
- **Language Support**: Multi-language interface with display names
- **Responsive Design**: Works on desktop and mobile devices

### 🌟 New: Background Caching System

The app now includes an intelligent background caching system that automatically loads English prayers into your browser's cache for lightning-fast search performance.

#### How It Works
- **Automatic**: Starts 3 seconds after page load
- **Smart**: Only caches prayers not already stored locally
- **Batch Processing**: Processes 50 prayers at a time to avoid API overload
- **Efficient**: 1-second delay between batches for optimal performance
- **Persistent**: Cache status tracked with 24-hour expiration
- **User-Friendly**: Subtle notification when significant caching occurs

#### Benefits
- **Faster Search**: Full-text search through cached English prayers
- **Improved Performance**: Reduced API calls for frequently accessed content
- **Better User Experience**: Instant results for common searches
- **Offline-Ready**: Cached prayers available without internet connection

## Technology Stack

- **Frontend**: Vanilla JavaScript, HTML5, CSS3
- **UI Framework**: Material Design Lite (MDL)
- **Database**: DoltHub API (holywritings/bahaiwritings)
- **Storage**: LocalStorage for caching and preferences
- **Testing**: Jest with jsdom environment
- **Build Tools**: npm scripts for development workflow

## Getting Started

### Prerequisites
- Node.js (version 14 or higher)
- npm (comes with Node.js)
- Modern web browser

### Installation
1. Clone the repository
2. Install dependencies:
   ```bash
   cd prayercodes/devotional-pwa
   npm install
   ```

### Development
```bash
# Start local server
npm run serve

# Run tests
npm test

# Run tests with coverage
npm run test:coverage

# Run linter
npm run lint

# Watch tests during development
npm run test:watch
```

## Data Source

This application uses the [holywritings/bahaiwritings](https://www.dolthub.com/repositories/holywritings/bahaiwritings) repository on DoltHub, which contains:

- **Prayers and Writings**: Original texts in multiple languages
- **Metadata**: Names, sources, links, and Phelps codes
- **Language Support**: Display names and language codes
- **Translations**: Multiple versions of the same prayer

## Architecture

### Caching Strategy
1. **Request Cache**: API responses cached for 5 minutes
2. **Prayer Cache**: Individual prayers cached indefinitely in localStorage
3. **Background Cache**: English prayers automatically cached for search
4. **Language Cache**: Language names cached for 7 days

### Storage Keys
- `hw_prayer_cache_*`: Individual prayer cache entries
- `hw_favorite_prayers`: User's favorite prayers
- `devotionalPWA_backgroundCacheStatus`: Background caching status
- `devotionalPWA_recentLanguages`: Recently used languages
- `hw_language_names_cache`: Language display names

## Performance Features

### Background Caching
- Automatically caches English prayers after page load
- Processes prayers in batches to avoid API rate limiting
- Tracks cache status with expiration management
- Provides user feedback for significant caching operations

### Search Optimization
- Combined search across database titles and cached full text
- Prioritizes cached results for faster response times
- Pagination for large result sets
- Debounced search input for better performance

### Memory Management
- Efficient localStorage usage with size monitoring
- Cache expiration to prevent stale data
- Cleanup of unused cache entries

## Testing

Comprehensive test suite covering:
- Core prayer functionality
- Background caching system
- Error handling and edge cases
- User interface interactions
- Performance optimization

See [README-TESTING.md](README-TESTING.md) for detailed testing information.

## Browser Support

- Chrome/Chromium (recommended)
- Firefox
- Safari
- Edge
- Mobile browsers (iOS Safari, Chrome Mobile)

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests for new functionality
5. Ensure all tests pass
6. Submit a pull request

## License

MIT License - see LICENSE file for details

## Acknowledgments

- **DoltHub**: For providing the database infrastructure
- **Material Design Lite**: For the UI framework
- **Bahá'í Writings**: For the source texts and translations
- **Contributors**: Everyone who helped improve the application

---

*This application is designed to help users explore and study Bahá'í writings with modern web technologies and intelligent caching for optimal performance.*