/* eslint-env jest */
/* global marked */

// Mock the marked library for testing
const mockMarked = {
  setOptions: jest.fn(),
  parse: jest.fn((text) => {
    if (!text) return '';
    // Simple mock that mimics marked's behavior
    let result = text
      .replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>')
      .replace(/\*(.*?)\*/g, '<em>$1</em>')
      .replace(/^## (.*$)/gm, '<h2>$1</h2>')
      .replace(/^# (.*$)/gm, '<h1>$1</h1>')
      .replace(/\n/g, '<br>\n');
    return result;
  })
};

// Mock the global marked variable
global.marked = mockMarked;

describe('Markdown Rendering Tests', () => {
  // Define the renderMarkdown function inline for testing
  function renderMarkdown(text) {
    if (!text) return '';
    
    // Check if marked library is available
    if (typeof marked === 'undefined') {
      console.warn('Marked library not available, rendering text as-is with basic formatting');
      return text.replace(/\n/g, '<br>');
    }
    
    // Configure marked for prayer content
    marked.setOptions({
      breaks: true,
      gfm: true,
      sanitize: false,
      smartypants: true
    });
    
    try {
      return marked.parse(text);
    } catch (error) {
      console.error('Error parsing Markdown:', error);
      // Fallback to basic HTML formatting
      return text.replace(/\n/g, '<br>');
    }
  }

  beforeEach(() => {
    jest.clearAllMocks();
    // Reset the mock implementation to ensure consistent behavior
    mockMarked.parse.mockImplementation((text) => {
      if (!text) return '';
      // Simple mock that mimics marked's behavior
      let result = text
        .replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>')
        .replace(/\*(.*?)\*/g, '<em>$1</em>')
        .replace(/^## (.*$)/gm, '<h2>$1</h2>')
        .replace(/^# (.*$)/gm, '<h1>$1</h1>')
        .replace(/\n/g, '<br>\n');
      return result;
    });
  });

  test('should render bold text with **text**', () => {
    const input = 'This is **bold** text';
    const result = renderMarkdown(input);
    
    expect(mockMarked.setOptions).toHaveBeenCalled();
    expect(mockMarked.parse).toHaveBeenCalledWith(input);
    expect(result).toContain('<strong>bold</strong>');
  });

  test('should render italic text with *text*', () => {
    const input = 'This is *italic* text';
    const result = renderMarkdown(input);
    
    expect(mockMarked.parse).toHaveBeenCalledWith(input);
    expect(result).toContain('<em>italic</em>');
  });

  test('should render headers with ## text', () => {
    const input = '## This is a header';
    const result = renderMarkdown(input);
    
    expect(mockMarked.parse).toHaveBeenCalledWith(input);
    expect(result).toContain('<h2>This is a header</h2>');
  });

  test('should handle line breaks', () => {
    const input = 'Line 1\nLine 2';
    const result = renderMarkdown(input);
    
    expect(mockMarked.parse).toHaveBeenCalledWith(input);
    expect(result).toContain('<br>');
  });

  test('should configure marked with proper options', () => {
    const input = 'Test text';
    renderMarkdown(input);
    
    expect(mockMarked.setOptions).toHaveBeenCalledWith({
      breaks: true,
      gfm: true,
      sanitize: false,
      smartypants: true
    });
  });

  test('should handle empty input', () => {
    const input = '';
    const result = renderMarkdown(input);
    
    // Empty input returns early, so marked.parse is not called
    expect(mockMarked.parse).not.toHaveBeenCalled();
    expect(result).toBe('');
  });

  test('should handle text without markdown formatting', () => {
    const input = 'Plain text without any formatting';
    const result = renderMarkdown(input);
    
    expect(mockMarked.parse).toHaveBeenCalledWith(input);
    expect(result).toBe(input);
  });

  test('should fallback gracefully when marked is undefined', () => {
    // Temporarily undefine marked
    const originalMarked = global.marked;
    global.marked = undefined;
    
    const input = 'Test text\nwith line breaks';
    const result = renderMarkdown(input);
    
    // Should fallback to basic HTML formatting
    expect(result).toBe('Test text<br>with line breaks');
    
    // Restore marked
    global.marked = originalMarked;
  });

  test('should handle error in marked.parse gracefully', () => {
    // Mock marked.parse to throw an error
    const originalParse = mockMarked.parse;
    mockMarked.parse = jest.fn(() => {
      throw new Error('Parse error');
    });
    
    const input = 'Test text\nwith line breaks';
    const result = renderMarkdown(input);
    
    // Should fallback to basic HTML formatting
    expect(result).toBe('Test text<br>with line breaks');
    
    // Restore normal behavior
    mockMarked.parse = originalParse;
  });

  test('should handle mixed formatting', () => {
    const input = '## Header\n\nThis is **bold** and *italic* text.';
    const result = renderMarkdown(input);
    
    expect(mockMarked.parse).toHaveBeenCalledWith(input);
    expect(result).toBeDefined();
    expect(result).toContain('<h2>Header</h2>');
    expect(result).toContain('<strong>bold</strong>');
    expect(result).toContain('<em>italic</em>');
  });

  test('should handle prayer text with emphasis', () => {
    const input = '*Important prayer instruction*';
    const result = renderMarkdown(input);
    
    expect(mockMarked.parse).toHaveBeenCalledWith(input);
    expect(result).toBeDefined();
    expect(result).toContain('<em>Important prayer instruction</em>');
  });
});