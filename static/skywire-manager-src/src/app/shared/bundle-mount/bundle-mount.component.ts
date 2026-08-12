import { AfterViewInit, Component, ElementRef, EventEmitter, Input, OnDestroy, Output, ViewChild, ChangeDetectionStrategy } from '@angular/core';

/**
 * Handle returned by a mountable bundle's `mount()` entry, used to tear it down.
 */
export interface MountHandle {
  unmount(): void;
}

/**
 * The contract a first-party embeddable UI bundle exposes on `window` (see
 * docs/design/gui-embedding-standardization.md): a `mount(root, opts)` that
 * builds all of its own DOM inside `root` and returns a {@link MountHandle}.
 */
export interface MountableBundle {
  mount(root: HTMLElement, opts?: Record<string, unknown>): MountHandle;
}

/**
 * Generic host for the `mount()` contract — the reusable machinery factored out
 * of NetworkVisualizerComponent so any first-party JS/TS bundle can be embedded
 * into the SPA (a tab or a WinBox `mount:` window) without an iframe or a
 * break-out server path.
 *
 * It loads the bundle `src` once (deduped by script id), resolves the mount
 * global named by `globalName` (a dot path on `window`, e.g. `SkywireTpviz` or
 * `SkywireUI.networkVisualizer`), calls `mount(root, opts)` into its own
 * `<div>`, and tears it down on destroy. It renders three states:
 *  - loading — while the script is fetched and mounted,
 *  - unavailable — a calm info note when the bundle isn't served on this
 *    surface (e.g. the wasm visor doesn't serve /tp-viz/) or exposes no global,
 *  - error — a red note when `mount()` itself throws.
 *
 * The resolved global + handle are emitted via `ready` so a host component can
 * drive the bundle further (e.g. call `setView`); mount failures surface on
 * `failed`.
 */
@Component({
  selector: 'app-bundle-mount',
  templateUrl: './bundle-mount.component.html',
  styleUrls: ['./bundle-mount.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class BundleMountComponent implements AfterViewInit, OnDestroy {
  /** URL of the bundle script to load (same-origin, relative to the SPA base). */
  @Input({ required: true }) src!: string;
  /** Dot path to the mount global on `window` (e.g. `SkywireTpviz`). */
  @Input({ required: true }) globalName!: string;
  /** Options passed straight through to the bundle's `mount(root, opts)`. */
  @Input() opts: Record<string, unknown> = {};
  /** Script element id used to dedupe the <script> across mounts. Defaults to `src`. */
  @Input() scriptId?: string;
  /** Text shown while loading. */
  @Input() loadingText = 'loading…';
  /** Text shown when the bundle isn't served on this surface. */
  @Input() unavailableText = "This component isn't available on this visor — it isn't served here.";

  /** Emits once the bundle is mounted, with its global and teardown handle. */
  @Output() ready = new EventEmitter<{ global: MountableBundle; handle: MountHandle }>();
  /** Emits the error message if `mount()` throws. */
  @Output() failed = new EventEmitter<string | null>();

  @ViewChild('mountRoot', { static: false }) rootEl!: ElementRef<HTMLDivElement>;

  loading = true;
  error: string | null = null;
  unavailable = false;

  private handle: MountHandle | null = null;

  ngAfterViewInit(): void {
    this.loadAndMount();
  }

  private resolveGlobal(): MountableBundle | null {
    let obj: any = window;
    for (const seg of this.globalName.split('.')) {
      if (obj == null) {
        return null;
      }
      obj = obj[seg];
    }

    return obj && typeof obj.mount === 'function' ? obj as MountableBundle : null;
  }

  private loadAndMount(): void {
    const doMount = () => {
      const global = this.resolveGlobal();
      if (!global || !this.rootEl) {
        this.unavailable = true;
        this.loading = false;

        return;
      }
      try {
        this.handle = global.mount(this.rootEl.nativeElement, this.opts);
        this.loading = false;
        this.ready.emit({ global: global, handle: this.handle });
      } catch (e: any) {
        this.error = e?.message || 'failed to mount component';
        this.loading = false;
        this.failed.emit(this.error);
      }
    };

    // Already loaded on this surface.
    if (this.resolveGlobal()) {
      doMount();

      return;
    }

    const onUnavailable = () => {
      this.unavailable = true;
      this.loading = false;
    };

    const id = this.scriptId || this.src;
    const existing = document.getElementById(id) as HTMLScriptElement | null;
    if (existing) {
      existing.addEventListener('load', doMount);
      existing.addEventListener('error', onUnavailable);

      return;
    }

    const s = document.createElement('script');
    s.id = id;
    s.src = this.src;
    s.async = true;
    s.onload = () => doMount();
    s.onerror = onUnavailable;
    document.body.appendChild(s);
  }

  ngOnDestroy(): void {
    if (this.handle) {
      try {
        this.handle.unmount();
      } catch { /* ignore teardown errors */ }
      this.handle = null;
    }
  }
}
