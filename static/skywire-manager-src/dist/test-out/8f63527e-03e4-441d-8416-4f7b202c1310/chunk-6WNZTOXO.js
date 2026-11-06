import {
  ChangeDetectorRef,
  DestroyRef,
  Directive,
  EMPTY,
  ElementRef,
  FactoryTarget,
  Injectable,
  InjectionToken,
  Injector,
  Input,
  Observable,
  Pipe,
  Subject,
  TemplateRef,
  ViewContainerRef,
  assertInInjectionContext,
  computed,
  concat,
  concatMap,
  core_exports,
  defer,
  effect,
  filter,
  finalize,
  forkJoin,
  inject,
  isObservable,
  map,
  merge,
  of,
  shareReplay,
  signal,
  switchMap,
  take,
  takeUntil,
  tap,
  untracked,
  ɵɵngDeclareClassMetadata,
  ɵɵngDeclareDirective,
  ɵɵngDeclareFactory,
  ɵɵngDeclareInjectable,
  ɵɵngDeclarePipe
} from "./chunk-ZNLGPAUZ.js";
import {
  __objRest,
  __restKey,
  __spreadProps,
  __spreadValues
} from "./chunk-ARXMZVRT.js";

// node_modules/@angular/core/fesm2022/rxjs-interop.mjs
/**
 * @license Angular v22.2.1
 * (c) 2010-2026 Google LLC. https://angular.dev/
 * License: MIT
 */
function takeUntilDestroyed(destroyRef) {
  if (!destroyRef) {
    ngDevMode && assertInInjectionContext(takeUntilDestroyed);
    destroyRef = inject(DestroyRef);
  }
  const destroyed$ = new Observable((subscriber) => {
    if (destroyRef.destroyed) {
      subscriber.next();
      return;
    }
    const unregisterFn = destroyRef.onDestroy(subscriber.next.bind(subscriber));
    return unregisterFn;
  });
  return (source) => {
    return source.pipe(takeUntil(destroyed$));
  };
}

// node_modules/@ngx-translate/core/fesm2022/ngx-translate-core.mjs
function omit(map2, key) {
  const next = {};
  for (const k of Object.keys(map2)) {
    if (k !== key)
      next[k] = map2[k];
  }
  return next;
}
var LoadingTranslationsRegistry = class {
  state = signal(
    {},
    ...ngDevMode ? [{ debugName: "state" }] : (
      /* istanbul ignore next */
      []
    )
  );
  /** Reactive — `true` while at least one load is in flight. */
  hasAny = computed(
    () => Object.keys(this.state()).length > 0,
    ...ngDevMode ? [{ debugName: "hasAny" }] : (
      /* istanbul ignore next */
      []
    )
  );
  /** `true` while THIS language is being loaded on this instance. */
  isLoading(lang) {
    return this.state()[lang] !== void 0;
  }
  get(lang) {
    return this.state()[lang];
  }
  set(lang, obs) {
    this.state.update((m) => __spreadProps(__spreadValues({}, m), { [lang]: obs }));
  }
  /** Unconditional clear. Used by `resetLang` to forcibly drop the entry. */
  clear(lang) {
    this.state.update((m) => omit(m, lang));
  }
  /**
   * Token-aware clear. Used by `loadAndCompileTranslations`'s `finalize` so
   * an old load's `finalize` cannot clobber a newer load's entry.
   */
  clearIfOwner(lang, token) {
    this.state.update((m) => m[lang] === token ? omit(m, lang) : m);
  }
};
var MissingTranslationHandler = class {
};
var DefaultMissingTranslationHandler = class _DefaultMissingTranslationHandler {
  handle(params) {
    return params.key;
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _DefaultMissingTranslationHandler, deps: [], target: FactoryTarget.Injectable });
  static \u0275prov = \u0275\u0275ngDeclareInjectable({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _DefaultMissingTranslationHandler });
};
\u0275\u0275ngDeclareClassMetadata({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: DefaultMissingTranslationHandler, decorators: [{
  type: Injectable
}] });
var TranslateCompiler = class {
};
var TranslateNoOpCompiler = class _TranslateNoOpCompiler extends TranslateCompiler {
  compile(value, lang) {
    void lang;
    return value;
  }
  compileTranslations(translations, lang) {
    void lang;
    return translations;
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _TranslateNoOpCompiler, deps: null, target: FactoryTarget.Injectable });
  static \u0275prov = \u0275\u0275ngDeclareInjectable({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _TranslateNoOpCompiler });
};
\u0275\u0275ngDeclareClassMetadata({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: TranslateNoOpCompiler, decorators: [{
  type: Injectable
}] });
var TranslateLoader = class {
};
var TranslateNoOpLoader = class _TranslateNoOpLoader extends TranslateLoader {
  getTranslation(lang) {
    void lang;
    return of({});
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _TranslateNoOpLoader, deps: null, target: FactoryTarget.Injectable });
  static \u0275prov = \u0275\u0275ngDeclareInjectable({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _TranslateNoOpLoader });
};
\u0275\u0275ngDeclareClassMetadata({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: TranslateNoOpLoader, decorators: [{
  type: Injectable
}] });
function equals(o1, o2) {
  if (o1 === o2)
    return true;
  if (o1 === null || o2 === null)
    return false;
  if (o1 !== o1 && o2 !== o2)
    return true;
  const t1 = typeof o1, t2 = typeof o2;
  let length;
  if (t1 == t2 && t1 == "object") {
    if (Array.isArray(o1)) {
      if (!Array.isArray(o2))
        return false;
      if ((length = o1.length) == o2.length) {
        for (let key = 0; key < length; key++) {
          if (!equals(o1[key], o2[key]))
            return false;
        }
        return true;
      }
    } else {
      if (Array.isArray(o2)) {
        return false;
      }
      if (isDict(o1) && isDict(o2)) {
        const keySet = /* @__PURE__ */ Object.create(null);
        for (const key in o1) {
          if (!equals(o1[key], o2[key])) {
            return false;
          }
          keySet[key] = true;
        }
        for (const key in o2) {
          if (!(key in keySet) && typeof o2[key] !== "undefined") {
            return false;
          }
        }
        return true;
      }
    }
  }
  return false;
}
function isDefinedAndNotNull(value) {
  return typeof value !== "undefined" && value !== null;
}
function isDefined(value) {
  return value !== void 0;
}
function isDict(value) {
  return isObject(value) && !isArray(value) && value !== null;
}
function isObject(value) {
  return typeof value === "object" && value !== null;
}
function isArray(value) {
  return Array.isArray(value);
}
function isString(value) {
  return typeof value === "string";
}
function isFunction(value) {
  return typeof value === "function";
}
function cloneDeep(value) {
  if (isArray(value)) {
    return value.map((item) => cloneDeep(item));
  } else if (isDict(value)) {
    const cloned = {};
    Object.keys(value).forEach((key) => {
      cloned[key] = cloneDeep(value[key]);
    });
    return cloned;
  } else {
    return value;
  }
}
function mergeDeep(target, source) {
  if (!isObject(target)) {
    return cloneDeep(source);
  }
  const output = cloneDeep(target);
  if (isObject(output) && isObject(source)) {
    Object.keys(source).forEach((key) => {
      if (isDict(source[key])) {
        if (key in target) {
          output[key] = mergeDeep(target[key], source[key]);
        } else {
          Object.assign(output, { [key]: source[key] });
        }
      } else {
        Object.assign(output, { [key]: source[key] });
      }
    });
  }
  return output;
}
function getValue(target, key) {
  const keys = key.split(".");
  key = "";
  do {
    key += keys.shift();
    const isLastKey = !keys.length;
    if (isDefinedAndNotNull(target)) {
      if (isDict(target) && isDefined(target[key]) && (isDict(target[key]) || isArray(target[key]) || isLastKey)) {
        target = target[key];
        key = "";
        continue;
      }
      if (isArray(target)) {
        if (key === "length" && isLastKey) {
          target = target.length;
          key = "";
          continue;
        }
        if (/^\d+$/.test(key)) {
          const index = parseInt(key, 10);
          if (isDefined(target[index]) && (isDict(target[index]) || isArray(target[index]) || isLastKey)) {
            target = target[index];
            key = "";
            continue;
          }
        }
      }
    }
    if (isLastKey) {
      target = void 0;
      continue;
    }
    key += ".";
  } while (keys.length);
  return target;
}
function insertValue(target, key, value) {
  return mergeDeep(target, createNestedObject(key, value));
}
function createNestedObject(dotSeparatedKey, value) {
  return dotSeparatedKey.split(".").reduceRight((acc, key) => ({ [key]: acc }), value);
}
var TranslateParser = class {
};
var TranslateDefaultParser = class _TranslateDefaultParser extends TranslateParser {
  templateMatcher = /{{\s?([^{}\s]*)\s?}}/g;
  interpolate(expr, params) {
    if (isString(expr)) {
      return this.interpolateString(expr, params);
    } else if (isFunction(expr)) {
      return this.interpolateFunction(expr, params);
    }
    return void 0;
  }
  interpolateFunction(fn, params) {
    return fn(params);
  }
  interpolateString(expr, params) {
    if (!params) {
      return expr;
    }
    return expr.replace(this.templateMatcher, (substring, key) => {
      const replacement = this.getInterpolationReplacement(params, key);
      return replacement !== void 0 ? replacement : substring;
    });
  }
  /**
   * Returns the replacement for an interpolation parameter
   * @params:
   */
  getInterpolationReplacement(params, key) {
    return this.formatValue(getValue(params, key));
  }
  /**
   * Converts a value into a useful string representation.
   * @param value The value to format.
   * @returns A string representation of the value.
   */
  formatValue(value) {
    if (isString(value)) {
      return value;
    }
    if (typeof value === "number" || typeof value === "boolean") {
      return value.toString();
    }
    if (value === null) {
      return "null";
    }
    if (isArray(value)) {
      return value.join(", ");
    }
    if (isObject(value)) {
      if (typeof value.toString === "function" && value.toString !== Object.prototype.toString) {
        return value.toString();
      }
      return JSON.stringify(value);
    }
    return void 0;
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _TranslateDefaultParser, deps: null, target: FactoryTarget.Injectable });
  static \u0275prov = \u0275\u0275ngDeclareInjectable({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _TranslateDefaultParser });
};
\u0275\u0275ngDeclareClassMetadata({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: TranslateDefaultParser, decorators: [{
  type: Injectable
}] });
var TranslateStore = class _TranslateStore {
  _translations = signal(
    {},
    ...ngDevMode ? [{ debugName: "_translations" }] : (
      /* istanbul ignore next */
      []
    )
  );
  translations = this._translations.asReadonly();
  _languages = signal(
    [],
    ...ngDevMode ? [{ debugName: "_languages" }] : (
      /* istanbul ignore next */
      []
    )
  );
  languages = this._languages.asReadonly();
  _lastTranslationChange = signal(
    null,
    ...ngDevMode ? [{ debugName: "_lastTranslationChange" }] : (
      /* istanbul ignore next */
      []
    )
  );
  lastTranslationChange = this._lastTranslationChange.asReadonly();
  _translationChange$ = new Subject();
  translationChange$ = this._translationChange$.asObservable();
  constructor() {
    inject(DestroyRef).onDestroy(() => {
      this._translationChange$.complete();
    });
  }
  getTranslations(language) {
    return this.translations()[language];
  }
  setTranslations(language, translations, extend) {
    this._translations.update((current) => __spreadProps(__spreadValues({}, current), {
      [language]: extend && this.hasTranslationFor(language) ? mergeDeep(current[language], translations) : translations
    }));
    this.addLanguages([language]);
    const event = {
      lang: language,
      translations: this.getTranslations(language)
    };
    this._lastTranslationChange.set(event);
    this._translationChange$.next(event);
  }
  getLanguages() {
    return this.languages();
  }
  addLanguages(langs) {
    this._languages.update((current) => Array.from(/* @__PURE__ */ new Set([...current, ...langs])));
  }
  hasTranslationFor(lang) {
    return typeof this.translations()[lang] !== "undefined";
  }
  deleteTranslations(lang) {
    this._translations.update((current) => {
      const _a = current, { [lang]: _ } = _a, rest = __objRest(_a, [__restKey(lang)]);
      return rest;
    });
  }
  getTranslationValue(language, key) {
    return getValue(this.getTranslations(language), key);
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _TranslateStore, deps: [], target: FactoryTarget.Injectable });
  static \u0275prov = \u0275\u0275ngDeclareInjectable({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _TranslateStore });
};
\u0275\u0275ngDeclareClassMetadata({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: TranslateStore, decorators: [{
  type: Injectable
}], ctorParameters: () => [] });
var TRANSLATE_SERVICE_CONFIG = new InjectionToken("TRANSLATE_CONFIG");
var makeObservable = (value) => {
  return isObservable(value) ? value : of(value);
};
var TranslateService = class _TranslateService {
  loadingTranslations = new LoadingTranslationsRegistry();
  lastUseLanguage = null;
  currentLoader = inject(TranslateLoader);
  compiler = inject(TranslateCompiler);
  parser = inject(TranslateParser);
  missingTranslationHandler = inject(MissingTranslationHandler);
  store = inject(TranslateStore);
  destroyRef = inject(DestroyRef);
  parent;
  get isRoot() {
    return this.parent === null;
  }
  _onLangChange = new Subject();
  _onFallbackLangChange = new Subject();
  _currentLang = signal(
    null,
    ...ngDevMode ? [{ debugName: "_currentLang" }] : (
      /* istanbul ignore next */
      []
    )
  );
  _fallbackLang = signal(
    null,
    ...ngDevMode ? [{ debugName: "_fallbackLang" }] : (
      /* istanbul ignore next */
      []
    )
  );
  _onTranslationRefresh = null;
  // Downward-inheritance: `true` if THIS service has loads in flight, OR any
  // ancestor does. Walks the parent chain via the public `parent.isLoading()`
  // getter (not by reaching into `parent.loadingTranslations`) so the
  // encapsulation boundary holds. Angular signals re-collect dependencies on
  // each evaluation; short-circuit is safe — the only signal whose flip
  // could change the result is the one returned by short-circuit, and it IS
  // tracked. Parent chain is acyclic (DI tree is acyclic; `skipSelf` only
  // walks up), so no cycle guard needed.
  //
  // Set-based composition at each level: `loadingTranslations.hasAny()`
  // stays true while ANY language has an in-flight load on this service,
  // flips false only when the last entry is cleared.
  _isLoading = computed(
    () => this.loadingTranslations.hasAny() || (this.parent?.isLoading() ?? false),
    ...ngDevMode ? [{ debugName: "_isLoading" }] : (
      /* istanbul ignore next */
      []
    )
  );
  /**
   * Returns the root of this service's hierarchy — the topmost service in
   * the `getParent()` chain. For an isolated subtree, returns the subtree's
   * root (since `parent === null` at the isolation boundary).
   *
   * A root service returns itself. Equivalent to walking `getParent()` until
   * it returns `null`, but provided as a convenience.
   */
  getRoot() {
    let svc = this;
    while (svc.parent)
      svc = svc.parent;
    return svc;
  }
  /**
   * Returns the service this one inherits translations from, or `null` if
   * this is a root (a top-level service or an isolated subtree root).
   *
   * A `null` return means the service is the terminus of its translation
   * fallback chain — equivalent to "is this a root?".
   */
  getParent() {
    return this.parent;
  }
  /**
   * The language most recently requested via `use()`. Always read from the
   * root, because `use()` delegates to the root (`parent!.use(lang)`) and only
   * the root ever assigns `lastUseLanguage`. A child's own `lastUseLanguage`
   * stays `null`, so reading it directly would make `get()` miss the child's
   * in-flight load. `null` until the first `use()` runs.
   */
  getActiveRequestedLang() {
    return this.getRoot().lastUseLanguage;
  }
  hasTranslationInChain(lang) {
    for (let svc = this; svc; svc = svc.parent) {
      if (svc.store.hasTranslationFor(lang))
        return true;
    }
    return false;
  }
  chainTranslationChange$() {
    const streams = [];
    for (let svc = this; svc; svc = svc.parent) {
      streams.push(svc.store.translationChange$);
    }
    return streams.length === 1 ? streams[0] : merge(...streams);
  }
  /**
   * An Observable to listen to translation change events
   * onTranslationChange.subscribe((params: TranslationChangeEvent) => {
   *     // do something
   * });
   */
  get onTranslationChange() {
    return this.store.translationChange$;
  }
  /**
   * An Observable to listen to lang change events
   * onLangChange.subscribe((params: LangChangeEvent) => {
   *     // do something
   * });
   */
  get onLangChange() {
    if (this.isRoot) {
      return this._onLangChange.asObservable();
    }
    return this.parent ? this.parent.onLangChange : EMPTY;
  }
  /**
   * An Observable to listen to fallback lang change events
   * onFallbackLangChange.subscribe((params: FallbackLangChangeEvent) => {
   *     // do something
   * });
   */
  get onFallbackLangChange() {
    if (this.isRoot) {
      return this._onFallbackLangChange.asObservable();
    }
    return this.parent ? this.parent.onFallbackLangChange : EMPTY;
  }
  /**
   * A combined Observable that emits whenever translations might need to be refreshed.
   * This includes: language changes, translation updates for the current or fallback language,
   * and fallback language changes.
   */
  get onTranslationRefresh() {
    if (!this._onTranslationRefresh) {
      const refresh$ = merge(this.onTranslationChange.pipe(filter((event) => event.lang === this.getCurrentLang() || event.lang === this.getFallbackLang())), this.onLangChange, this.onFallbackLangChange).pipe(map(() => void 0));
      if (this.isRoot) {
        this._onTranslationRefresh = refresh$;
      } else {
        this._onTranslationRefresh = this.parent ? merge(refresh$, this.parent.onTranslationRefresh) : refresh$;
      }
    }
    return this._onTranslationRefresh;
  }
  constructor() {
    const config = __spreadValues({
      isRoot: true,
      fallbackLang: null
    }, inject(TRANSLATE_SERVICE_CONFIG, {
      optional: true
    }));
    this.parent = config.isRoot ? null : inject(_TranslateService, { optional: true, skipSelf: true });
    const destroyRef = inject(DestroyRef);
    if (this.isRoot) {
      if (config.lang) {
        this.use(config.lang);
      }
      if (config.fallbackLang) {
        this.setFallbackLang(config.fallbackLang);
      }
    } else {
      const currentLang = this.getCurrentLang();
      if (currentLang) {
        this.loadOrExtendLanguage(currentLang)?.pipe(takeUntilDestroyed(destroyRef)).subscribe({
          error: (err) => {
            console.warn(`@ngx-translate/core: child failed to load "${currentLang}". Cause:`, err);
          }
        });
      }
      const fallbackLang = this.getFallbackLang();
      if (fallbackLang && fallbackLang !== currentLang) {
        this.loadOrExtendLanguage(fallbackLang)?.pipe(takeUntilDestroyed(destroyRef)).subscribe({
          error: (err) => {
            console.warn(`@ngx-translate/core: child failed to load "${fallbackLang}". Cause:`, err);
          }
        });
      }
    }
    this.onLangChange.pipe(takeUntilDestroyed(destroyRef)).subscribe((event) => {
      if (!this.isRoot) {
        this.loadOrExtendLanguage(event.lang)?.pipe(takeUntilDestroyed(destroyRef)).subscribe({
          error: (err) => {
            console.warn(`@ngx-translate/core: child failed to load "${event.lang}". Cause:`, err);
          }
        });
      }
    });
    this.onFallbackLangChange.pipe(takeUntilDestroyed(destroyRef)).subscribe((event) => {
      if (!this.isRoot) {
        this.loadOrExtendLanguage(event.lang)?.pipe(takeUntilDestroyed(destroyRef)).subscribe({
          error: (err) => {
            console.warn(`@ngx-translate/core: child failed to load "${event.lang}". Cause:`, err);
          }
        });
      }
    });
    destroyRef.onDestroy(() => {
      this._onLangChange.complete();
      this._onFallbackLangChange.complete();
    });
  }
  /**
   * Sets the fallback language to use if a translation is not found in the
   * current language
   */
  setFallbackLang(lang) {
    if (!this.isRoot) {
      return this.parent.setFallbackLang(lang);
    }
    if (!this._fallbackLang()) {
      this._fallbackLang.set(lang);
    }
    const pending = this.loadOrExtendLanguage(lang);
    if (isObservable(pending)) {
      pending.pipe(take(1)).subscribe({
        next: () => {
          this._fallbackLang.set(lang);
          this._onFallbackLangChange.next({
            lang,
            translations: this.store.getTranslations(lang)
          });
        },
        error: (err) => {
          console.warn(`@ngx-translate/core: failed to load fallback "${lang}". Cause:`, err);
        }
      });
      return pending;
    }
    this._fallbackLang.set(lang);
    this._onFallbackLangChange.next({
      lang,
      translations: this.store.getTranslations(lang)
    });
    return of(this.store.getTranslations(lang));
  }
  /**
   * Signal that is `true` while one or more language loads are in flight at
   * this service or any of its ancestors in the service hierarchy.
   *
   * Loading scope propagates DOWNWARD: a load triggered at the root marks
   * the root and all descendants as loading. A load triggered at a child
   * (e.g. a lazy-route bootstrap fetching its translations) marks only that
   * child's subtree. Siblings and ancestors are unaffected by a descendant's
   * loads.
   *
   * Drive a spinner by reading it from the service injected at the scope
   * where the spinner should live: root for an app-shell spinner, the
   * nearest child for a local spinner inside a lazy-loaded subtree.
   */
  get isLoading() {
    return this._isLoading;
  }
  /**
   * Changes the lang currently used
   */
  use(lang) {
    if (!this.isRoot) {
      return this.parent.use(lang);
    }
    const prevLang = this._currentLang();
    const prevLastUseLang = this.lastUseLanguage;
    this.lastUseLanguage = lang;
    if (!this._currentLang()) {
      this._currentLang.set(lang);
    }
    const pending = this.loadOrExtendLanguage(lang);
    if (!isObservable(pending)) {
      this.changeLang(lang);
      return of(this.store.getTranslations(lang));
    }
    pending.pipe(take(1)).subscribe({
      next: () => {
        this.changeLang(lang);
      },
      error: (err) => {
        if (this.lastUseLanguage === lang) {
          this._currentLang.set(prevLang);
          this.lastUseLanguage = prevLastUseLang;
        }
        console.warn(`@ngx-translate/core: failed to load "${lang}". currentLang was NOT changed; remains "${prevLang ?? "null"}". Cause:`, err);
      }
    });
    return pending;
  }
  /**
   * Retrieves the given translations
   */
  loadOrExtendLanguage(lang) {
    if (!this.store.hasTranslationFor(lang)) {
      return this.loadAndCompileTranslations(lang);
    }
    return of(this.store.getTranslations(lang));
  }
  /**
   * @returns The loaded translations for the given language
   */
  getTranslations(language) {
    return this.store.getTranslations(language);
  }
  /**
   * Changes the current lang
   */
  changeLang(lang) {
    if (lang !== this.lastUseLanguage) {
      return;
    }
    this._currentLang.set(lang);
    this._onLangChange.next({ lang, translations: this.store.getTranslations(lang) });
  }
  getCurrentLang() {
    return this.isRoot ? this._currentLang() : this.parent?.getCurrentLang() ?? null;
  }
  /**
   * Loads translations for `lang` via the configured `TranslateLoader`,
   * compiles them, and stores the result. Tracking via the protected
   * `loadingTranslations` registry happens automatically.
   *
   * Subclasses that override this method bypass `isLoading` tracking
   * unless they call `this.loadingTranslations.set(lang, obs)` and arrange
   * a token-aware finalize (`this.loadingTranslations.clearIfOwner(lang, obs)`)
   * from the override.
   */
  loadAndCompileTranslations(lang) {
    const existing = this.loadingTranslations.get(lang);
    if (existing) {
      return existing;
    }
    const translations$ = this.currentLoader.getTranslation(lang).pipe(
      map((res) => this.compiler.compileTranslations(res, lang)),
      tap((compiled) => {
        this.store.setTranslations(lang, compiled, false);
        this.loadingTranslations.clearIfOwner(lang, translations$);
      }),
      // Token-aware clear: if `resetLang` + `reloadLang` raced between
      // set() and finalize(), this load's finalize must NOT clobber the
      // newer load's entry. clearIfOwner compares by reference identity.
      finalize(() => this.loadingTranslations.clearIfOwner(lang, translations$)),
      // cache the single result & share it across all subscribers
      shareReplay({ bufferSize: 1, refCount: true })
    );
    this.loadingTranslations.set(lang, translations$);
    translations$.pipe(takeUntilDestroyed(this.destroyRef)).subscribe({
      // eslint-disable-next-line @typescript-eslint/no-empty-function
      error: () => {
      }
    });
    return translations$;
  }
  /**
   * Manually sets an object of translations for a given language,
   * passing it through the configured {@link TranslateCompiler} first.
   *
   * If you already have translations in their final compiled form
   * (e.g. interpolation functions produced at build time), use
   * {@link setCompiledTranslation} instead — it stores the data
   * directly and skips the compiler.
   */
  setTranslation(lang, translations, shouldMerge = false) {
    const interpolatableTranslations = this.compiler.compileTranslations(translations, lang);
    this.store.setTranslations(lang, interpolatableTranslations, shouldMerge);
  }
  /**
   * Stores an already-compiled translation object for the given language,
   * bypassing the configured {@link TranslateCompiler}.
   *
   * Use this when you have translations in their final, interpolator-ready
   * form — e.g. interpolation functions produced at build time. For raw
   * translations that still need to go through the compiler, use
   * {@link setTranslation} instead.
   */
  setCompiledTranslation(lang, translations, shouldMerge = false) {
    this.store.setTranslations(lang, translations, shouldMerge);
  }
  getLangs() {
    return this.store.getLanguages();
  }
  /**
   * Add available languages
   */
  addLangs(languages) {
    this.store.addLanguages(languages);
  }
  getParsedResultForKey(key, interpolateParams, lang) {
    const textToInterpolate = this.getTextToInterpolate(key, lang);
    if (isDefinedAndNotNull(textToInterpolate)) {
      return this.runInterpolation(textToInterpolate, interpolateParams);
    }
    const handler = this.getMissingTranslationHandler();
    const res = handler.handle(__spreadValues({
      key,
      translateService: this
    }, interpolateParams !== void 0 && { interpolateParams }));
    return res !== void 0 ? res : key;
  }
  getMissingTranslationHandler() {
    return this.missingTranslationHandler;
  }
  /**
   * Gets the fallback language. null if none is defined
   */
  getFallbackLang() {
    return this.isRoot ? this._fallbackLang() : this.parent?.getFallbackLang() ?? null;
  }
  getTextToInterpolate(key, lang) {
    if (lang) {
      const res2 = this.store.getTranslationValue(lang, key);
      if (res2 !== void 0) {
        return res2;
      }
      return this.parent?.getTextToInterpolate(key, lang);
    }
    const currentLang = this.getCurrentLang();
    const fallbackLang = this.getFallbackLang();
    let res;
    if (currentLang) {
      res = this.store.getTranslationValue(currentLang, key);
    }
    if (!isDefinedAndNotNull(res) && fallbackLang && fallbackLang !== currentLang) {
      res = this.store.getTranslationValue(fallbackLang, key);
    }
    if (res !== void 0) {
      return res;
    }
    return this.parent?.getTextToInterpolate(key);
  }
  runInterpolation(translations, interpolateParams) {
    if (!isDefinedAndNotNull(translations)) {
      return;
    }
    if (isArray(translations)) {
      return this.runInterpolationOnArray(translations, interpolateParams);
    }
    if (isDict(translations)) {
      return this.runInterpolationOnDict(translations, interpolateParams);
    }
    return this.parser.interpolate(translations, interpolateParams);
  }
  runInterpolationOnArray(translations, interpolateParams) {
    return translations.map((translation) => this.runInterpolation(translation, interpolateParams));
  }
  runInterpolationOnDict(translations, interpolateParams) {
    const result = {};
    for (const key in translations) {
      const res = this.runInterpolation(translations[key], interpolateParams);
      if (res !== void 0) {
        result[key] = res;
      }
    }
    return result;
  }
  /**
   * Returns the parsed result of the translations
   */
  getParsedResult(key, interpolateParams, lang) {
    return key instanceof Array ? this.getParsedResultForArray(key, interpolateParams, lang) : this.getParsedResultForKey(key, interpolateParams, lang);
  }
  getParsedResultForArray(key, interpolateParams, lang) {
    const result = {};
    let observables = false;
    for (const k of key) {
      result[k] = this.getParsedResultForKey(k, interpolateParams, lang);
      observables = observables || isObservable(result[k]);
    }
    if (!observables) {
      return result;
    }
    const sources = key.map((k) => makeObservable(result[k]));
    return forkJoin(sources).pipe(map((arr) => {
      const obj = {};
      arr.forEach((value, index) => {
        obj[key[index]] = value;
      });
      return obj;
    }));
  }
  /**
   * Gets the translated value of a key (or an array of keys)
   * @returns the translated key, or an object of translated keys
   */
  get(key, interpolateParams, lang) {
    if (!isDefinedAndNotNull(key) || !key.length) {
      return of("");
    }
    const effectiveLang = lang ?? this.getActiveRequestedLang() ?? this.getCurrentLang();
    const pending = effectiveLang ? this.loadingTranslations.get(effectiveLang) : void 0;
    if (pending) {
      return pending.pipe(concatMap(() => {
        return makeObservable(this.getParsedResult(key, interpolateParams, lang));
      }));
    }
    return makeObservable(this.getParsedResult(key, interpolateParams, lang));
  }
  /**
   * Returns a stream of translated values of a key (or an array of keys) which updates
   * whenever the translation changes.
   * @returns A stream of the translated key, or an object of translated keys
   */
  getStreamOnTranslationChange(key, interpolateParams, lang) {
    if (!isDefinedAndNotNull(key) || !key.length) {
      throw new Error(`Parameter "key" is required and cannot be empty`);
    }
    return concat(defer(() => this.get(key, interpolateParams, lang)), this.onTranslationChange.pipe(switchMap(() => {
      const res = this.getParsedResult(key, interpolateParams, lang);
      return makeObservable(res);
    })));
  }
  /**
   * Returns a stream of translated values of a key (or an array of keys) which updates
   * whenever the language changes, the requested language's translations are
   * (re)loaded, or the explicitly-requested `lang` argument's translations change.
   *
   * Without `lang`: re-emits on `onLangChange` (active-language switches via
   * {@link use}). With `lang`: also re-emits when translations for that specific
   * language are loaded or updated via `store.translationChange$`, so an
   * explicit `stream("KEY", undefined, "de")` updates once "de" finishes
   * loading.
   *
   * @returns A stream of the translated key, or an object of translated keys
   */
  stream(key, interpolateParams, lang) {
    if (!isDefinedAndNotNull(key) || !key.length) {
      throw new Error(`Parameter "key" required`);
    }
    const reemit$ = lang ? merge(this.onLangChange, this.chainTranslationChange$().pipe(filter((e) => e.lang === lang))) : this.onLangChange;
    return concat(defer(() => this.get(key, interpolateParams, lang)), reemit$.pipe(switchMap(() => {
      const res = this.getParsedResult(key, interpolateParams, lang);
      return makeObservable(res);
    })));
  }
  /**
   * Returns a translation instantly from the internal state of loaded translation.
   * All rules regarding the current language, the preferred language of even fallback languages
   * will be used except any promise handling.
   *
   * When `lang` is provided, the lookup goes directly to the specified language,
   * bypassing the current language and fallback chain.
   */
  instant(key, interpolateParams, lang) {
    if (!isDefinedAndNotNull(key) || key.length === 0) {
      return "";
    }
    if (lang && !this.hasTranslationInChain(lang)) {
      this.warnUnloadedInstantLang(lang);
    }
    const result = this.getParsedResult(key, interpolateParams, lang);
    return isObservable(result) ? this.keyToObject(key) : result;
  }
  warnedUnloadedInstantLangs = /* @__PURE__ */ new Set();
  warnUnloadedInstantLang(lang) {
    const root = this.getRoot();
    if (root !== this) {
      root.warnUnloadedInstantLang(lang);
      return;
    }
    if (this.warnedUnloadedInstantLangs.has(lang))
      return;
    untracked(() => {
      this.warnedUnloadedInstantLangs.add(lang);
      console.warn(`@ngx-translate/core: instant() called with lang="${lang}" but no translations are loaded for that language. Returning the key as fallback. Load with use("${lang}") or setTranslation("${lang}", ...) first.`);
    });
  }
  /**
   * Returns a Signal that provides the translated value and automatically
   * updates when the language changes or translations are reloaded.
   *
   * Parameters accept plain values or arrow functions. Signal reads inside
   * the function are tracked reactively. Signals themselves are also
   * accepted directly, since Signal<T> is callable.
   *
   * @param key The translation key (or array of keys), a function returning one
   * @param params Optional interpolation parameters, or a function returning them
   * @param lang Optional language override, or a function returning one
   * @returns A Signal that emits the translated value(s)
   *
   * @example
   * // Static key
   * greeting = this.translate.translate('HELLO');
   *
   * @example
   * // Derived key from another signal (no separate computed needed)
   * model = signal({ currentKey: 'HELLO' });
   * greeting = this.translate.translate(() => this.model().currentKey);
   *
   * @example
   * // Multi-key lookup
   * labels = this.translate.translate(['SAVE', 'CANCEL']);
   */
  translate(key, params, lang) {
    return computed(() => {
      const currentKey = typeof key === "function" ? key() : key;
      const currentParams = typeof params === "function" ? params() : params;
      const currentLang = typeof lang === "function" ? lang() : lang;
      return this.instant(currentKey, currentParams, currentLang);
    });
  }
  keyToObject(key) {
    if (Array.isArray(key)) {
      return key.reduce((acc, currKey) => {
        acc[currKey] = currKey;
        return acc;
      }, {});
    }
    return key;
  }
  /**
   * Sets the translated value of a key, after compiling it
   */
  set(key, translation, lang = this.getCurrentLang()) {
    this.store.setTranslations(lang, insertValue(this.store.getTranslations(lang), key, isString(translation) ? this.compiler.compile(translation, lang) : this.compiler.compileTranslations(translation, lang)), false);
  }
  /**
   * Allows reloading the lang file from the file
   */
  reloadLang(lang) {
    this.resetLang(lang);
    return this.loadAndCompileTranslations(lang);
  }
  /**
   * Deletes stored translations for `lang` and clears the in-flight registry
   * entry — `isLoading()` flips to `false` immediately on this service.
   *
   * Does NOT cancel the underlying network call: if the loader is mid-flight
   * when this method returns, the request can still complete and `tap()`
   * translations back into the store. To replace state and re-fetch
   * deterministically, follow with `reloadLang(lang)`.
   */
  resetLang(lang) {
    this.loadingTranslations.clear(lang);
    this.store.deleteTranslations(lang);
  }
  /**
   * Returns the language code name from the browser, e.g. "de"
   */
  static getBrowserLang() {
    if (typeof window === "undefined" || !window.navigator) {
      return void 0;
    }
    const browserLang = this.getBrowserCultureLang();
    return browserLang ? browserLang.split(/[-_]/)[0] : void 0;
  }
  /**
   * Returns the culture language code name from the browser, e.g. "de-DE"
   */
  static getBrowserCultureLang() {
    if (typeof window === "undefined" || typeof window.navigator === "undefined") {
      return void 0;
    }
    return window.navigator.languages ? window.navigator.languages[0] : window.navigator.language || window.navigator.browserLanguage || window.navigator.userLanguage;
  }
  getBrowserLang() {
    return _TranslateService.getBrowserLang();
  }
  getBrowserCultureLang() {
    return _TranslateService.getBrowserCultureLang();
  }
  /**
   * The current language as a reactive Signal.
   * Use `getCurrentLang()` for a non-reactive snapshot.
   */
  get currentLang() {
    return this.isRoot ? this._currentLang.asReadonly() : this.parent.currentLang;
  }
  /**
   * The fallback language as a reactive Signal.
   * Use `getFallbackLang()` for a non-reactive snapshot.
   */
  get fallbackLang() {
    return this.isRoot ? this._fallbackLang.asReadonly() : this.parent.fallbackLang;
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _TranslateService, deps: [], target: FactoryTarget.Injectable });
  static \u0275prov = \u0275\u0275ngDeclareInjectable({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _TranslateService });
};
\u0275\u0275ngDeclareClassMetadata({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: TranslateService, decorators: [{
  type: Injectable
}], ctorParameters: () => [] });
var TranslateBlockContext = class {
  $implicit;
  constructor($implicit) {
    this.$implicit = $implicit;
  }
};
var TranslateBlockDirective = class _TranslateBlockDirective {
  templateRef = inject(TemplateRef);
  viewContainer = inject(ViewContainerRef);
  translateService = inject(TranslateService);
  ngOnInit() {
    const translateFn = (key, params) => {
      return this.translateService.instant(key, params);
    };
    this.viewContainer.createEmbeddedView(this.templateRef, new TranslateBlockContext(translateFn));
  }
  static ngTemplateContextGuard(_dir, _ctx) {
    return true;
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _TranslateBlockDirective, deps: [], target: FactoryTarget.Directive });
  static \u0275dir = \u0275\u0275ngDeclareDirective({ minVersion: "14.0.0", version: "22.0.0", type: _TranslateBlockDirective, isStandalone: true, selector: "[translateBlock]", ngImport: core_exports });
};
\u0275\u0275ngDeclareClassMetadata({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: TranslateBlockDirective, decorators: [{
  type: Directive,
  args: [{
    // eslint-disable-next-line @angular-eslint/directive-selector
    selector: "[translateBlock]",
    standalone: true
  }]
}] });
var ContentKeyHandler = class {
  element;
  changeDetectorRef;
  translateService;
  lastParams;
  constructor(element, changeDetectorRef, translateService) {
    this.element = element;
    this.changeDetectorRef = changeDetectorRef;
    this.translateService = translateService;
    console.warn(`@ngx-translate/core: Using element content as a translation key is deprecated and will be removed in v19. Use [translate]="'KEY'" or *translateBlock="let t" instead.`, this.element?.nativeElement);
  }
  checkNodes(currentParams, forceUpdate = false) {
    const nodes = this.element.nativeElement.childNodes;
    if (!nodes.length) {
      return;
    }
    nodes.forEach((n) => {
      const node = n;
      if (node.nodeType === 3) {
        let key;
        if (forceUpdate) {
          node.lastKey = null;
        }
        if (isDefinedAndNotNull(node.lookupKey)) {
          key = node.lookupKey;
        } else {
          const content = this.getContent(node);
          const trimmedContent = content.trim();
          if (trimmedContent.length) {
            node.lookupKey = trimmedContent;
            if (content !== node.currentValue) {
              key = trimmedContent;
              node.originalContent = content || node.originalContent;
            } else if (node.originalContent) {
              key = node.originalContent.trim();
            }
          }
        }
        this.updateValue(key, node, currentParams);
      }
    });
  }
  updateValue(key, node, currentParams) {
    if (!key) {
      return;
    }
    if (node.lastKey === key && this.lastParams === currentParams) {
      return;
    }
    this.lastParams = currentParams;
    const res = this.translateService.instant(key, currentParams);
    if (res !== key || !node.lastKey) {
      node.lastKey = key;
    }
    if (!node.originalContent) {
      node.originalContent = this.getContent(node);
    }
    if (isString(res)) {
      node.currentValue = res;
    } else if (!isDefinedAndNotNull(res)) {
      node.currentValue = node.originalContent || key;
    } else {
      node.currentValue = JSON.stringify(res);
    }
    this.setContent(node, node.originalContent.replace(key, node.currentValue));
    this.changeDetectorRef.markForCheck();
  }
  getContent(node) {
    return isDefinedAndNotNull(node.textContent) ? node.textContent : node.data;
  }
  setContent(node, content) {
    if (isDefinedAndNotNull(node.textContent)) {
      node.textContent = content;
    } else {
      node.data = content;
    }
  }
};
var TranslateDirective = class _TranslateDirective {
  translateService = inject(TranslateService);
  element = inject(ElementRef);
  destroyRef = inject(DestroyRef);
  changeDetectorRef = inject(ChangeDetectorRef);
  injector = inject(Injector);
  key;
  currentParams;
  // Signal-based explicit key path
  keySignal = null;
  paramsSignal = null;
  translationSignal = null;
  effectCreated = false;
  // Deprecated content-as-key path
  contentKeyHandler = null;
  set translate(key) {
    if (key) {
      this.key = key;
      if (!this.keySignal) {
        this.keySignal = signal(
          key,
          ...ngDevMode ? [{ debugName: "keySignal" }] : (
            /* istanbul ignore next */
            []
          )
        );
        this.paramsSignal = signal(
          this.currentParams,
          ...ngDevMode ? [{ debugName: "paramsSignal" }] : (
            /* istanbul ignore next */
            []
          )
        );
        this.translationSignal = this.translateService.translate(this.keySignal, this.paramsSignal);
        this.setupEffect();
      } else {
        this.keySignal.set(key);
      }
    }
  }
  set translateParams(params) {
    if (!equals(this.currentParams, params)) {
      this.currentParams = params;
      if (this.paramsSignal) {
        this.paramsSignal.set(params);
      } else if (this.contentKeyHandler) {
        this.contentKeyHandler.checkNodes(params, true);
      }
    }
  }
  constructor() {
    this.translateService.onTranslationRefresh.pipe(takeUntilDestroyed(this.destroyRef)).subscribe(() => {
      if (this.translationSignal) {
        this.writeTranslationToDOM();
      } else if (this.contentKeyHandler) {
        this.contentKeyHandler.checkNodes(this.currentParams, true);
      }
    });
  }
  ngAfterViewChecked() {
    if (!this.keySignal && !this.contentKeyHandler) {
      this.contentKeyHandler = new ContentKeyHandler(this.element, this.changeDetectorRef, this.translateService);
    }
    if (this.contentKeyHandler) {
      this.contentKeyHandler.checkNodes(this.currentParams);
    }
  }
  setupEffect() {
    if (this.effectCreated) {
      return;
    }
    this.effectCreated = true;
    effect(() => {
      const value = this.translationSignal();
      this.writeToDOM(value);
    }, { injector: this.injector });
  }
  writeTranslationToDOM() {
    if (this.translationSignal) {
      const value = this.translationSignal();
      this.writeToDOM(value);
    }
  }
  writeToDOM(value) {
    const el = this.element.nativeElement;
    let text;
    if (isString(value)) {
      text = value;
    } else if (!isDefinedAndNotNull(value)) {
      text = this.key;
    } else {
      text = JSON.stringify(value);
    }
    el.textContent = text;
    this.changeDetectorRef.markForCheck();
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _TranslateDirective, deps: [], target: FactoryTarget.Directive });
  static \u0275dir = \u0275\u0275ngDeclareDirective({ minVersion: "14.0.0", version: "22.0.0", type: _TranslateDirective, isStandalone: true, selector: "[translate],[ngx-translate]", inputs: { translate: "translate", translateParams: "translateParams" }, ngImport: core_exports });
};
\u0275\u0275ngDeclareClassMetadata({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: TranslateDirective, decorators: [{
  type: Directive,
  args: [{
    // eslint-disable-next-line @angular-eslint/directive-selector
    selector: "[translate],[ngx-translate]",
    standalone: true
  }]
}], ctorParameters: () => [], propDecorators: { translate: [{
  type: Input
}], translateParams: [{
  type: Input
}] } });
var TranslatePipe = class _TranslatePipe {
  translateService = inject(TranslateService);
  cachedSignal = null;
  lastKey = null;
  lastParams;
  /* eslint-disable-next-line @typescript-eslint/no-explicit-any */
  transform(query, ...args) {
    if (!query || !query.length) {
      return query;
    }
    const interpolateParams = this.parseArgs(args);
    if (query !== this.lastKey || !equals(interpolateParams, this.lastParams)) {
      this.cachedSignal = this.translateService.translate(query, interpolateParams);
      this.lastKey = query;
      this.lastParams = interpolateParams;
    }
    return this.cachedSignal();
  }
  parseArgs(args) {
    if (!isDefinedAndNotNull(args[0]) || !args.length) {
      return void 0;
    }
    if (isString(args[0]) && args[0].length) {
      const validArgs = args[0].replace(/(')?([a-zA-Z0-9_]+)(')?(\s)?:/g, '"$2":').replace(/:(\s)?(')(.*?)(')/g, ':"$3"');
      try {
        return JSON.parse(validArgs);
      } catch (e) {
        void e;
        throw new SyntaxError(`Wrong parameter in TranslatePipe. Expected a valid Object, received: ${args[0]}`);
      }
    }
    if (isDict(args[0])) {
      return args[0];
    }
    return void 0;
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _TranslatePipe, deps: [], target: FactoryTarget.Pipe });
  static \u0275pipe = \u0275\u0275ngDeclarePipe({ minVersion: "14.0.0", version: "22.0.0", ngImport: core_exports, type: _TranslatePipe, isStandalone: true, name: "translate", pure: false });
  static \u0275prov = \u0275\u0275ngDeclareInjectable({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: _TranslatePipe });
};
\u0275\u0275ngDeclareClassMetadata({ minVersion: "12.0.0", version: "22.0.0", ngImport: core_exports, type: TranslatePipe, decorators: [{
  type: Injectable
}, {
  type: Pipe,
  args: [{
    name: "translate",
    standalone: true,
    pure: false
    // required to update the value when the signal changes
  }]
}] });
function isClass(fn) {
  return /^class\s/.test(Function.prototype.toString.call(fn));
}
function toProvider(token, value) {
  return isClass(value) ? { provide: token, useClass: value } : { provide: token, useFactory: value };
}
function provideTranslateService(config = {}) {
  return defaultProviders(__spreadProps(__spreadValues({}, config), { isRoot: true }));
}
function resolvePluginProvider(token, value, defaultClass, fieldName, helperName) {
  if (value === void 0)
    return toProvider(token, defaultClass);
  if (typeof value === "function") {
    if (isClass(value)) {
      const className = value.name || "YourClass";
      console.warn(`@ngx-translate/core: "${fieldName}" received a bare class (${className}); auto-wrapping with ${helperName}(). For clarity, prefer ${fieldName}: ${helperName}(${className}).`);
    }
    return toProvider(token, value);
  }
  return value;
}
function defaultProviders(config) {
  const providers = [];
  const loader = resolvePluginProvider(TranslateLoader, config.loader, TranslateNoOpLoader, "loader", "provideTranslateLoader");
  const compiler = resolvePluginProvider(TranslateCompiler, config.compiler, TranslateNoOpCompiler, "compiler", "provideTranslateCompiler");
  const parser = resolvePluginProvider(TranslateParser, config.parser, TranslateDefaultParser, "parser", "provideTranslateParser");
  const missingTranslationHandler = resolvePluginProvider(MissingTranslationHandler, config.missingTranslationHandler, DefaultMissingTranslationHandler, "missingTranslationHandler", "provideMissingTranslationHandler");
  providers.push(loader, compiler, parser, missingTranslationHandler);
  providers.push(TranslateStore);
  const serviceConfig = {
    fallbackLang: config.fallbackLang ?? null,
    lang: config.lang,
    isRoot: config.isRoot
  };
  providers.push({
    provide: TRANSLATE_SERVICE_CONFIG,
    useValue: serviceConfig
  });
  providers.push({
    provide: TranslateService,
    useClass: TranslateService
  });
  return providers;
}

export {
  TranslateService,
  TranslatePipe,
  provideTranslateService
};
//# debugId=a502fe68-f5ab-57d5-9418-fe8bf0c82151
//# sourceMappingURL=chunk-6WNZTOXO.js.map
