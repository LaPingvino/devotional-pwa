#!/bin/bash

# Test runner script for Devotional PWA
# This script provides an easy way to run tests and set up the testing environment

set -e  # Exit on any error

echo "🙏 Holy Writings Reader - Test Runner"
echo "======================================"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if Node.js is installed
if ! command -v node &> /dev/null; then
    print_error "Node.js is not installed. Please install Node.js version 14 or higher."
    exit 1
fi

# Check Node.js version
NODE_VERSION=$(node --version | cut -d'v' -f2)
MAJOR_VERSION=$(echo $NODE_VERSION | cut -d'.' -f1)

if [ "$MAJOR_VERSION" -lt 14 ]; then
    print_error "Node.js version $NODE_VERSION is too old. Please install version 14 or higher."
    exit 1
fi

print_status "Node.js version: $NODE_VERSION ✓"

# Check if npm is installed
if ! command -v npm &> /dev/null; then
    print_error "npm is not installed. Please install npm."
    exit 1
fi

print_status "npm version: $(npm --version) ✓"

# Check if package.json exists
if [ ! -f "package.json" ]; then
    print_error "package.json not found. Please run this script from the devotional-pwa directory."
    exit 1
fi

# Install dependencies if node_modules doesn't exist
if [ ! -d "node_modules" ]; then
    print_status "Installing dependencies..."
    npm install
    if [ $? -eq 0 ]; then
        print_success "Dependencies installed successfully"
    else
        print_error "Failed to install dependencies"
        exit 1
    fi
else
    print_status "Dependencies already installed ✓"
fi

# Parse command line arguments
COMMAND=${1:-"test"}

case $COMMAND in
    "test" | "t")
        print_status "Running tests..."
        npm test
        ;;
    "watch" | "w")
        print_status "Running tests in watch mode..."
        npm run test:watch
        ;;
    "coverage" | "c")
        print_status "Running tests with coverage..."
        npm run test:coverage
        ;;
    "debug" | "d")
        print_status "Running tests in debug mode..."
        npm run test:debug
        ;;
    "lint" | "l")
        print_status "Running linter..."
        npm run lint
        ;;
    "all" | "a")
        print_status "Running all tests and linting..."
        npm test && npm run lint
        if [ $? -eq 0 ]; then
            print_success "All tests and linting passed! 🎉"
        else
            print_error "Some tests or linting failed"
            exit 1
        fi
        ;;
    "setup" | "s")
        print_status "Setting up test environment..."
        npm install --save-dev jest@^29.0.0 jest-environment-jsdom@^29.0.0 eslint@^8.0.0 fetch-mock@^9.11.0 @testing-library/jest-dom@^5.16.0 @testing-library/dom@^8.19.0
        if [ $? -eq 0 ]; then
            print_success "Test environment setup complete! 🎉"
        else
            print_error "Failed to set up test environment"
            exit 1
        fi
        ;;
    "clean")
        print_status "Cleaning test environment..."
        rm -rf node_modules package-lock.json
        print_success "Test environment cleaned"
        ;;
    "help" | "h" | "--help")
        echo ""
        echo "Usage: $0 [command]"
        echo ""
        echo "Commands:"
        echo "  test, t      Run tests once (default)"
        echo "  watch, w     Run tests in watch mode"
        echo "  coverage, c  Run tests with coverage report"
        echo "  debug, d     Run tests in debug mode"
        echo "  lint, l      Run linter"
        echo "  all, a       Run tests and linting"
        echo "  setup, s     Set up test environment"
        echo "  clean        Clean test environment"
        echo "  help, h      Show this help message"
        echo ""
        echo "Examples:"
        echo "  $0                # Run tests"
        echo "  $0 watch          # Run tests in watch mode"
        echo "  $0 coverage       # Run tests with coverage"
        echo "  $0 all            # Run everything"
        echo ""
        echo "Test Files:"
        echo "  tests/prayer-functions.test.js  - Core prayer functionality"
        echo "  tests/pinning-interface.test.js - Prayer pinning interface"
        echo ""
        echo "For more information, see README-TESTING.md"
        ;;
    *)
        print_error "Unknown command: $COMMAND"
        echo "Use '$0 help' for usage information"
        exit 1
        ;;
esac

# Final status
if [ $? -eq 0 ] && [ "$COMMAND" != "help" ] && [ "$COMMAND" != "h" ] && [ "$COMMAND" != "--help" ]; then
    echo ""
    print_success "Command completed successfully! 🙏"
    echo ""
    echo "Prayer loading performance optimizations tested:"
    echo "  ✓ Parallel API calls instead of sequential"
    echo "  ✓ Request caching and debouncing"
    echo "  ✓ Batch language name lookups"
    echo "  ✓ Improved error handling"
    echo ""
    echo "Next steps:"
    echo "  - Run '$0 watch' to continuously test during development"
    echo "  - Run '$0 coverage' to check test coverage"
    echo "  - See README-TESTING.md for more information"
fi