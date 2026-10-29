import { ChangeDetectionStrategy, ChangeDetectorRef, Component, ElementRef, HostListener, OnDestroy, OnInit, ViewChild } from '@angular/core';
import { Router } from '@angular/router';
import { MatDialog } from '@angular/material/dialog';
import { TranslateService } from '@ngx-translate/core';
import { Subscription, take } from 'rxjs';

import { Node } from '../../../app.datatypes';
import { CommandPaletteService, paletteIsMac } from '../../../services/command-palette.service';
import { MultipleNodeDataService } from '../../../services/multiple-node-data.service';
import { NodeService } from '../../../services/node.service';
import { ClipboardService } from '../../../services/clipboard.service';
import { SnackbarService } from '../../../services/snackbar.service';
import { homeTabsData } from '../../../utils/home-tabs';
import { nodePaletteTabs } from '../../../utils/node-tabs';
import { ptyUrl } from '../../../utils/pty-url';
import GeneralUtils from '../../../utils/generalUtils';
import { PaletteGroup, PaletteItem, PaletteVisor, paletteEntries, paletteTokens, rankPalette } from '../../../utils/command-palette';
import { NodeData, UpdateAllComponent } from '../update-all/update-all.component';
import { BulkRewardAddressChangerComponent } from '../bulk-reward-address-changer/bulk-reward-address-changer.component';
import { ThemeService } from '../../../services/theme.service';

interface ShownGroup {
  group: PaletteGroup;
  items: { item: PaletteItem; index: number }[];
}

/**
 * True for the key press that opens the palette: Ctrl-K, or Cmd-K on a Mac.
 * On a Mac, Ctrl-K inside a text field deletes to the line end, so it is left alone.
 */
export function isPaletteShortcut(e: KeyboardEvent, mac: boolean, inTextField: boolean): boolean {
  if ((e.key || '').toLowerCase() !== 'k' || e.altKey || e.shiftKey) {
    return false;
  }
  if (mac && e.metaKey && !e.ctrlKey) {
    return true;
  }

  return e.ctrlKey && !e.metaKey && !(mac && inTextField);
}

/** The palette's view of a visor from the visor list. */
export function paletteVisor(n: Node): PaletteVisor {
  return {
    pk: n.localPk,
    label: n.label,
    ip: n.publicIp || n.ip || undefined,
    place: [n.cityName, n.regionName, n.countryCode].filter(p => !!p).join(', ') || undefined,
    hostname: n.hostname,
    online: n.online,
    wasm: n.arch === 'wasm',
    version: n.version,
    buildTag: n.buildTag,
  };
}

/** The visor whose page a router url shows, if any. */
export function visorPkFromUrl(url: string): string | undefined {
  const m = /^\/nodes\/([0-9a-fA-F]{66})(?:[/?#]|$)/.exec(url || '');

  return m ? m[1] : undefined;
}

/**
 * Ctrl-K overlay for finding a visor, a page or an action on a visor. It uses the
 * visor list the dashboard already has and fetches it once only if there is none.
 */
@Component({
  selector: 'app-command-palette',
  templateUrl: './command-palette.component.html',
  styleUrls: ['./command-palette.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class CommandPaletteComponent implements OnInit, OnDestroy {
  @ViewChild('searchInput') searchInput?: ElementRef<HTMLInputElement>;

  open = false;
  query = '';
  groups: ShownGroup[] = [];
  selected = 0;
  readonly mac = paletteIsMac();

  private flat: PaletteItem[] = [];
  private visors: PaletteVisor[] = [];
  private currentPk?: string;
  private previousFocus: HTMLElement | null = null;
  private lastPointer?: string;
  private openSubscription!: Subscription;
  private fetchSubscription?: Subscription;
  private restartSubscription?: Subscription;

  constructor(
    private router: Router,
    private dialog: MatDialog,
    private translate: TranslateService,
    private paletteService: CommandPaletteService,
    private multipleNodeDataService: MultipleNodeDataService,
    private nodeService: NodeService,
    private clipboardService: ClipboardService,
    private snackbarService: SnackbarService,
    private cdr: ChangeDetectorRef,
    private themeService: ThemeService,
  ) { }

  ngOnInit() {
    this.openSubscription = this.paletteService.openRequests.subscribe(() => {
      this.show();
      this.cdr.markForCheck();
    });
  }

  ngOnDestroy() {
    this.openSubscription.unsubscribe();
    this.fetchSubscription?.unsubscribe();
    this.restartSubscription?.unsubscribe();
  }

  @HostListener('document:keydown', ['$event'])
  onDocumentKey(e: KeyboardEvent) {
    const target = e.target as HTMLElement | null;
    const inTextField = !!target && (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName));
    if (isPaletteShortcut(e, this.mac, inTextField)) {
      if (this.open) {
        e.preventDefault();
        this.close();
      } else if (this.enabled()) {
        e.preventDefault();
        this.show();
      }
    } else if (this.open && e.key === 'Escape') {
      e.preventDefault();
      this.close();
    }
  }

  onInput(e: Event) {
    this.query = (e.target as HTMLInputElement).value;
    this.refresh();
  }

  onInputKey(e: KeyboardEvent) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (this.flat.length) {
        const step = e.key === 'ArrowDown' ? 1 : -1;
        this.selected = (this.selected + step + this.flat.length) % this.flat.length;
        this.cdr.detectChanges();
        document.getElementById('palette-item-' + this.selected)?.scrollIntoView({ block: 'nearest' });
      }
    } else if (e.key === 'Enter') {
      e.preventDefault();
      if (this.flat[this.selected]) {
        this.run(this.flat[this.selected]);
      }
    }
  }

  // A list opening under a still pointer fires mousemove too, so only a real move selects.
  hover(index: number, e: MouseEvent) {
    const pos = e.screenX + ',' + e.screenY;
    if (this.lastPointer !== undefined && this.lastPointer !== pos) {
      this.selected = index;
    }
    this.lastPointer = pos;
  }

  close() {
    this.open = false;
    this.cdr.markForCheck();
    if (this.previousFocus && document.contains(this.previousFocus)) {
      this.previousFocus.focus();
    }
    this.previousFocus = null;
  }

  run(item: PaletteItem) {
    this.previousFocus = null;
    this.close();
    if (item.theme) {
      this.themeService.setChoice(item.theme);
    } else if (item.action && item.visor) {
      this.runAction(item);
    } else if (item.externalUrl) {
      window.open(item.externalUrl, '_blank', 'noopener noreferrer');
    } else if (item.link) {
      this.router.navigate(item.link);
    }
  }

  // The embedded single-tab views, the VPN client and the login page have no use for it.
  private enabled(): boolean {
    const url = this.router.url || '';

    return !window.location.href.includes('embed=1') && !url.includes('/vpn/') && !url.includes('vpnlogin') && !url.includes('login');
  }

  private show() {
    if (!this.enabled()) {
      return;
    }
    this.previousFocus = document.activeElement as HTMLElement | null;
    this.open = true;
    this.query = '';
    this.lastPointer = undefined;
    this.currentPk = visorPkFromUrl(this.router.url);
    this.loadVisors();
    this.refresh();
    // change-detection: no view state — focuses the input once it is rendered
    setTimeout(() => this.searchInput?.nativeElement.focus());
  }

  private loadVisors() {
    const data = this.multipleNodeDataService.lastEmitedData;
    let nodes: Node[] = [];
    if (data && data.sections && data.sections.length) {
      data.sections.forEach(s => nodes = nodes.concat(s.nodes || []));
    } else if (data && Array.isArray(data.data)) {
      nodes = data.data;
    }
    // Keep the list fetched on an earlier opening when the visor list page has none.
    if (nodes.length) {
      this.setVisors(nodes);
    }
    if (this.visors.length === 0 && !this.fetchSubscription) {
      this.fetchSubscription = this.nodeService.getNodes().pipe(take(1)).subscribe({
        next: list => {
          this.setVisors(list);
          this.refresh();
          this.cdr.markForCheck();
        },
        error: () => {
          this.fetchSubscription = undefined;
          this.cdr.markForCheck();
        },
        complete: () => {
          this.fetchSubscription = undefined;
          this.cdr.markForCheck();
        },
      });
    }
  }

  private setVisors(nodes: Node[]) {
    const seen = new Set<string>();
    this.visors = (Array.isArray(nodes) ? nodes : []).filter(n => n && n.localPk && !seen.has(n.localPk) && !!seen.add(n.localPk))
      .map(paletteVisor);
  }

  private refresh() {
    let current = this.visors.find(v => v.pk === this.currentPk);
    if (!current && this.currentPk) {
      current = { pk: this.currentPk, label: '' };
    }
    const entries = paletteEntries({
      visors: this.visors,
      current: current,
      homeTabs: homeTabsData(),
      tabsFor: v => nodePaletteTabs(v.pk, !!v.wasm),
      translate: k => this.translate.instant(k),
      themeChoice: this.themeService.choice,
    }, paletteTokens(this.query));
    const ranked = rankPalette(entries, this.query);
    this.flat = [];
    this.groups = ranked.map(g => {
      const items = g.items.map(item => {
        this.flat.push(item);

        return { item: item, index: this.flat.length - 1 };
      });

      return { group: g.group, items: items };
    });
    this.selected = 0;
  }

  private runAction(item: PaletteItem) {
    const v = item.visor!;
    const name = v.label || v.pk;
    if (item.action === 'copy-key') {
      if (this.clipboardService.copy(v.pk)) {
        this.snackbarService.showDone('copy.copied');
      }
    } else if (item.action === 'terminal') {
      window.open(ptyUrl(v.pk), '_blank', 'noopener noreferrer');
    } else if (item.action === 'logs') {
      this.router.navigate(['/nodes', v.pk, 'logs']);
    } else if (item.action === 'restart') {
      const confirmationDialog = GeneralUtils.createConfirmationDialog(
        this.dialog, this.translate.instant('palette.restart-confirmation', { name: name }));
      confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
        confirmationDialog.close();
        this.restartSubscription = this.nodeService.restart(v.pk).subscribe({
          next: () => {
            this.snackbarService.showDone(this.translate.instant('nodes.bulk.restarting', { name: name }));
            this.cdr.markForCheck();
          },
          error: () => {
            this.snackbarService.showError(this.translate.instant('nodes.bulk.restart-error', { name: name }));
            this.cdr.markForCheck();
          },
        });
        this.cdr.markForCheck();
      });
    } else if (item.action === 'update') {
      const data: NodeData = { key: v.pk, label: name, version: v.version || '', tag: v.buildTag || '' };
      const updatable = GeneralUtils.checkIfTagIsUpdatable(data.tag);
      UpdateAllComponent.openDialog(this.dialog, updatable ? [data] : [], updatable ? [] : [data]);
    } else if (item.action === 'reward') {
      BulkRewardAddressChangerComponent.openDialog(this.dialog, { nodes: [{ key: v.pk, label: name }] });
    }
  }
}
