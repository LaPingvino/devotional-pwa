/**
 * @typedef {object} ViewSpec
 * @property {string} titleKey - Localization key for the page title.
 * @property {function(): (HTMLElement | DocumentFragment | string | void)} contentRenderer - Callback function to render view-specific content. It can return an HTML element, a document fragment, a string of HTML, or nothing if it directly manipulates the DOM.
 * @property {boolean} [showLanguageSwitcher=true] - Whether to display the language switcher. Defaults to true.
 * @property {boolean} [showBackButton=false] - Whether to display a back button in the header. Defaults to false.
 * @property {function(): (HTMLElement | DocumentFragment | string)} [customHeaderContentRenderer] - Optional callback to render custom content in the header, replacing the default title and back button.
 * @property {string | null} [activeLangCodeForPicker=null] - Optional language code to set as active in the language picker. Defaults to null.
 */

// This file defines the ViewSpec type for JSDoc.
// No actual runtime JavaScript code is exported from this file.
// It helps in documenting and understanding the structure of the
// object expected by the renderPageLayout function.