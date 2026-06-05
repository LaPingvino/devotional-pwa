// Ambient declarations for the cross-file globals the static/js modules
// share via window. Keep in sync when a module grows its public API.

interface HwCollectionItem {
  code: string;
  lang?: string;
  v?: string;
  title?: string;
  w?: string;
  added?: number;
}

interface HwCollection {
  id: string;
  name: string;
  builtin?: boolean;
  items: HwCollectionItem[];
  created: number;
  modified: number;
}

interface Window {
  // i18n (partials/head.html)
  __t(key: string): string;
  __i18n?: Record<string, Record<string, string>>;
  __uiLang?: string;
  __bestLang(storageKey: string, availableCodes: string[]): string;
  __translatePage?: () => void;
  __setUiLang?: (code: string) => void;
  __markUiLang?: () => void;

  // render-md.js
  renderMd(text: string): string;

  // uuid-base36.js
  uuidToBase36(uuid: string): string;
  base36ToUuid(b36: string): string | null;

  // translit.js
  __transliterate?: (text: string) => { html?: string; text?: string };
  __transliterateWithDict?: (text: string, cb: (result: { html?: string; text?: string }) => void) => void;
  __translitActive?: () => boolean;
  __addTranslitIn?: (el: Element) => void;

  // fuzzy-ar.js
  normalizeAr?: (s: string) => string;

  // prayer-list.js
  renderPrayerList(rootEl: Element, opts: any): any;

  // collections.js
  hwCollections: {
    list(): HwCollection[];
    get(id: string): HwCollection | null;
    displayName(col: HwCollection): string;
    create(name: string): HwCollection;
    rename(id: string, name: string): boolean;
    removeCollection(id: string): boolean;
    add(id: string, item: HwCollectionItem | string): boolean;
    setItems(id: string, items: HwCollectionItem[]): boolean;
    removeItem(id: string, code: string, lang?: string): boolean;
    has(id: string, code: string, lang?: string): boolean;
    isFavorite(code: string): boolean;
    toggleFavorite(item: HwCollectionItem | string): boolean;
    isRef(code: string): boolean;
    resolveText(item: HwCollectionItem): Promise<string | null>;
    toHash(items: HwCollectionItem[]): string;
    shareUrl(items: HwCollectionItem[]): string;
    parseList(text: string): HwCollectionItem[];
    exportText(col: HwCollection, defaultLang?: string): Promise<string>;
    openPicker(items: HwCollectionItem | HwCollectionItem[], onChange?: () => void): void;
  };

  // program-view.js
  hwProgramView: {
    create(cfg: {
      container: Element;
      emptyEl?: HTMLElement | null;
      getItems(): any[];
      onChange?: (items: any[]) => void;
      getLang(): string;
      getAllLanguages(): any[];
      getCompareLangs?: () => string[];
      removable?: boolean | (() => boolean);
    }): { render(): Promise<void> };
    resolveCode(code: string, lang: string): Promise<any>;
    getPrayerMap(lang: string): Promise<Record<string, any>>;
    getWritingMap(key: string, lang: string): Promise<Record<string, any>>;
    getVersionIndex(): Promise<Record<string, [string, string]>>;
    resolveVersionLocked(item: any): Promise<any>;
    parseWritingShorthand(code: string): any;
    authorFromPin(pin: string, lang: string): string;
    sigFromPin(pin: string, lang: string): string;
    formatAttribution(name: string): string;
    TRANSLIT_ENTRIES: { code: string; name: string; nameLC: string }[];
    isTranslitCode(c: string): boolean;
  };

  // misc page globals
  _writingsIndex?: any[];
  fuzzySearch?: (...args: any[]) => any;
}
