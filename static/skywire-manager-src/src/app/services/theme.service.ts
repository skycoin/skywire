import { Injectable, OnDestroy } from '@angular/core';
import { BehaviorSubject, Observable } from 'rxjs';

export type ThemeChoice = 'system' | 'light' | 'dark';
export type Theme = 'light' | 'dark';

export const themeChoices: ThemeChoice[] = ['system', 'light', 'dark'];

/**
 * The light or dark theme. It follows the system unless the user picked one,
 * and the pick is kept per browser, as the hypervisor has no per-user store.
 */
@Injectable({ providedIn: 'root' })
export class ThemeService implements OnDestroy {
  static readonly storageKey = 'hvui-theme';

  private readonly media: MediaQueryList | null;
  private readonly choiceSubject: BehaviorSubject<ThemeChoice>;
  private readonly onMediaChange: () => void;

  constructor() {
    this.onMediaChange = () => this.apply();
    this.media = typeof window.matchMedia === 'function' ? window.matchMedia('(prefers-color-scheme: light)') : null;
    this.choiceSubject = new BehaviorSubject<ThemeChoice>(this.readChoice());
    this.media?.addEventListener?.('change', this.onMediaChange);
    this.apply();
  }

  ngOnDestroy() {
    this.media?.removeEventListener?.('change', this.onMediaChange);
  }

  get choice(): ThemeChoice {
    return this.choiceSubject.value;
  }

  get choice$(): Observable<ThemeChoice> {
    return this.choiceSubject.asObservable();
  }

  /** The theme in effect, with 'system' resolved. */
  get theme(): Theme {
    if (this.choice !== 'system') {
      return this.choice;
    }

    return this.media?.matches ? 'light' : 'dark';
  }

  setChoice(choice: ThemeChoice) {
    if (!themeChoices.includes(choice)) {
      return;
    }
    try {
      if (choice === 'system') {
        window.localStorage.removeItem(ThemeService.storageKey);
      } else {
        window.localStorage.setItem(ThemeService.storageKey, choice);
      }
    } catch {
      // Storage can be blocked. The choice still holds for this page.
    }
    this.choiceSubject.next(choice);
    this.apply();
  }

  private readChoice(): ThemeChoice {
    try {
      const saved = window.localStorage.getItem(ThemeService.storageKey) as ThemeChoice;

      return themeChoices.includes(saved) ? saved : 'system';
    } catch {
      return 'system';
    }
  }

  private apply() {
    document.documentElement.setAttribute('data-theme', this.theme);
  }
}
