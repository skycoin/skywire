import { ApplicationRef, ComponentRef, Injectable, Injector, Type } from '@angular/core';
import { ComponentPortal, DomPortalOutlet } from '@angular/cdk/portal';

import { SkychatComponent } from '../components/pages/node/skychat/skychat.component';
import { LogsComponent } from '../components/pages/node/logs/logs.component';

/** Options passed from the host (browse.js WinBox) to a mounted component. */
export interface NgMountOpts {
  /** Visor public key the component should act on (its `embeddedNodeKey`). */
  nodeKey?: string;
  /** Optional peer PK to preselect (skychat's `embeddedPeer`). */
  peer?: string;
}

/** Handle returned to the host so it can tear the mounted component down. */
export interface NgMountHandle {
  dispose(): void;
}

/**
 * Bridge that lets non-Angular host code (the wasm desktop's browse.js WinBox
 * windows) MOUNT a real Angular component into a plain `<div>` in the SPA's own
 * DOM/JS context — instead of iframing the SPA back into itself with `?embed=1`.
 *
 * It exposes `window.SkywireNg.mountComponent(el, name, opts)`; the component is
 * created via a CDK `DomPortalOutlet` in the app's root injector (so it gets the
 * same services/theme/session), its `embedded*` inputs are set from `opts`, and
 * a handle with `dispose()` is returned for teardown on window close.
 *
 * Only components that are self-contained enough to run outside the router are
 * registered here (they read their visor context from the `embeddedNodeKey`
 * input rather than the `NodeComponent` parent route).
 */
@Injectable({ providedIn: 'root' })
export class NgBridgeService {
  private readonly registry: Record<string, Type<unknown>> = {
    skychat: SkychatComponent,
    logs: LogsComponent,
  };

  constructor(
    private appRef: ApplicationRef,
    private injector: Injector,
  ) { }

  /** Publish the bridge on `window.SkywireNg` (called once at app startup). */
  install(): void {
    (window as unknown as { SkywireNg?: unknown }).SkywireNg = {
      mountComponent: (el: HTMLElement, name: string, opts: NgMountOpts = {}): NgMountHandle | null =>
        this.mount(el, name, opts),
    };
  }

  private mount(el: HTMLElement, name: string, opts: NgMountOpts): NgMountHandle | null {
    const component = this.registry[name];
    if (!component) {
      console.error('SkywireNg.mountComponent: unknown component', name);

      return null;
    }

    const outlet = new DomPortalOutlet(el, this.appRef, this.injector);
    const ref = outlet.attach(new ComponentPortal(component)) as ComponentRef<unknown>;
    // Set inputs BEFORE the first change detection so the component's ngOnInit
    // sees them (detectChanges below runs ngOnInit synchronously).
    if (opts.nodeKey) {
      ref.setInput('embeddedNodeKey', opts.nodeKey);
    }
    if (opts.peer) {
      ref.setInput('embeddedPeer', opts.peer);
    }
    ref.changeDetectorRef.detectChanges();

    return {
      dispose: () => {
        try {
          outlet.dispose();
        } catch { /* ignore teardown errors */ }
      },
    };
  }
}
