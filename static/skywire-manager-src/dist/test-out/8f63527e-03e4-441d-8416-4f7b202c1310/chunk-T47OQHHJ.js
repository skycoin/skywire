import {
  BulkRewardAddressChangerComponent,
  MultipleNodeDataService,
  UpdateAllComponent
} from "./chunk-SW25Q345.js";
import {
  ClipboardService
} from "./chunk-XSOC4ELV.js";
import {
  BulkAppActionComponent
} from "./chunk-4PPPV3QE.js";
import {
  AuthService,
  AuthStates
} from "./chunk-4XQ3VDCY.js";
import {
  AppComponent,
  PageBaseComponent,
  SelectOptionComponent,
  visorSwitcherChips
} from "./chunk-CIWHQAPM.js";
import {
  GeneralUtils,
  homeTabsData
} from "./chunk-KWVFHEEW.js";
import {
  UntypedFormBuilder
} from "./chunk-B4GMTCLR.js";
import {
  AppConfig,
  MAT_DIALOG_DATA,
  MatDialog,
  MatDialogConfig,
  MatDialogRef
} from "./chunk-CAA347WM.js";
import {
  TranslateService
} from "./chunk-6WNZTOXO.js";
import {
  KnownHealthStatuses,
  NodeService
} from "./chunk-XBRWDJ2O.js";
import {
  ActivatedRoute,
  CdkScrollableModule,
  Directionality,
  HttpClient,
  Router,
  ScrollDispatcher,
  SnackbarService,
  ViewportRuler,
  _CdkPrivateStyleLoader,
  _IdGenerator,
  _getEventTarget,
  _getShadowRoot,
  coerceArray,
  coerceElement,
  coerceNumberProperty,
  isFakeMousedownFromScreenReader,
  isFakeTouchstartFromScreenReader
} from "./chunk-ZMZW6HXB.js";
import {
  LabelInfo,
  LabeledElementTypes,
  StorageService
} from "./chunk-XOUXCPTJ.js";
import {
  BehaviorSubject,
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  Component,
  DOCUMENT,
  Directive,
  ElementRef,
  EventEmitter,
  FactoryTarget,
  Inject,
  Injectable,
  InjectionToken,
  Injector,
  Input,
  NgModule,
  NgZone,
  Observable,
  Output,
  Renderer2,
  RendererFactory2,
  Service,
  Subject,
  Subscription,
  TemplateRef,
  ViewChild,
  ViewContainerRef,
  ViewEncapsulation,
  afterNextRender,
  animationFrameScheduler,
  booleanAttribute,
  catchError,
  core_exports,
  forkJoin,
  inject,
  interval,
  map,
  merge,
  mergeMap,
  numberAttribute,
  of,
  signal,
  startWith,
  switchMap,
  take,
  takeUntil,
  tap,
  timer,
  ɵɵngDeclareClassMetadata,
  ɵɵngDeclareComponent,
  ɵɵngDeclareDirective,
  ɵɵngDeclareFactory,
  ɵɵngDeclareInjector,
  ɵɵngDeclareNgModule,
  ɵɵngDeclareService
} from "./chunk-ZNLGPAUZ.js";

// angular:jit:template:src/app/components/pages/node-list/node-list.component.html
var node_list_component_default = `<!-- Content when loading. -->
@if (loading) {
  <div class="d-flex flex-column h-100 w-100">
    <div>
      <app-top-bar
        [titleParts]="['start.title']"
        [tabsData]="tabsData"
        [selectedTabIndex]="showRewardsInfo ? 2 : 0"
        [showUpdateButton]="false"
      ></app-top-bar>
    </div>
    <app-loading-indicator class="h-100"></app-loading-indicator>
  </div>
}

<!-- Content when loaded. -->
@if (!loading) {
  <div class="row">
    <div class="col-12">
      <app-top-bar
        [titleParts]="['start.title']"
        [tabsData]="tabsData"
        [switcherTabsData]="switcherTabs"
        [selectedTabIndex]="showRewardsInfo ? 2 : 0"
        [secondsSinceLastUpdate]="secondsSinceLastUpdate"
        [showLoading]="updating"
        [showAlert]="errorsUpdating"
        [refeshRate]="storageService.getRefreshTime()"
        [optionsData]="options"
        (refreshRequested)="forceDataRefresh(true)"
        (optionSelected)="performAction($event)"
      ></app-top-bar>
    </div>
    <div class="col-12">
      <!-- Header. -->
      <div class="generic-title-container mt-4.5 d-flex" [ngClass]="{'paginator-icons-fixer': numberOfPages > 1}">
        <div class="title">
          @if (localHypervisorPk) {
            <!-- Local hypervisor PK label. Populated from the tree
                 response's local section (sections[0]). The next-step
                 PR adds per-sub-hypervisor table sections below the
                 main table; this is the title bar piece per #2640's
                 design. -->
            <div class="hypervisor-pk-label transparent-50" [matTooltip]="localHypervisorPk">
              <mat-icon class="small-icon" [inline]="true">star_outline</mat-icon>
              <span class="ml-1">{{ localHypervisorPk }}</span>
            </div>
          }
          @if (dataFilterer.currentFiltersTexts && dataFilterer.currentFiltersTexts.length > 0) {
            <div class="filter-label subtle-transparent-button cursor-pointer" (click)="dataFilterer.removeFilters()">
              @for (filterTexts of dataFilterer.currentFiltersTexts; track filterTexts) {
                <div class="item">
                  <span>{{ filterTexts.filterName | translate }}: </span>
                  @if (filterTexts.translatableValue) {
                    {{ filterTexts.translatableValue | translate }}
                  }
                  @if (filterTexts.value) {
                    {{ filterTexts.value }}
                  }
                </div>
              }
              <div class="transparent-50">{{ 'filters.press-to-remove' | translate }}</div>
            </div>
          }
        </div>
        <div class="options">
          <div class="options-container">
            @if (allNodes && allNodes.length > 0) {
              <mat-icon
                [inline]="true"
                (click)="dataFilterer.changeFilters()"
                [matTooltip]="'filters.filter-action' | translate"
                class="small-icon"
              >filter_list</mat-icon>
            }
            @if (dataSource.length > 0) {
              <mat-icon
                [matMenuTriggerFor]="selectionMenu"
              >more_horiz</mat-icon>
            }
            <mat-menu #selectionMenu="matMenu" [overlapTrigger]="false">
              <div mat-menu-item disabled="{{ !hasOfflineNodes }}" (click)="removeOffline()">
                {{ 'nodes.delete-all-offline' | translate }}
              </div>
            </mat-menu>
          </div>
          <!-- Upper paginator. -->
          @if (numberOfPages > 1) {
            <app-paginator
              [currentPage]="currentPage"
              [numberOfPages]="numberOfPages"
              [linkParts]="showRewardsInfo ? ['/nodes', 'rewards'] : ['/nodes', 'list']"
              [queryParams]="dataFilterer.currentUrlQueryParams">
            </app-paginator>
          }
        </div>
      </div>
      @if (allNodes && allNodes.length > 0) {
        <div class="fleet-toolbar">
          <input
            class="fleet-search"
            [value]="quickSearch"
            (input)="setQuickSearch($any($event.target).value)"
            [placeholder]="'nodes.quick.search' | translate"
          />
          @for (chip of ['online', 'offline', 'outdated', 'no-reward']; track chip) {
            <button class="fleet-chip" [class.on]="quickChip === chip" (click)="setQuickChip($any(chip))">
              {{ 'nodes.quick.' + chip | translate }}
            </button>
          }
          @if (appNames.length > 0) {
            <label class="fleet-app-filter">
              {{ 'nodes.apps-filter.label' | translate }} &#9656;
              <select class="fleet-select" [value]="appFilter" (change)="setAppFilter($any($event.target).value)">
                <option value="">{{ 'nodes.apps-filter.any' | translate }}</option>
                @for (name of appNames; track name) {
                  <option [value]="name" [selected]="name === appFilter">{{ name }}</option>
                }
              </select>
            </label>
          }
          @if (quickFilterHides > 0) {
            <span class="fleet-hidden">{{ 'nodes.quick.hidden' | translate: { n: quickFilterHides } }}</span>
          }
          <span class="fleet-spacer"></span>
          @if (!showRewardsInfo) {
            <button class="fleet-chip" [class.on]="compact" (click)="toggleCompact()" [matTooltip]="'nodes.quick.compact-tooltip' | translate">
              {{ 'nodes.quick.compact' | translate }}
            </button>
            <button class="fleet-chip apps-chip" [class.on]="showApps" (click)="toggleApps()" [matTooltip]="'nodes.apps-filter.column-tooltip' | translate">
              {{ 'nodes.apps-filter.column' | translate }}
            </button>
          }
        </div>
        @if (selected.size > 0 && !showRewardsInfo) {
          <div class="fleet-bulk">
            <span>{{ 'nodes.bulk.selected' | translate: { n: selected.size } }}</span>
            <button mat-stroked-button (click)="updateSelected()">{{ 'nodes.bulk.update' | translate }}</button>
            <button mat-stroked-button (click)="rewardsSelected()">{{ 'nodes.bulk.reward' | translate }}</button>
            <button mat-stroked-button (click)="restartSelected()">{{ 'nodes.bulk.restart' | translate }}</button>
            <button mat-stroked-button class="bulk-add-app" (click)="appActionSelected('add')">{{ 'nodes.bulk.add-app' | translate }}</button>
            <button mat-stroked-button class="bulk-start-app" (click)="appActionSelected('start')">{{ 'nodes.bulk.start-app' | translate }}</button>
            <button mat-stroked-button class="bulk-stop-app" (click)="appActionSelected('stop')">{{ 'nodes.bulk.stop-app' | translate }}</button>
            <button mat-stroked-button (click)="clearSelection()">{{ 'nodes.bulk.clear' | translate }}</button>
          </div>
        }
      }
      @if (dataSource.length !== 0) {
        <div class="rounded-elevated-box mt-3"><div class="box-internal-container overflow full-node-list-margins">

          <!-- ==================== REWARDS TABLE (large screens) ==================== -->
          @if (showRewardsInfo && dataSource.length > 0) {
            <table
              class="responsive-table-translucid d-none d-md-table"
              cellspacing="0" cellpadding="0"
              >
              <!-- Column names. -->
              <tr>
                <th class="sortable-column small-column" (click)="dataSorter.changeSortingOrder(hypervisorSortData)" [matTooltip]="'nodes.hypervisor' | translate">
                  <mat-icon class="hypervisor-icon grey-text">star_outline</mat-icon>
                  @if (dataSorter.currentSortingColumn === hypervisorSortData) {
                    <mat-icon [inline]="true">{{ dataSorter.sortingArrow }}</mat-icon>
                  }
                </th>
                <th class="sortable-column small-column" (click)="dataSorter.changeSortingOrder(stateSortData)" [matTooltip]="'nodes.state-tooltip' | translate">
                  <span class="dot-outline-gray"></span>
                  @if (dataSorter.currentSortingColumn === stateSortData) {
                    <mat-icon [inline]="true">{{ dataSorter.sortingArrow }}</mat-icon>
                  }
                </th>
                <th class="sortable-column labels" (click)="dataSorter.changeSortingOrder(labelSortData)">
                  {{ 'nodes.label' | translate }}
                  @if (dataSorter.currentSortingColumn === labelSortData) {
                    <mat-icon [inline]="true">{{ dataSorter.sortingArrow }}</mat-icon>
                  }
                </th>
                <th class="sortable-column" (click)="dataSorter.changeSortingOrder(keySortData)">
                  {{ 'nodes.key' | translate }}
                  @if (dataSorter.currentSortingColumn === keySortData) {
                    <mat-icon [inline]="true">{{ dataSorter.sortingArrow }}</mat-icon>
                  }
                </th>
                <th>
                  {{ 'nodes.reward-address' | translate }}
                </th>
                @if (rewardDataLoaded) {
                  @for (header of rewardDateHeaders; track header; let i = $index) {
                    <th class="reward-day-column">
                      {{ header }}
                    </th>
                  }
                  <th class="reward-total-column">
                    {{ 'nodes.reward-total' | translate }}
                  </th>
                }
                @if (rewardDataLoading) {
                  <th>
                    {{ 'nodes.rewards-loading' | translate }}
                  </th>
                }
                <th class="actions"></th>
              </tr>
              <!-- Values. -->
              @for (node of dataSource; track node.localPk) {
                <a
                  [ngClass]="{'click-effect': true, 'wasm-visor-row': node.arch === 'wasm'}"
                  class="selectable link-row"
                  [routerLink]="['/nodes', node.localPk, 'rewards']"
                  >
                  <td>
                    @if (node.isHypervisor) {
                      <mat-icon class="hypervisor-icon" [inline]="true" [matTooltip]="'nodes.hypervisor-info' | translate">star</mat-icon>
                    }
                  </td>
                  <td>
                    <span [class]="nodeStatusClass(node, true)" [matTooltip]="nodeStatusText(node, true) | translate"></span>
                  </td>
                  <td>
                    {{ node.label }}
                    @if (node.isStale) {
                      <div class="stale-caption">{{ lastSeenText(node) }}</div>
                    }
                  </td>
                  <td>
                    {{ node.localPk }}
                  </td>
                  <td>
                    <span style="font-size: 0.85em;">{{ getRewardAddress(node.localPk) }}</span>
                  </td>
                  @if (rewardDataLoaded) {
                    @for (date of rewardDates; track date) {
                      <td class="reward-day-column">
                        <span [class]="getRewardClass(node.localPk, date)" style="font-size: 0.85em;">{{ getRewardAmount(node.localPk, date) }}</span>
                      </td>
                    }
                    <td class="reward-total-column">
                      <span style="font-size: 0.85em; font-weight: bold;">{{ getWeekTotal(node.localPk) }}</span>
                    </td>
                  }
                  @if (rewardDataLoading) {
                    <td>
                      <span style="font-size: 0.85em;">...</span>
                    </td>
                  }
                  <td class="actions">
                    <button
                      (click)="$event.stopPropagation(); $event.preventDefault(); copyToClipboard(node);"
                      mat-button
                      [matTooltip]="'nodes.copy-key' | translate"
                      class="big-action-button transparent-button"
                      >
                      <mat-icon [inline]="true">filter_none</mat-icon>
                    </button>
                    <button
                      (click)="$event.stopPropagation(); $event.preventDefault(); showEditLabelDialog(node);"
                      mat-button
                      [matTooltip]="'labeled-element.edit-label' | translate"
                      class="big-action-button transparent-button"
                      >
                      <mat-icon [inline]="true">short_text</mat-icon>
                    </button>
                    @if (node.online) {
                      <button
                        mat-button
                        [matTooltip]="'nodes.view-node' | translate"
                        class="big-action-button transparent-button"
                        >
                        <mat-icon [inline]="true">chevron_right</mat-icon>
                      </button>
                    }
                    @if (!node.online) {
                      <button
                        (click)="$event.stopPropagation(); $event.preventDefault(); deleteNode(node);"
                        mat-button
                        [matTooltip]="'nodes.delete-node' | translate"
                        class="big-action-button transparent-button"
                        >
                        <mat-icon>close</mat-icon>
                      </button>
                    }
                  </td>
                </a>
              }
            </table>
          }

          <!-- ==================== VISOR LIST TABLE (large screens) ==================== -->
          @if (!showRewardsInfo && dataSource.length > 0) {
            <table
              class="responsive-table-translucid d-none d-md-table"
              cellspacing="0" cellpadding="0"
              cdkDropList
              [cdkDropListData]="dataSource"
              (cdkDropListDropped)="rowDropped($event)"
              >
              <!-- Column names. Click any header to sort by that column;
                   clicking also drops any active drag-drop custom order
                   (changeSort wraps dataSorter.changeSortingOrder +
                   resetCustomOrder), so a sort always wins over a
                   previously-arranged manual order. -->
              <tr>
                <th class="select-column">
                  <mat-checkbox [checked]="pageAllSelected" (change)="toggleSelectPage()" [matTooltip]="'nodes.bulk.select-page' | translate"></mat-checkbox>
                </th>
                <th class="drag-handle-column"></th>
                <th class="sortable-column small-column" (click)="changeSort(hypervisorSortData)" [matTooltip]="'nodes.hypervisor' | translate">
                  <mat-icon class="hypervisor-icon grey-text">star_outline</mat-icon>
                  @if (dataSorter.currentSortingColumn === hypervisorSortData) {
                    <mat-icon [inline]="true">{{ dataSorter.sortingArrow }}</mat-icon>
                  }
                </th>
                <th class="sortable-column small-column" (click)="changeSort(stateSortData)" [matTooltip]="'nodes.state-tooltip' | translate">
                  <span class="dot-outline-gray"></span>
                  @if (dataSorter.currentSortingColumn === stateSortData) {
                    <mat-icon [inline]="true">{{ dataSorter.sortingArrow }}</mat-icon>
                  }
                </th>
                <th class="sortable-column labels" (click)="changeSort(labelSortData)">
                  {{ 'nodes.label' | translate }}
                  @if (dataSorter.currentSortingColumn === labelSortData) {
                    <mat-icon [inline]="true">{{ dataSorter.sortingArrow }}</mat-icon>
                  }
                </th>
                <th class="sortable-column ip-location-column" (click)="changeSort(ipLocationSortData)">
                  {{ 'nodes.ip-location' | translate }}
                  @if (dataSorter.currentSortingColumn === ipLocationSortData) {
                    <mat-icon [inline]="true">{{ dataSorter.sortingArrow }}</mat-icon>
                  }
                </th>
                <th class="sortable-column transports-column" (click)="changeSort(transportsCountSortData)">
                  {{ 'nodes.transports' | translate }}
                  @if (dataSorter.currentSortingColumn === transportsCountSortData) {
                    <mat-icon [inline]="true">{{ dataSorter.sortingArrow }}</mat-icon>
                  }
                </th>
                <th class="sortable-column dmsg-column" (click)="changeSort(dmsgServerCountSortData)">
                  {{ 'nodes.dmsg-servers' | translate }}
                  @if (dataSorter.currentSortingColumn === dmsgServerCountSortData) {
                    <mat-icon [inline]="true">{{ dataSorter.sortingArrow }}</mat-icon>
                  }
                </th>
                <th class="sortable-column key-column" (click)="changeSort(keySortData)">
                  {{ 'nodes.key' | translate }}
                  @if (dataSorter.currentSortingColumn === keySortData) {
                    <mat-icon [inline]="true">{{ dataSorter.sortingArrow }}</mat-icon>
                  }
                </th>
                <th class="sortable-column version-column" (click)="changeSort(versionSortData)">
                  {{ 'nodes.version' | translate }}
                  @if (dataSorter.currentSortingColumn === versionSortData) {
                    <mat-icon [inline]="true">{{ dataSorter.sortingArrow }}</mat-icon>
                  }
                </th>
                <th class="sortable-column services-column" (click)="changeSort(servicesCountSortData)">
                  {{ 'nodes.services' | translate }}
                  @if (dataSorter.currentSortingColumn === servicesCountSortData) {
                    <mat-icon [inline]="true">{{ dataSorter.sortingArrow }}</mat-icon>
                  }
                </th>
                @if (showApps) {
                  <th class="apps-column">{{ 'nodes.apps-filter.column' | translate }}</th>
                }
                <th class="sortable-column reward-column" (click)="changeSort(rewardAddressSortData)">
                  {{ 'nodes.reward' | translate }}
                  @if (dataSorter.currentSortingColumn === rewardAddressSortData) {
                    <mat-icon [inline]="true">{{ dataSorter.sortingArrow }}</mat-icon>
                  }
                </th>
                <th class="actions"></th>
              </tr>
              <!-- Values. cdkDrag makes the row draggable from its
                   handle column; clicks anywhere else still navigate
                   via routerLink. cdkDragHandle scopes the drag to
                   the icon so the rest of the row keeps its
                   click-to-navigate behavior. -->
              @for (node of dataSource; track node.localPk) {
                @let section = sectionOf(node.localPk);
                <a
                  [ngClass]="{'click-effect': node.online, 'non-selectable': !node.online, 'stale-row': node.isStale, 'wasm-visor-row': node.arch === 'wasm', 'compact-row': compact}"
                  class="selectable link-row"
                  [routerLink]="node.online ? ['/nodes', node.localPk] : null"
                  cdkDrag
                  [cdkDragData]="node"
                  >
                  <td class="select-column" (click)="$event.stopPropagation(); $event.preventDefault(); toggleSelected(node)">
                    <mat-checkbox [checked]="selected.has(node.localPk)" (click)="$event.preventDefault()"></mat-checkbox>
                  </td>
                  <td class="drag-handle-column" cdkDragHandle [matTooltip]="'nodes.drag-to-reorder' | translate" (click)="$event.stopPropagation(); $event.preventDefault();">
                    <mat-icon class="grey-text drag-handle-icon" [inline]="true">drag_indicator</mat-icon>
                  </td>
                  <ng-container *ngTemplateOutlet="rowCells; context: {$implicit: node, section: section, child: false}"></ng-container>
                </a>
                <!-- The visors a sub-hypervisor manages, indented under its row. -->
                @if (section && !collapsedSections.has(section.hypervisorPk)) {
                  @if (section.subError) {
                    <tr class="child-row">
                      <td></td><td></td><td></td><td></td>
                      <td class="label-cell child-label font-sm" [attr.colspan]="showApps ? 10 : 9">
                        <mat-icon [inline]="true" class="alert-icon">warning</mat-icon>
                        {{ 'nodes.subhypervisor-error' | translate }}: {{ section.subError }}
                      </td>
                    </tr>
                  }
                  @for (child of childNodes(section); track child.localPk) {
                    <a
                      [ngClass]="{'click-effect': child.online, 'non-selectable': !child.online, 'stale-row': child.isStale, 'wasm-visor-row': child.arch === 'wasm', 'compact-row': compact}"
                      class="selectable link-row child-row"
                      [routerLink]="child.online ? ['/nodes', child.localPk] : null"
                      >
                      <td class="select-column" (click)="$event.stopPropagation(); $event.preventDefault(); toggleSelected(child)">
                        <mat-checkbox [checked]="selected.has(child.localPk)" (click)="$event.preventDefault()"></mat-checkbox>
                      </td>
                      <td class="drag-handle-column"></td>
                      <ng-container *ngTemplateOutlet="rowCells; context: {$implicit: child, child: true, remote: true}"></ng-container>
                    </a>
                  }
                }
              }
            </table>
          }

          <!-- List for small screens. -->
          @if (dataSource.length > 0) {
            <table
              class="responsive-table-translucid d-md-none nowrap"
              cellspacing="0" cellpadding="0"
              >
              <!-- Sorting button. -->
              <tr class="selectable click-effect" (click)="dataSorter.openSortingOrderModal()"><td>
                <div class="list-item-container">
                  <div class="left-part">
                    <div class="title">{{ 'tables.sorting-title' | translate }}</div>
                    <div>{{ dataSorter.currentSortingColumn.label | translate }}
                      @if (dataSorter.currentlySortingByLabel) {
                        {{ 'tables.label' | translate }}
                      }
                      @if (dataSorter.sortingInReverseOrder) {
                        {{ 'tables.inverted-order' | translate }}
                      }
                    </div>
                  </div>
                  <div class="right-part">
                    <mat-icon [inline]="true">keyboard_arrow_down</mat-icon>
                  </div>
                </div>
              </td></tr>
              <!-- Values. -->
              @for (node of dataSource; track node.localPk) {
                @let section = sectionOf(node.localPk);
                <ng-container *ngTemplateOutlet="mobileItem; context: {$implicit: node, section: section, child: false}"></ng-container>
                @if (section && !collapsedSections.has(section.hypervisorPk)) {
                  @for (child of childNodes(section); track child.localPk) {
                    <ng-container *ngTemplateOutlet="mobileItem; context: {$implicit: child, child: true}"></ng-container>
                  }
                }
              }
              </table>
            }
          </div></div>
        }
        <!-- Lower paginator. -->
        @if (numberOfPages > 1) {
          <app-paginator
            [currentPage]="currentPage"
            [numberOfPages]="numberOfPages"
            [linkParts]="showRewardsInfo ? ['/nodes', 'rewards'] : ['/nodes', 'list']"
            [queryParams]="dataFilterer.currentUrlQueryParams">
          </app-paginator>
        }
        <!-- Msg shown if the list is empty. -->
        @if (dataSource.length === 0) {
          <div class="rounded-elevated-box mt-3">
            <div class="box-internal-container font-sm">
              <mat-icon [inline]="true" class="alert-icon">warning</mat-icon>
              @if (allNodes.length === 0) {
                <span class="font-sm">{{ 'nodes.empty' | translate }}</span>
              }
              @if (allNodes.length !== 0) {
                <span class="font-sm">{{ 'nodes.empty-with-filter' | translate }}</span>
              }
            </div>
          </div>
        }

        <!-- Sub-hypervisors with no row on this page (filtered out, on another
             page, or not managed by this hypervisor) keep a panel of their own.
             The rest are nested under their row in the list above. -->
        @if (detachedSections.length > 0) {
          @for (section of detachedSections; track section.hypervisorPk) {
            <div class="rounded-elevated-box mt-3">
              <div class="box-internal-container full-node-list-margins">
                <div class="generic-title-container">
                  <div class="title">
                    <div class="hypervisor-pk-label transparent-50" [matTooltip]="section.hypervisorPk">
                      <mat-icon class="small-icon" [inline]="true">star_outline</mat-icon>
                      <span class="ml-1">{{ section.hypervisorPk }}</span>
                    </div>
                  </div>
                </div>
                @if (section.subError) {
                  <div class="font-sm mt-2">
                    <mat-icon [inline]="true" class="alert-icon">warning</mat-icon>
                    <span class="font-sm ml-1">{{ 'nodes.subhypervisor-error' | translate }}: {{ section.subError }}</span>
                  </div>
                } @else if (!section.nodes || section.nodes.length === 0) {
                  <div class="font-sm mt-2">
                    <mat-icon [inline]="true" class="alert-icon">info_outline</mat-icon>
                    <span class="font-sm ml-1">{{ 'nodes.subhypervisor-empty' | translate }}</span>
                  </div>
                } @else {
                  <!-- Same shape as the main local-section table.
                       Sortable-column interactivity is intentionally
                       dropped: the local dataSorter is keyed against
                       allNodes/dataSource and reusing it would
                       cross-wire the sub-section's sort state with
                       the main table. Sub-sections are bounded
                       (one hypervisor's roster) so sorting is less
                       valuable here than visual parity is. -->
                  <table class="responsive-table-translucid d-none d-md-table" cellspacing="0" cellpadding="0">
                    <tr>
                      <!-- Empty placeholder column matching the main table's
                           drag-handle column. Sub-section tables don't
                           support drag-drop (the dataSorter is keyed against
                           the local section's dataSource), but the column
                           still has to exist so the columns to its right
                           line up vertically with the main table's. -->
                      <th class="select-column"></th>
                      <th class="drag-handle-column"></th>
                      <th class="small-column" [matTooltip]="'nodes.hypervisor' | translate">
                        <mat-icon class="hypervisor-icon grey-text">star_outline</mat-icon>
                      </th>
                      <th class="small-column" [matTooltip]="'nodes.state-tooltip' | translate">
                        <span class="dot-outline-gray"></span>
                      </th>
                      <th class="labels">{{ 'nodes.label' | translate }}</th>
                      <th class="ip-location-column">{{ 'nodes.ip-location' | translate }}</th>
                      <th class="transports-column">{{ 'nodes.transports' | translate }}</th>
                      <th class="dmsg-column">{{ 'nodes.dmsg-servers' | translate }}</th>
                      <th class="key-column">{{ 'nodes.key' | translate }}</th>
                      <th class="version-column">{{ 'nodes.version' | translate }}</th>
                      <th class="services-column">{{ 'nodes.services' | translate }}</th>
                      @if (showApps) {
                        <th class="apps-column">{{ 'nodes.apps-filter.column' | translate }}</th>
                      }
                      <th class="reward-column">{{ 'nodes.reward' | translate }}</th>
                      <th class="actions"></th>
                    </tr>
                    @for (node of quickFiltered(section.nodes); track node.localPk) {
                      <a
                        [ngClass]="{'click-effect': node.online, 'non-selectable': !node.online, 'stale-row': node.isStale, 'wasm-visor-row': node.arch === 'wasm', 'compact-row': compact}"
                        class="selectable link-row"
                        [routerLink]="node.online ? ['/nodes', node.localPk] : null"
                        >
                        <td class="select-column" (click)="$event.stopPropagation(); $event.preventDefault(); toggleSelected(node)">
                          <mat-checkbox [checked]="selected.has(node.localPk)" (click)="$event.preventDefault()"></mat-checkbox>
                        </td>
                        <td class="drag-handle-column"></td>
                        <ng-container *ngTemplateOutlet="rowCells; context: {$implicit: node, remote: true}"></ng-container>
                      </a>
                    }
                  </table>
                  <table class="responsive-table-translucid d-md-none nowrap" cellspacing="0" cellpadding="0">
                    @for (node of quickFiltered(section.nodes); track node.localPk) {
                      <ng-container *ngTemplateOutlet="mobileItem; context: {$implicit: node}"></ng-container>
                    }
                  </table>
                }
              </div>
            </div>
          }
        }

          <!-- The cells of one visor row, shared by the main list, the visors nested
               under a sub-hypervisor and the panels of sub-hypervisors not on this page. -->
          <ng-template #rowCells let-node let-section="section" let-child="child" let-remote="remote">
            <td>
              @if (isLocalHypervisor(node)) {
                <mat-icon class="hypervisor-icon" [inline]="true" [matTooltip]="'nodes.hypervisor-info' | translate">star</mat-icon>
              } @else if (section || node.isHypervisor) {
                <mat-icon class="hypervisor-icon attached" [inline]="true" [matTooltip]="'nodes.attached-hypervisor-info' | translate">star</mat-icon>
              }
            </td>
            <td>
              <span [class]="nodeStatusClass(node, true)" [matTooltip]="nodeStatusText(node, true) | translate"></span>
            </td>
            <td class="label-cell" [class.child-label]="child">
              @if (section && (section.subError || childNodes(section).length > 0)) {
                <button class="section-toggle" (click)="$event.stopPropagation(); $event.preventDefault(); toggleSection(section.hypervisorPk)"
                  [matTooltip]="(collapsedSections.has(section.hypervisorPk) ? 'nodes.show-managed' : 'nodes.hide-managed') | translate">
                  <mat-icon [inline]="true">{{ collapsedSections.has(section.hypervisorPk) ? 'chevron_right' : 'expand_more' }}</mat-icon>
                </button>
              }
              {{ node.label }}
              @if (section && (section.subError || childNodes(section).length > 0)) {
                <span class="managed-count" [matTooltip]="'nodes.managed-count' | translate">{{ childNodes(section).length }}</span>
              }
              @if (node.isStale) {
                <div class="stale-caption">{{ lastSeenText(node) }}</div>
              }
            </td>
            <td class="ip-cell">
              @if (compact && (node.ip || node.publicIp || node.countryCode)) {
                <div style="font-size: 0.85em;" [matTooltip]="[node.ip, node.publicIp].join(' ')">
                  {{ node.publicIp || node.ip }}@if (node.countryCode) { &middot; {{ node.cityName || node.countryCode }} }
                </div>
              } @else if (node.ip || node.publicIp || node.countryCode) {
                @if (node.ip && node.publicIp && node.ip !== node.publicIp) {
                  <div style="font-size: 0.85em;">{{ node.ip }}</div>
                  <div style="font-size: 0.85em;">{{ node.publicIp }}</div>
                } @else if (node.publicIp) {
                  <div style="font-size: 0.85em;">{{ node.publicIp }}</div>
                } @else if (node.ip) {
                  <div style="font-size: 0.85em;">{{ node.ip }}</div>
                }
                @if (node.countryCode) {
                  <div style="font-size: 0.85em;">{{ node.cityName ? node.cityName + ', ' : '' }}{{ node.regionName ? node.regionName + ', ' : '' }}{{ node.countryCode }}</div>
                }
              }
              @if (!node.ip && !node.publicIp && !node.countryCode) {
                <span>-</span>
              }
            </td>
            <td>
              @if (compact && node.transports && node.transports.length > 0) {
                @let ctotals = getTransportTotals(node);
                <span class="tp-compact" [matTooltip]="transportBreakdown(node)" matTooltipClass="multiline-tooltip">
                  {{ node.transports.length }}
                  @if (ctotals.incoming + ctotals.outgoing > 0) {
                    <span class="tp-in">{{ ctotals.incoming }}&#8595;</span>
                    <span class="tp-out">{{ ctotals.outgoing }}&#8593;</span>
                  }
                </span>
              } @else if (node.transports && node.transports.length > 0) {
                @let totals = getTransportTotals(node);
                <div class="tp-counts">
                  @for (entry of getTransportCounts(node); track entry.type) {
                    <span class="tp-type">{{ entry.type }}</span>
                    <span class="tp-count">{{ entry.count }}</span>
                    <span class="tp-in" title="incoming">{{ entry.incoming }}&#8595;</span>
                    <span class="tp-out" title="outgoing">{{ entry.outgoing }}&#8593;</span>
                  }
                  <span class="tp-type tp-total">Total</span>
                  <span class="tp-count tp-total">{{ node.transports.length }}</span>
                  @if (totals.incoming + totals.outgoing > 0) {
                    <span class="tp-in tp-total" title="incoming">{{ totals.incoming }}&#8595;</span>
                    <span class="tp-out tp-total" title="outgoing">{{ totals.outgoing }}&#8593;</span>
                  } @else {
                    <span class="tp-total"></span><span class="tp-total"></span>
                  }
                </div>
              }
              @if (node.transports && node.transports.length === 0) {
                <span style="font-size: 0.85em;">Total: 0</span>
              }
              @if (!node.transports) {
                <span>-</span>
              }
            </td>
            <td>
              @if (compact && getDmsgServerCount(node) > 0) {
                <span>{{ getDmsgServerCount(node) }}</span>
              } @else if (getDmsgServerCount(node) > 0) {
                <div class="dmsg-counts">
                  @for (entry of getDmsgCounts(node); track entry.type) {
                    <span class="dmsg-type">{{ entry.type }}</span>
                    <span class="dmsg-count">{{ entry.count }}</span>
                  }
                  <span class="dmsg-type dmsg-total">Total</span>
                  <span class="dmsg-count dmsg-total">{{ getDmsgServerCount(node) }}</span>
                </div>
              } @else {
                <span>-</span>
              }
            </td>
            <td class="key-cell">
              {{ node.localPk }}
            </td>
            <td>
              @if (compact) {
                <div style="font-size: 0.85em;" [class.outdated]="isOutdated(node)"
                  [matTooltip]="(node.version ? node.version + ' \xB7 ' : '') + (isOutdated(node) ? ('nodes.quick.outdated-tooltip' | translate) + ' ' : '') + ('nodes.config-label' | translate) + ': ' + (node.configVersion || '-')">
                  {{ (node.version | versionLabel) || '-' }}
                </div>
              } @else if (node.version && node.configVersion && node.version === node.configVersion) {
                <div style="font-size: 0.85em;" [matTooltip]="node.version">{{ node.version | versionLabel }}</div>
              } @else {
                <div style="font-size: 0.85em;" [matTooltip]="node.version">{{ 'nodes.visor-version' | translate }}: {{ (node.version | versionLabel) || '-' }}</div>
                <div style="font-size: 0.85em;">{{ 'nodes.config-label' | translate }}: {{ node.configVersion || '-' }}</div>
              }
            </td>
            <td>
              @if (compact && getNodeServices(node).length > 0) {
                <div style="font-size: 0.85em;">{{ getNodeServices(node).join(', ') }}</div>
              } @else if (getNodeServices(node).length > 0) {
                @for (service of getNodeServices(node); track service) {
                  <div style="font-size: 0.85em;">{{ service }}</div>
                }
              }
              @if (getNodeServices(node).length === 0) {
                <span>-</span>
              }
            </td>
            @if (showApps) {
              <td class="apps-cell">
                @for (chip of nodeAppChips(node); track chip.name) {
                  <span class="app-chip" [class.running]="chip.running">{{ chip.name }}</span>
                } @empty {
                  <span>-</span>
                }
              </td>
            }
            <td>
              @if (!node.rewardsAddress) {
                <span style="color: var(--sw-status-stopped); font-size: 20px;" [matTooltip]="'nodes.reward-not-set' | translate">&mdash;</span>
              } @else if (node.rewardEligible === false) {
                <mat-icon style="color: rgb(var(--sw-red-alt-rgb)); font-size: 20px;" [matTooltip]="node.rewardIneligibleReason || ('nodes.reward-rejected' | translate)">cancel</mat-icon>
              } @else {
                <mat-icon style="color: rgb(var(--sw-green-rgb)); font-size: 20px;" [matTooltip]="node.rewardsAddress">check_circle</mat-icon>
              }
            </td>
            <td class="actions">
              <button
                (click)="$event.stopPropagation(); $event.preventDefault(); copyToClipboard(node);"
                mat-button
                [matTooltip]="'nodes.copy-key' | translate"
                class="big-action-button transparent-button"
                >
                <mat-icon [inline]="true">filter_none</mat-icon>
              </button>
              <button
                (click)="$event.stopPropagation(); $event.preventDefault(); showEditLabelDialog(node);"
                mat-button
                [matTooltip]="'labeled-element.edit-label' | translate"
                class="big-action-button transparent-button"
                >
                <mat-icon [inline]="true">short_text</mat-icon>
              </button>
              @if (node.online) {
                <button
                  mat-button
                  [matTooltip]="'nodes.view-node' | translate"
                  class="big-action-button transparent-button"
                  >
                  <mat-icon [inline]="true">chevron_right</mat-icon>
                </button>
              }
              @if (!node.online && !remote) {
                <button
                  (click)="$event.stopPropagation(); $event.preventDefault(); deleteNode(node);"
                  mat-button
                  [matTooltip]="'nodes.delete-node' | translate"
                  class="big-action-button transparent-button"
                  >
                  <mat-icon>close</mat-icon>
                </button>
              }
            </td>
          </ng-template>

          <!-- One visor in the small-screen list, nested ones indented. -->
          <ng-template #mobileItem let-node let-section="section" let-child="child">
            <a class="link-row" [class.child-item]="child" [ngClass]="{'selectable click-effect': node.online, 'stale-row': node.isStale, 'wasm-visor-row': node.arch === 'wasm'}" [routerLink]="node.online ? ['/nodes', node.localPk] : null"><tr class="d-block"><td class="d-block">
              <div class="list-item-container">
                <div class="left-part">
                  <!-- Values only: the status dot, full key, label, place, version and
                       transport count say what they are without field names. -->
                  <div class="list-row m-key">
                    <span [class]="nodeStatusClass(node, true)" [matTooltip]="nodeStatusText(node, true) | translate"></span>
                    <span class="m-pk">{{ node.localPk }}</span>
                  </div>
                  <div class="list-row m-label">
                    @if (isLocalHypervisor(node)) {
                      <mat-icon class="hypervisor-icon" [inline]="true" [matTooltip]="'nodes.hypervisor-info' | translate">star</mat-icon>
                    } @else if (section || node.isHypervisor) {
                      <mat-icon class="hypervisor-icon attached" [inline]="true" [matTooltip]="'nodes.attached-hypervisor-info' | translate">star</mat-icon>
                    }
                    <span class="title">{{ node.label }}</span>
                    @if (node.isStale) {
                      <span class="stale-caption">&nbsp;({{ lastSeenText(node) }})</span>
                    }
                    @if (section && (section.subError || childNodes(section).length > 0)) {
                      <button class="section-toggle" (click)="$event.stopPropagation(); $event.preventDefault(); toggleSection(section.hypervisorPk)">
                        <mat-icon [inline]="true">{{ collapsedSections.has(section.hypervisorPk) ? 'chevron_right' : 'expand_more' }}</mat-icon>
                      </button>
                      <span class="managed-count">{{ childNodes(section).length }}</span>
                    }
                  </div>
                  @if (!compact && (node.ip || node.publicIp)) {
                    <div class="list-row m-sub">
                      {{ node.ip }}@if (node.ip && node.publicIp && node.ip !== node.publicIp) { / {{ node.publicIp }} } @else if (!node.ip) { {{ node.publicIp }} }
                    </div>
                  }
                  @if (node.countryCode) {
                    <div class="list-row m-sub">
                      {{ node.cityName ? node.cityName + ', ' : '' }}{{ node.regionName ? node.regionName + ', ' : '' }}{{ node.countryCode }}
                    </div>
                  }
                  <div class="list-row m-sub">
                    <span [class.outdated]="isOutdated(node)" [matTooltip]="node.version">{{ (node.version | versionLabel) || '-' }}</span>
                    @if (node.transports) {
                      @let mt = getTransportTotals(node);
                      <span class="m-tp" [matTooltip]="transportBreakdown(node)" matTooltipClass="multiline-tooltip">
                        <mat-icon [inline]="true">swap_horiz</mat-icon>{{ node.transports.length }}
                        @if (mt.incoming + mt.outgoing > 0) {
                          <span class="tp-in">{{ mt.incoming }}&#8595;</span>
                          <span class="tp-out">{{ mt.outgoing }}&#8593;</span>
                        }
                      </span>
                    }
                  </div>
                  @if (!compact && node.configVersion && node.configVersion !== node.version) {
                    <div class="list-row m-sub">
                      {{ 'nodes.config-label' | translate }}: {{ node.configVersion }}
                    </div>
                  }
                </div>
                  <div class="margin-part"></div>
                  <div class="right-part">
                    <button
                      (click)="$event.stopPropagation(); $event.preventDefault(); showOptionsDialog(node);"
                      mat-icon-button
                      [matTooltip]="'common.options' | translate"
                      class="transparent-button"
                      >
                      <mat-icon>{{ 'add' }}</mat-icon>
                    </button>
                  </div>
                </div>
              </td></tr></a>
          </ng-template>
      </div>
    </div>
  }
`;

// angular:jit:style:src/app/components/pages/node-list/node-list.component.scss
var node_list_component_default2 = '/* src/app/components/pages/node-list/node-list.component.scss */\n.hypervisor-pk-label {\n  display: inline-flex;\n  align-items: center;\n  font-family: monospace;\n  font-size: 12px;\n  max-width: 100%;\n  overflow: hidden;\n  white-space: nowrap;\n  text-overflow: ellipsis;\n}\n.hypervisor-pk-label span {\n  min-width: 0;\n  overflow: hidden;\n  text-overflow: ellipsis;\n}\n.responsive-table-translucid.d-md-table {\n  table-layout: fixed;\n  width: 100%;\n}\n.labels {\n  width: 10%;\n}\n.ip-location-column {\n  width: 10%;\n}\n.transports-column {\n  width: 13%;\n}\n.tp-counts {\n  display: inline-grid;\n  grid-template-columns: auto auto auto auto;\n  column-gap: 6px;\n  row-gap: 1px;\n  font-size: 0.85em;\n  line-height: 1.35;\n  text-align: right;\n  white-space: nowrap;\n}\n.tp-counts .tp-type {\n  text-align: left;\n}\n.tp-counts .tp-count {\n  opacity: 0.85;\n}\n.tp-counts .tp-in {\n  color: rgb(var(--sw-green-rgb));\n  font-weight: 500;\n}\n.tp-counts .tp-out {\n  color: rgb(var(--sw-sky-rgb));\n  font-weight: 500;\n}\n.tp-counts .tp-total {\n  padding-top: 2px;\n  border-top: 1px solid rgba(var(--sw-fg-rgb), 0.14);\n  font-weight: 600;\n}\n.dmsg-column {\n  width: 10%;\n}\n.dmsg-counts {\n  display: inline-grid;\n  grid-template-columns: auto auto;\n  column-gap: 6px;\n  row-gap: 1px;\n  font-size: 0.85em;\n  line-height: 1.35;\n  text-align: right;\n  white-space: nowrap;\n}\n.dmsg-counts .dmsg-type {\n  text-align: left;\n}\n.dmsg-counts .dmsg-count {\n  opacity: 0.85;\n}\n.dmsg-counts .dmsg-total {\n  padding-top: 2px;\n  border-top: 1px solid rgba(var(--sw-fg-rgb), 0.14);\n  font-weight: 600;\n}\n.key-column {\n  width: 21%;\n}\n.key-cell {\n  white-space: nowrap;\n  overflow: hidden;\n  text-overflow: ellipsis;\n  max-width: 0;\n}\n.version-column {\n  width: 15%;\n}\n.services-column {\n  width: 10%;\n}\n.apps-column {\n  width: 18%;\n}\n.reward-column {\n  width: 8%;\n}\n.actions {\n  text-align: right;\n  width: 120px;\n}\n.alert-icon {\n  vertical-align: middle;\n  margin-right: 10px;\n}\n.hypervisor-icon {\n  font-size: 14px !important;\n  line-height: 14px !important;\n  position: relative;\n  top: 2px;\n  margin-left: 2px;\n  color: var(--sw-yellow);\n}\n.hypervisor-icon.attached {\n  color: rgba(var(--sw-fg-rgb), 0.55);\n}\n.attached-hypervisor-text {\n  color: rgba(var(--sw-fg-rgb), 0.6);\n}\n.small-column {\n  width: 1px;\n}\n.drag-handle-column {\n  width: 24px;\n  cursor: grab;\n  padding-left: 4px;\n  padding-right: 4px;\n}\n.drag-handle-icon {\n  opacity: 0.5;\n}\n.drag-handle-icon:hover {\n  opacity: 1;\n}\n.cdk-drag-preview {\n  box-sizing: border-box;\n  border-radius: 4px;\n  box-shadow:\n    0 5px 5px -3px rgba(var(--sw-shade-rgb), 0.3),\n    0 8px 10px 1px rgba(var(--sw-shade-rgb), 0.2),\n    0 3px 14px 2px rgba(var(--sw-shade-rgb), 0.12);\n  background: rgba(20, 30, 50, 0.95);\n}\n.cdk-drag-placeholder {\n  opacity: 0.3;\n}\n.cdk-drag-animating {\n  transition: transform 250ms cubic-bezier(0, 0, 0.2, 1);\n}\n.non-selectable {\n  cursor: not-allowed;\n}\n.reward-day-column {\n  text-align: center;\n  white-space: nowrap;\n  min-width: 60px;\n}\n.reward-total-column {\n  text-align: center;\n  white-space: nowrap;\n  min-width: 70px;\n}\n.reward-sent {\n  color: rgb(var(--sw-green-rgb));\n}\n.reward-pending {\n  color: rgb(var(--sw-orange-rgb));\n}\n.stale-row {\n  opacity: 0.55;\n}\n.stale-caption {\n  font-size: 0.75em;\n  color: var(--sw-status-stopped);\n  font-style: italic;\n}\n.dot-red.dimmed,\n.red-text.dimmed {\n  opacity: 0.6;\n}\n.wasm-visor-row,\n.wasm-visor-row tr,\n.wasm-visor-row td {\n  background-color: rgba(140, 110, 205, 0.13) !important;\n}\n.tp-counts,\n.dmsg-counts {\n  max-width: 100%;\n  overflow: hidden;\n}\n@media (max-width: 1400px) {\n  .tp-counts,\n  .dmsg-counts {\n    font-size: 0.78em;\n    column-gap: 4px;\n  }\n}\n@media (max-width: 1100px) {\n  .tp-counts,\n  .dmsg-counts {\n    font-size: 0.72em;\n    column-gap: 3px;\n  }\n}\n.fleet-toolbar {\n  display: flex;\n  flex-wrap: wrap;\n  align-items: center;\n  gap: 8px;\n  margin-top: 12px;\n}\n.fleet-toolbar .fleet-search {\n  flex: 1;\n  min-width: 220px;\n  max-width: 420px;\n  background: rgba(var(--sw-shade-rgb), 0.25);\n  color: inherit;\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.25);\n  border-radius: 4px;\n  padding: 6px 10px;\n  font: inherit;\n}\n@media (max-width: 767px) {\n  .fleet-toolbar .fleet-search {\n    flex-basis: 100%;\n    max-width: none;\n  }\n}\n.fleet-toolbar .fleet-chip {\n  background: transparent;\n  color: rgba(var(--sw-fg-rgb), 0.85);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.3);\n  border-radius: 14px;\n  padding: 3px 12px;\n  font: inherit;\n  font-size: 0.85em;\n  cursor: pointer;\n}\n.fleet-toolbar .fleet-chip.on {\n  background: rgba(var(--sw-fg-rgb), 0.85);\n  color: var(--sw-on-fg);\n}\n.fleet-toolbar .fleet-app-filter {\n  font-size: 0.85em;\n  color: rgba(var(--sw-fg-rgb), 0.85);\n  white-space: nowrap;\n}\n.fleet-toolbar .fleet-select {\n  background: var(--sw-control-bg);\n  color: rgba(var(--sw-fg-rgb), 0.92);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.3);\n  border-radius: 4px;\n  padding: 3px 6px;\n  font: inherit;\n  margin-left: 4px;\n}\n.fleet-toolbar .fleet-hidden {\n  font-size: 0.85em;\n  color: rgba(var(--sw-fg-rgb), 0.7);\n}\n.fleet-toolbar .fleet-spacer {\n  flex: 1;\n}\n.fleet-bulk {\n  display: flex;\n  flex-wrap: wrap;\n  align-items: center;\n  gap: 8px;\n  margin-top: 8px;\n  padding: 6px 12px;\n  border-radius: 6px;\n  background: rgba(var(--sw-sky-rgb), 0.18);\n}\n.fleet-bulk .mat-mdc-outlined-button {\n  color: rgba(var(--sw-fg-rgb), 0.92);\n  border-color: rgba(var(--sw-fg-rgb), 0.35);\n}\n.select-column {\n  width: 36px;\n}\n.compact-row td {\n  padding-top: 4px !important;\n  padding-bottom: 4px !important;\n}\n.tp-compact {\n  font-size: 0.85em;\n  white-space: nowrap;\n}\n.tp-compact .tp-in {\n  color: rgb(var(--sw-green-rgb));\n  margin-left: 4px;\n}\n.tp-compact .tp-out {\n  color: rgb(var(--sw-sky-rgb));\n  margin-left: 2px;\n}\n.outdated {\n  color: rgb(var(--sw-amber-rgb));\n}\n.apps-cell {\n  line-height: 1.6;\n}\n.app-chip {\n  display: inline-block;\n  font-size: 0.75em;\n  padding: 0 6px;\n  margin: 1px 3px 1px 0;\n  border-radius: 8px;\n  color: rgba(var(--sw-fg-rgb), 0.7);\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.25);\n}\n.app-chip.running {\n  color: rgb(var(--sw-mint-rgb));\n  border-color: rgba(var(--sw-green-rgb), 0.7);\n  background: rgba(var(--sw-green-rgb), 0.15);\n}\n.child-label {\n  position: relative;\n  padding-left: 28px !important;\n}\n.child-label::before {\n  content: "";\n  position: absolute;\n  left: 10px;\n  top: 0;\n  bottom: 50%;\n  width: 12px;\n  border-left: 1px solid rgba(var(--sw-fg-rgb), 0.3);\n  border-bottom: 1px solid rgba(var(--sw-fg-rgb), 0.3);\n}\n.section-toggle {\n  background: none;\n  border: none;\n  padding: 0;\n  margin-right: 2px;\n  color: inherit;\n  cursor: pointer;\n  vertical-align: middle;\n}\n.managed-count {\n  display: inline-block;\n  font-size: 0.75em;\n  padding: 0 6px;\n  margin-left: 6px;\n  border-radius: 8px;\n  border: 1px solid rgba(var(--sw-fg-rgb), 0.3);\n  color: rgba(var(--sw-fg-rgb), 0.7);\n}\n.child-item {\n  display: block;\n  margin-left: 16px;\n  border-left: 2px solid rgba(var(--sw-fg-rgb), 0.2);\n}\n.m-key {\n  display: flex;\n  align-items: flex-start;\n  gap: 6px;\n}\n.m-key > span:first-child {\n  margin-top: 3px;\n  flex-shrink: 0;\n}\n.m-key .m-pk {\n  font-family: monospace;\n  font-size: 0.75em;\n  white-space: normal;\n  word-break: break-all;\n  opacity: 0.8;\n}\n.m-label .title {\n  font-weight: 600;\n}\n.m-sub {\n  font-size: 0.85em;\n  white-space: normal;\n}\n.m-tp {\n  margin-left: 10px;\n  white-space: nowrap;\n}\n.m-tp mat-icon {\n  vertical-align: middle;\n  margin-right: 2px;\n}\n.m-tp .tp-in {\n  color: rgb(var(--sw-green-rgb));\n  margin-left: 4px;\n}\n.m-tp .tp-out {\n  color: rgb(var(--sw-sky-rgb));\n  margin-left: 2px;\n}\n/*# sourceMappingURL=node-list.component.css.map */\n';

// node_modules/@angular/cdk/fesm2022/drag-drop.mjs
function deepCloneNode(node) {
  const clone = node.cloneNode(true);
  const descendantsWithId = clone.querySelectorAll("[id]");
  const nodeName = node.nodeName.toLowerCase();
  clone.removeAttribute("id");
  for (let i = 0; i < descendantsWithId.length; i++) {
    descendantsWithId[i].removeAttribute("id");
  }
  if (nodeName === "canvas") {
    transferCanvasData(node, clone);
  } else if (nodeName === "input" || nodeName === "select" || nodeName === "textarea") {
    transferInputData(node, clone);
  }
  transferData("canvas", node, clone, transferCanvasData);
  transferData("input, textarea, select", node, clone, transferInputData);
  return clone;
}
function transferData(selector, node, clone, callback) {
  const descendantElements = node.querySelectorAll(selector);
  if (descendantElements.length) {
    const cloneElements = clone.querySelectorAll(selector);
    for (let i = 0; i < descendantElements.length; i++) {
      callback(descendantElements[i], cloneElements[i]);
    }
  }
}
var cloneUniqueId = 0;
function transferInputData(source, clone) {
  if (clone.type !== "file") {
    clone.value = source.value;
  }
  if (clone.type === "radio" && clone.name) {
    clone.name = `mat-clone-${clone.name}-${cloneUniqueId++}`;
  }
}
function transferCanvasData(source, clone) {
  const context = clone.getContext("2d");
  if (context) {
    try {
      context.drawImage(source, 0, 0);
    } catch (e) {
    }
  }
}
function getMutableClientRect(element) {
  const rect = element.getBoundingClientRect();
  return {
    top: rect.top,
    right: rect.right,
    bottom: rect.bottom,
    left: rect.left,
    width: rect.width,
    height: rect.height,
    x: rect.x,
    y: rect.y
  };
}
function isInsideClientRect(clientRect, x, y) {
  const {
    top,
    bottom,
    left,
    right
  } = clientRect;
  return y >= top && y <= bottom && x >= left && x <= right;
}
function isOverflowingParent(parentRect, childRect) {
  const isLeftOverflowing = childRect.left < parentRect.left;
  const isRightOverflowing = childRect.left + childRect.width > parentRect.right;
  const isTopOverflowing = childRect.top < parentRect.top;
  const isBottomOverflowing = childRect.top + childRect.height > parentRect.bottom;
  return isLeftOverflowing || isRightOverflowing || isTopOverflowing || isBottomOverflowing;
}
function adjustDomRect(domRect, top, left) {
  domRect.top += top;
  domRect.bottom = domRect.top + domRect.height;
  domRect.left += left;
  domRect.right = domRect.left + domRect.width;
}
function isPointerNearDomRect(rect, threshold, pointerX, pointerY) {
  const {
    top,
    right,
    bottom,
    left,
    width,
    height
  } = rect;
  const xThreshold = width * threshold;
  const yThreshold = height * threshold;
  return pointerY > top - yThreshold && pointerY < bottom + yThreshold && pointerX > left - xThreshold && pointerX < right + xThreshold;
}
var ParentPositionTracker = class {
  _document;
  positions = /* @__PURE__ */ new Map();
  constructor(_document) {
    this._document = _document;
  }
  clear() {
    this.positions.clear();
  }
  cache(elements) {
    this.clear();
    this.positions.set(this._document, {
      scrollPosition: this.getViewportScrollPosition()
    });
    elements.forEach((element) => {
      this.positions.set(element, {
        scrollPosition: {
          top: element.scrollTop,
          left: element.scrollLeft
        },
        clientRect: getMutableClientRect(element)
      });
    });
  }
  handleScroll(event) {
    const target = _getEventTarget(event);
    const cachedPosition = this.positions.get(target);
    if (!cachedPosition) {
      return null;
    }
    const scrollPosition = cachedPosition.scrollPosition;
    let newTop;
    let newLeft;
    if (target === this._document) {
      const viewportScrollPosition = this.getViewportScrollPosition();
      newTop = viewportScrollPosition.top;
      newLeft = viewportScrollPosition.left;
    } else {
      newTop = target.scrollTop;
      newLeft = target.scrollLeft;
    }
    const topDifference = scrollPosition.top - newTop;
    const leftDifference = scrollPosition.left - newLeft;
    this.positions.forEach((position, node) => {
      if (position.clientRect && target !== node && target.contains(node)) {
        adjustDomRect(position.clientRect, topDifference, leftDifference);
      }
    });
    scrollPosition.top = newTop;
    scrollPosition.left = newLeft;
    return {
      top: topDifference,
      left: leftDifference
    };
  }
  getViewportScrollPosition() {
    return {
      top: window.scrollY,
      left: window.scrollX
    };
  }
};
function getRootNode(viewRef, _document) {
  const rootNodes = viewRef.rootNodes;
  if (rootNodes.length === 1 && rootNodes[0].nodeType === _document.ELEMENT_NODE) {
    return rootNodes[0];
  }
  const wrapper = _document.createElement("div");
  rootNodes.forEach((node) => wrapper.appendChild(node));
  return wrapper;
}
function extendStyles(dest, source, importantProperties2) {
  for (let key in source) {
    if (Object.hasOwn(source, key)) {
      const value = source[key];
      if (value) {
        dest.setProperty(key, value, importantProperties2?.has(key) ? "important" : "");
      } else {
        dest.removeProperty(key);
      }
    }
  }
  return dest;
}
function toggleNativeDragInteractions(element, enable) {
  const userSelect = enable ? "" : "none";
  extendStyles(element.style, {
    "touch-action": enable ? "" : "none",
    "-webkit-user-drag": enable ? "" : "none",
    "-webkit-tap-highlight-color": enable ? "" : "transparent",
    "user-select": userSelect,
    "-ms-user-select": userSelect,
    "-webkit-user-select": userSelect,
    "-moz-user-select": userSelect
  });
}
function toggleVisibility(element, enable, importantProperties2) {
  extendStyles(element.style, {
    position: enable ? "" : "fixed",
    top: enable ? "" : "0",
    opacity: enable ? "" : "0",
    left: enable ? "" : "-999em"
  }, importantProperties2);
}
function combineTransforms(transform, initialTransform) {
  return initialTransform && initialTransform != "none" ? transform + " " + initialTransform : transform;
}
function matchElementSize(target, sourceRect) {
  target.style.width = `${sourceRect.width}px`;
  target.style.height = `${sourceRect.height}px`;
  target.style.transform = getTransform(sourceRect.left, sourceRect.top);
}
function getTransform(x, y) {
  return `translate3d(${Math.round(x)}px, ${Math.round(y)}px, 0)`;
}
var capturingEventOptions = {
  capture: true
};
var activeCapturingEventOptions$1 = {
  passive: false,
  capture: true
};
var _ResetsLoader = class __ResetsLoader {
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.0",
    ngImport: core_exports,
    type: __ResetsLoader,
    deps: [],
    target: FactoryTarget.Component
  });
  static \u0275cmp = \u0275\u0275ngDeclareComponent({
    minVersion: "14.0.0",
    version: "22.2.0",
    type: __ResetsLoader,
    isStandalone: true,
    selector: "ng-component",
    host: {
      attributes: {
        "cdk-drag-resets-container": ""
      }
    },
    ngImport: core_exports,
    template: "",
    isInline: true,
    styles: ["@layer cdk-resets {\n  .cdk-drag-preview {\n    background: none;\n    border: none;\n    padding: 0;\n    color: inherit;\n    overflow: visible;\n    inset: auto;\n  }\n}\n.cdk-drag-placeholder *,\n.cdk-drag-preview * {\n  pointer-events: none !important;\n}\n"],
    encapsulation: ViewEncapsulation.None
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.0",
  ngImport: core_exports,
  type: _ResetsLoader,
  decorators: [{
    type: Component,
    args: [{
      encapsulation: ViewEncapsulation.None,
      template: "",
      host: {
        "cdk-drag-resets-container": ""
      },
      styles: ["@layer cdk-resets {\n  .cdk-drag-preview {\n    background: none;\n    border: none;\n    padding: 0;\n    color: inherit;\n    overflow: visible;\n    inset: auto;\n  }\n}\n.cdk-drag-placeholder *,\n.cdk-drag-preview * {\n  pointer-events: none !important;\n}\n"]
    }]
  }]
});
var DragDropRegistry = class _DragDropRegistry {
  _ngZone = inject(NgZone);
  _document = inject(DOCUMENT);
  _styleLoader = inject(_CdkPrivateStyleLoader);
  _renderer = inject(RendererFactory2).createRenderer(null, null);
  _cleanupDocumentTouchmove;
  _scroll = new Subject();
  _dropInstances = /* @__PURE__ */ new Set();
  _dragInstances = /* @__PURE__ */ new Set();
  _activeDragInstances = signal([], ...ngDevMode ? [{
    debugName: "_activeDragInstances"
  }] : []);
  _globalListeners;
  _draggingPredicate = (item) => item.isDragging();
  _domNodesToDirectives = null;
  pointerMove = new Subject();
  pointerUp = new Subject();
  registerDropContainer(drop) {
    if (!this._dropInstances.has(drop)) {
      this._dropInstances.add(drop);
    }
  }
  registerDragItem(drag) {
    this._dragInstances.add(drag);
    if (this._dragInstances.size === 1) {
      this._ngZone.runOutsideAngular(() => {
        this._cleanupDocumentTouchmove?.();
        this._cleanupDocumentTouchmove = this._renderer.listen(this._document, "touchmove", this._persistentTouchmoveListener, activeCapturingEventOptions$1);
      });
    }
  }
  removeDropContainer(drop) {
    this._dropInstances.delete(drop);
  }
  removeDragItem(drag) {
    this._dragInstances.delete(drag);
    this.stopDragging(drag);
    if (this._dragInstances.size === 0) {
      this._cleanupDocumentTouchmove?.();
    }
  }
  startDragging(drag, event) {
    if (this._activeDragInstances().indexOf(drag) > -1) {
      return;
    }
    this._styleLoader.load(_ResetsLoader);
    this._activeDragInstances.update((instances) => [...instances, drag]);
    if (this._activeDragInstances().length === 1) {
      const isTouchEvent2 = event.type.startsWith("touch");
      const endEventHandler = (e) => this.pointerUp.next(e);
      const toBind = [["scroll", (e) => this._scroll.next(e), capturingEventOptions], ["selectstart", this._preventDefaultWhileDragging, activeCapturingEventOptions$1]];
      if (isTouchEvent2) {
        toBind.push(["touchend", endEventHandler, capturingEventOptions], ["touchcancel", endEventHandler, capturingEventOptions]);
      } else {
        toBind.push(["mouseup", endEventHandler, capturingEventOptions]);
      }
      if (!isTouchEvent2) {
        toBind.push(["mousemove", (e) => this.pointerMove.next(e), activeCapturingEventOptions$1]);
      }
      this._ngZone.runOutsideAngular(() => {
        this._globalListeners = toBind.map(([name, handler, options]) => this._renderer.listen(this._document, name, handler, options));
      });
    }
  }
  stopDragging(drag) {
    this._activeDragInstances.update((instances) => {
      const index = instances.indexOf(drag);
      if (index > -1) {
        instances.splice(index, 1);
        return [...instances];
      }
      return instances;
    });
    if (this._activeDragInstances().length === 0) {
      this._clearGlobalListeners();
    }
  }
  isDragging(drag) {
    return this._activeDragInstances().indexOf(drag) > -1;
  }
  scrolled(shadowRoot) {
    const streams = [this._scroll];
    if (shadowRoot && shadowRoot !== this._document) {
      streams.push(new Observable((observer) => {
        return this._ngZone.runOutsideAngular(() => {
          const cleanup = this._renderer.listen(shadowRoot, "scroll", (event) => {
            if (this._activeDragInstances().length) {
              observer.next(event);
            }
          }, capturingEventOptions);
          return () => {
            cleanup();
          };
        });
      }));
    }
    return merge(...streams);
  }
  registerDirectiveNode(node, dragRef) {
    this._domNodesToDirectives ??= /* @__PURE__ */ new WeakMap();
    this._domNodesToDirectives.set(node, dragRef);
  }
  removeDirectiveNode(node) {
    this._domNodesToDirectives?.delete(node);
  }
  getDragDirectiveForNode(node) {
    return this._domNodesToDirectives?.get(node) || null;
  }
  ngOnDestroy() {
    this._dragInstances.forEach((instance) => this.removeDragItem(instance));
    this._dropInstances.forEach((instance) => this.removeDropContainer(instance));
    this._domNodesToDirectives = null;
    this._clearGlobalListeners();
    this.pointerMove.complete();
    this.pointerUp.complete();
  }
  _preventDefaultWhileDragging = (event) => {
    if (this._activeDragInstances().length > 0) {
      event.preventDefault();
    }
  };
  _persistentTouchmoveListener = (event) => {
    if (this._activeDragInstances().length > 0) {
      if (this._activeDragInstances().some(this._draggingPredicate)) {
        event.preventDefault();
      }
      this.pointerMove.next(event);
    }
  };
  _clearGlobalListeners() {
    this._globalListeners?.forEach((cleanup) => cleanup());
    this._globalListeners = void 0;
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.0",
    ngImport: core_exports,
    type: _DragDropRegistry,
    deps: [],
    target: FactoryTarget.Service
  });
  static \u0275prov = \u0275\u0275ngDeclareService({
    minVersion: "22.0.0",
    version: "22.2.0",
    ngImport: core_exports,
    type: _DragDropRegistry
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.0",
  ngImport: core_exports,
  type: DragDropRegistry,
  decorators: [{
    type: Service
  }]
});
function parseCssTimeUnitsToMs(value) {
  const multiplier = value.toLowerCase().indexOf("ms") > -1 ? 1 : 1e3;
  return parseFloat(value) * multiplier;
}
function getTransformTransitionDurationInMs(element) {
  const computedStyle = getComputedStyle(element);
  const transitionedProperties = parseCssPropertyValue(computedStyle, "transition-property");
  const property = transitionedProperties.find((prop) => prop === "transform" || prop === "all");
  if (!property) {
    return 0;
  }
  const propertyIndex = transitionedProperties.indexOf(property);
  const rawDurations = parseCssPropertyValue(computedStyle, "transition-duration");
  const rawDelays = parseCssPropertyValue(computedStyle, "transition-delay");
  return parseCssTimeUnitsToMs(rawDurations[propertyIndex]) + parseCssTimeUnitsToMs(rawDelays[propertyIndex]);
}
function parseCssPropertyValue(computedStyle, name) {
  const value = computedStyle.getPropertyValue(name);
  return value.split(",").map((part) => part.trim());
}
var importantProperties = /* @__PURE__ */ new Set(["position"]);
var PreviewRef = class {
  _document;
  _rootElement;
  _direction;
  _initialDomRect;
  _previewTemplate;
  _previewClass;
  _pickupPositionOnPage;
  _initialTransform;
  _zIndex;
  _renderer;
  _previewEmbeddedView = null;
  _preview;
  get element() {
    return this._preview;
  }
  constructor(_document, _rootElement, _direction, _initialDomRect, _previewTemplate, _previewClass, _pickupPositionOnPage, _initialTransform, _zIndex, _renderer) {
    this._document = _document;
    this._rootElement = _rootElement;
    this._direction = _direction;
    this._initialDomRect = _initialDomRect;
    this._previewTemplate = _previewTemplate;
    this._previewClass = _previewClass;
    this._pickupPositionOnPage = _pickupPositionOnPage;
    this._initialTransform = _initialTransform;
    this._zIndex = _zIndex;
    this._renderer = _renderer;
  }
  attach(parent) {
    this._preview = this._createPreview();
    parent.appendChild(this._preview);
    if (supportsPopover(this._preview)) {
      this._preview["showPopover"]();
    }
  }
  destroy() {
    this._preview.remove();
    this._previewEmbeddedView?.destroy();
    this._preview = this._previewEmbeddedView = null;
  }
  setTransform(value) {
    this._preview.style.transform = value;
  }
  getBoundingClientRect() {
    return this._preview.getBoundingClientRect();
  }
  addClass(className) {
    this._preview.classList.add(className);
  }
  getTransitionDuration() {
    return getTransformTransitionDurationInMs(this._preview);
  }
  addEventListener(name, handler) {
    return this._renderer.listen(this._preview, name, handler);
  }
  _createPreview() {
    const previewConfig = this._previewTemplate;
    const previewClass = this._previewClass;
    const previewTemplate = previewConfig ? previewConfig.template : null;
    let preview;
    if (previewTemplate && previewConfig) {
      const rootRect = previewConfig.matchSize ? this._initialDomRect : null;
      const viewRef = previewConfig.viewContainer.createEmbeddedView(previewTemplate, previewConfig.context);
      viewRef.detectChanges();
      preview = getRootNode(viewRef, this._document);
      this._previewEmbeddedView = viewRef;
      if (previewConfig.matchSize) {
        matchElementSize(preview, rootRect);
      } else {
        preview.style.transform = getTransform(this._pickupPositionOnPage.x, this._pickupPositionOnPage.y);
      }
    } else {
      preview = deepCloneNode(this._rootElement);
      matchElementSize(preview, this._initialDomRect);
      if (this._initialTransform) {
        preview.style.transform = this._initialTransform;
      }
    }
    extendStyles(preview.style, {
      "pointer-events": "none",
      "margin": supportsPopover(preview) ? "0 auto 0 0" : "0",
      "position": "fixed",
      "top": "0",
      "left": "0",
      "z-index": this._zIndex + ""
    }, importantProperties);
    toggleNativeDragInteractions(preview, false);
    preview.classList.add("cdk-drag-preview");
    preview.setAttribute("popover", "manual");
    preview.setAttribute("dir", this._direction);
    if (previewClass) {
      if (Array.isArray(previewClass)) {
        previewClass.forEach((className) => preview.classList.add(className));
      } else {
        preview.classList.add(previewClass);
      }
    }
    return preview;
  }
};
function supportsPopover(element) {
  return "showPopover" in element;
}
var passiveEventListenerOptions = {
  passive: true
};
var activeEventListenerOptions = {
  passive: false
};
var activeCapturingEventOptions = {
  passive: false,
  capture: true
};
var MOUSE_EVENT_IGNORE_TIME = 800;
var PLACEHOLDER_CLASS = "cdk-drag-placeholder";
var dragImportantProperties = /* @__PURE__ */ new Set(["position"]);
function createDragRef(injector, element, config = {
  dragStartThreshold: 5,
  pointerDirectionChangeThreshold: 5
}) {
  const renderer = injector.get(Renderer2, null, {
    optional: true
  }) || injector.get(RendererFactory2).createRenderer(null, null);
  return new DragRef(element, config, injector.get(DOCUMENT), injector.get(NgZone), injector.get(ViewportRuler), injector.get(DragDropRegistry), renderer);
}
var DragRef = class {
  _config;
  _document;
  _ngZone;
  _viewportRuler;
  _dragDropRegistry;
  _renderer;
  _rootElementCleanups;
  _cleanupShadowRootSelectStart;
  _preview = null;
  _previewContainer;
  _placeholderRef = null;
  _placeholder;
  _pickupPositionInElement;
  _pickupPositionOnPage;
  _marker;
  _anchor = null;
  _passiveTransform = {
    x: 0,
    y: 0
  };
  _activeTransform = {
    x: 0,
    y: 0
  };
  _initialTransform;
  _hasStartedDragging = signal(false, ...ngDevMode ? [{
    debugName: "_hasStartedDragging"
  }] : []);
  _hasMoved = false;
  _initialContainer;
  _initialIndex;
  _parentPositions;
  _moveEvents = new Subject();
  _pointerDirectionDelta;
  _pointerPositionAtLastDirectionChange;
  _lastKnownPointerPosition;
  _rootElement;
  _ownerSVGElement = null;
  _rootElementTapHighlight;
  _pointerMoveSubscription = Subscription.EMPTY;
  _pointerUpSubscription = Subscription.EMPTY;
  _scrollSubscription = Subscription.EMPTY;
  _resizeSubscription = Subscription.EMPTY;
  _lastTouchEventTime;
  _dragStartTime;
  _boundaryElement = null;
  _nativeInteractionsEnabled = true;
  _initialDomRect;
  _previewRect;
  _boundaryRect;
  _previewTemplate;
  _placeholderTemplate;
  _handles = [];
  _disabledHandles = /* @__PURE__ */ new Set();
  _dropContainer;
  _direction = "ltr";
  _parentDragRef = null;
  _cachedShadowRoot;
  lockAxis = null;
  dragStartDelay = 0;
  previewClass;
  scale = 1;
  get disabled() {
    return this._disabled || !!(this._dropContainer && this._dropContainer.disabled);
  }
  set disabled(value) {
    if (value !== this._disabled) {
      this._disabled = value;
      this._toggleNativeDragInteractions();
      this._handles.forEach((handle) => toggleNativeDragInteractions(handle, value));
    }
  }
  _disabled = false;
  beforeStarted = new Subject();
  started = new Subject();
  released = new Subject();
  ended = new Subject();
  entered = new Subject();
  exited = new Subject();
  dropped = new Subject();
  moved = this._moveEvents;
  data;
  constrainPosition;
  constructor(element, _config, _document, _ngZone, _viewportRuler, _dragDropRegistry, _renderer) {
    this._config = _config;
    this._document = _document;
    this._ngZone = _ngZone;
    this._viewportRuler = _viewportRuler;
    this._dragDropRegistry = _dragDropRegistry;
    this._renderer = _renderer;
    this.withRootElement(element).withParent(_config.parentDragRef || null);
    this._parentPositions = new ParentPositionTracker(_document);
    _dragDropRegistry.registerDragItem(this);
  }
  getPlaceholderElement() {
    return this._placeholder;
  }
  getRootElement() {
    return this._rootElement;
  }
  getVisibleElement() {
    return this.isDragging() ? this.getPlaceholderElement() : this.getRootElement();
  }
  withHandles(handles) {
    this._handles = handles.map((handle) => coerceElement(handle));
    this._handles.forEach((handle) => toggleNativeDragInteractions(handle, this.disabled));
    this._toggleNativeDragInteractions();
    const disabledHandles = /* @__PURE__ */ new Set();
    this._disabledHandles.forEach((handle) => {
      if (this._handles.indexOf(handle) > -1) {
        disabledHandles.add(handle);
      }
    });
    this._disabledHandles = disabledHandles;
    return this;
  }
  withPreviewTemplate(template) {
    this._previewTemplate = template;
    return this;
  }
  withPlaceholderTemplate(template) {
    this._placeholderTemplate = template;
    return this;
  }
  withRootElement(rootElement) {
    const element = coerceElement(rootElement);
    if (element !== this._rootElement) {
      this._removeRootElementListeners();
      const renderer = this._renderer;
      this._rootElementCleanups = this._ngZone.runOutsideAngular(() => [renderer.listen(element, "mousedown", this._pointerDown, activeEventListenerOptions), renderer.listen(element, "touchstart", this._pointerDown, passiveEventListenerOptions), renderer.listen(element, "dragstart", this._nativeDragStart, activeEventListenerOptions)]);
      this._initialTransform = void 0;
      this._rootElement = element;
    }
    if (typeof SVGElement !== "undefined" && this._rootElement instanceof SVGElement) {
      this._ownerSVGElement = this._rootElement.ownerSVGElement;
    }
    return this;
  }
  withBoundaryElement(boundaryElement) {
    this._boundaryElement = boundaryElement ? coerceElement(boundaryElement) : null;
    this._resizeSubscription.unsubscribe();
    if (boundaryElement) {
      this._resizeSubscription = this._viewportRuler.change(10).subscribe(() => this._containInsideBoundaryOnResize());
    }
    return this;
  }
  withParent(parent) {
    this._parentDragRef = parent;
    return this;
  }
  dispose() {
    this._removeRootElementListeners();
    if (this.isDragging()) {
      this._rootElement?.remove();
    }
    this._marker?.remove();
    this._destroyPreview();
    this._destroyPlaceholder();
    this._dragDropRegistry.removeDragItem(this);
    this._removeListeners();
    this.beforeStarted.complete();
    this.started.complete();
    this.released.complete();
    this.ended.complete();
    this.entered.complete();
    this.exited.complete();
    this.dropped.complete();
    this._moveEvents.complete();
    this._handles = [];
    this._disabledHandles.clear();
    this._dropContainer = void 0;
    this._resizeSubscription.unsubscribe();
    this._parentPositions.clear();
    this._boundaryElement = this._rootElement = this._ownerSVGElement = this._placeholderTemplate = this._previewTemplate = this._marker = this._parentDragRef = null;
  }
  isDragging() {
    return this._hasStartedDragging() && this._dragDropRegistry.isDragging(this);
  }
  reset() {
    this._rootElement.style.transform = this._initialTransform || "";
    this._activeTransform = {
      x: 0,
      y: 0
    };
    this._passiveTransform = {
      x: 0,
      y: 0
    };
  }
  resetToBoundary() {
    if (this._boundaryElement && this._rootElement && isOverflowingParent(this._boundaryElement.getBoundingClientRect(), this._rootElement.getBoundingClientRect())) {
      const parentRect = this._boundaryElement.getBoundingClientRect();
      const childRect = this._rootElement.getBoundingClientRect();
      let offsetX = 0;
      let offsetY = 0;
      if (childRect.left < parentRect.left) {
        offsetX = parentRect.left - childRect.left;
      } else if (childRect.right > parentRect.right) {
        offsetX = parentRect.right - childRect.right;
      }
      if (childRect.top < parentRect.top) {
        offsetY = parentRect.top - childRect.top;
      } else if (childRect.bottom > parentRect.bottom) {
        offsetY = parentRect.bottom - childRect.bottom;
      }
      const currentLeft = this._activeTransform.x;
      const currentTop = this._activeTransform.y;
      let x = currentLeft + offsetX, y = currentTop + offsetY;
      this._rootElement.style.transform = getTransform(x, y);
      this._activeTransform = {
        x,
        y
      };
      this._passiveTransform = {
        x,
        y
      };
    }
  }
  disableHandle(handle) {
    if (!this._disabledHandles.has(handle) && this._handles.indexOf(handle) > -1) {
      this._disabledHandles.add(handle);
      toggleNativeDragInteractions(handle, true);
    }
  }
  enableHandle(handle) {
    if (this._disabledHandles.has(handle)) {
      this._disabledHandles.delete(handle);
      toggleNativeDragInteractions(handle, this.disabled);
    }
  }
  withDirection(direction) {
    this._direction = direction;
    return this;
  }
  _withDropContainer(container) {
    this._dropContainer = container;
  }
  getFreeDragPosition() {
    const position = this.isDragging() ? this._activeTransform : this._passiveTransform;
    return {
      x: position.x,
      y: position.y
    };
  }
  setFreeDragPosition(value) {
    this._activeTransform = {
      x: 0,
      y: 0
    };
    this._passiveTransform.x = value.x;
    this._passiveTransform.y = value.y;
    if (!this._dropContainer) {
      this._applyRootElementTransform(value.x, value.y);
    }
    return this;
  }
  withPreviewContainer(value) {
    this._previewContainer = value;
    return this;
  }
  _sortFromLastPointerPosition() {
    const position = this._lastKnownPointerPosition;
    if (position && this._dropContainer) {
      this._updateActiveDropContainer(this._getConstrainedPointerPosition(position), position);
    }
  }
  _removeListeners() {
    this._pointerMoveSubscription.unsubscribe();
    this._pointerUpSubscription.unsubscribe();
    this._scrollSubscription.unsubscribe();
    this._cleanupShadowRootSelectStart?.();
    this._cleanupShadowRootSelectStart = void 0;
  }
  _destroyPreview() {
    this._preview?.destroy();
    this._preview = null;
  }
  _destroyPlaceholder() {
    this._anchor?.remove();
    this._placeholder?.remove();
    this._placeholderRef?.destroy();
    this._placeholder = this._anchor = this._placeholderRef = null;
  }
  _pointerDown = (event) => {
    this.beforeStarted.next();
    if (this._handles.length) {
      const targetHandle = this._getTargetHandle(event);
      if (targetHandle && !this._disabledHandles.has(targetHandle) && !this.disabled) {
        this._initializeDragSequence(targetHandle, event);
      }
    } else if (!this.disabled) {
      this._initializeDragSequence(this._rootElement, event);
    }
  };
  _pointerMove = (event) => {
    const pointerPosition = this._getPointerPositionOnPage(event);
    if (!this._hasStartedDragging()) {
      const distanceX = Math.abs(pointerPosition.x - this._pickupPositionOnPage.x);
      const distanceY = Math.abs(pointerPosition.y - this._pickupPositionOnPage.y);
      const isOverThreshold = distanceX + distanceY >= this._config.dragStartThreshold;
      if (isOverThreshold) {
        const isDelayElapsed = Date.now() >= this._dragStartTime + this._getDragStartDelay(event);
        const container = this._dropContainer;
        if (!isDelayElapsed) {
          this._endDragSequence(event);
          return;
        }
        if (!container || !container.isDragging() && !container.isReceiving()) {
          if (event.cancelable) {
            event.preventDefault();
          }
          this._hasStartedDragging.set(true);
          this._ngZone.run(() => this._startDragSequence(event));
        }
      }
      return;
    }
    if (event.cancelable) {
      event.preventDefault();
    }
    const constrainedPointerPosition = this._getConstrainedPointerPosition(pointerPosition);
    this._hasMoved = true;
    this._lastKnownPointerPosition = pointerPosition;
    this._updatePointerDirectionDelta(constrainedPointerPosition);
    if (this._dropContainer) {
      this._updateActiveDropContainer(constrainedPointerPosition, pointerPosition);
    } else {
      const offset = this.constrainPosition ? this._initialDomRect : this._pickupPositionOnPage;
      const activeTransform = this._activeTransform;
      activeTransform.x = constrainedPointerPosition.x - offset.x + this._passiveTransform.x;
      activeTransform.y = constrainedPointerPosition.y - offset.y + this._passiveTransform.y;
      this._applyRootElementTransform(activeTransform.x, activeTransform.y);
    }
    if (this._moveEvents.observers.length) {
      this._ngZone.run(() => {
        this._moveEvents.next({
          source: this,
          pointerPosition: constrainedPointerPosition,
          event,
          distance: this._getDragDistance(constrainedPointerPosition),
          delta: this._pointerDirectionDelta
        });
      });
    }
  };
  _pointerUp = (event) => {
    this._endDragSequence(event);
  };
  _endDragSequence(event) {
    if (!this._dragDropRegistry.isDragging(this)) {
      return;
    }
    this._removeListeners();
    this._dragDropRegistry.stopDragging(this);
    this._toggleNativeDragInteractions();
    if (this._handles) {
      this._rootElement.style.webkitTapHighlightColor = this._rootElementTapHighlight;
    }
    if (!this._hasStartedDragging()) {
      return;
    }
    this.released.next({
      source: this,
      event
    });
    if (this._dropContainer) {
      this._dropContainer._stopScrolling();
      this._animatePreviewToPlaceholder().then(() => {
        this._cleanupDragArtifacts(event);
        this._cleanupCachedDimensions();
        this._dragDropRegistry.stopDragging(this);
      });
    } else {
      this._passiveTransform.x = this._activeTransform.x;
      const pointerPosition = this._getPointerPositionOnPage(event);
      this._passiveTransform.y = this._activeTransform.y;
      this._ngZone.run(() => {
        this.ended.next({
          source: this,
          distance: this._getDragDistance(pointerPosition),
          dropPoint: pointerPosition,
          event
        });
      });
      this._cleanupCachedDimensions();
      this._dragDropRegistry.stopDragging(this);
    }
  }
  _startDragSequence(event) {
    if (isTouchEvent(event)) {
      this._lastTouchEventTime = Date.now();
    }
    this._toggleNativeDragInteractions();
    const shadowRoot = this._getShadowRoot();
    const dropContainer = this._dropContainer;
    if (shadowRoot) {
      this._ngZone.runOutsideAngular(() => {
        this._cleanupShadowRootSelectStart = this._renderer.listen(shadowRoot, "selectstart", shadowDomSelectStart, activeCapturingEventOptions);
      });
    }
    if (dropContainer) {
      const element = this._rootElement;
      const parent = element.parentNode;
      const placeholder = this._placeholder = this._createPlaceholderElement();
      const marker = this._marker = this._marker || this._document.createComment(typeof ngDevMode === "undefined" || ngDevMode ? "cdk-drag-marker" : "");
      parent.insertBefore(marker, element);
      this._initialTransform = element.style.transform || "";
      this._preview = new PreviewRef(this._document, this._rootElement, this._direction, this._initialDomRect, this._previewTemplate || null, this.previewClass || null, this._pickupPositionOnPage, this._initialTransform, this._config.zIndex || 1e3, this._renderer);
      this._preview.attach(this._getPreviewInsertionPoint(parent, shadowRoot));
      toggleVisibility(element, false, dragImportantProperties);
      this._document.body.appendChild(parent.replaceChild(placeholder, element));
      this.started.next({
        source: this,
        event
      });
      dropContainer.start();
      this._initialContainer = dropContainer;
      this._initialIndex = dropContainer.getItemIndex(this);
    } else {
      this.started.next({
        source: this,
        event
      });
      this._initialContainer = this._initialIndex = void 0;
    }
    this._parentPositions.cache(dropContainer ? dropContainer.getScrollableParents() : []);
  }
  _initializeDragSequence(referenceElement, event) {
    if (this._parentDragRef) {
      event.stopPropagation();
    }
    const isDragging = this.isDragging();
    const isTouchSequence = isTouchEvent(event);
    const isAuxiliaryMouseButton = !isTouchSequence && event.button !== 0;
    const rootElement = this._rootElement;
    const target = _getEventTarget(event);
    const isSyntheticEvent = !isTouchSequence && this._lastTouchEventTime && this._lastTouchEventTime + MOUSE_EVENT_IGNORE_TIME > Date.now();
    const isFakeEvent = isTouchSequence ? isFakeTouchstartFromScreenReader(event) : isFakeMousedownFromScreenReader(event);
    if (target && target.draggable && event.type === "mousedown") {
      event.preventDefault();
    }
    if (isDragging || isAuxiliaryMouseButton || isSyntheticEvent || isFakeEvent) {
      return;
    }
    if (this._handles.length) {
      const rootStyles = rootElement.style;
      this._rootElementTapHighlight = rootStyles.webkitTapHighlightColor || "";
      rootStyles.webkitTapHighlightColor = "transparent";
    }
    this._hasMoved = false;
    this._hasStartedDragging.set(this._hasMoved);
    this._removeListeners();
    this._initialDomRect = this._rootElement.getBoundingClientRect();
    this._pointerMoveSubscription = this._dragDropRegistry.pointerMove.subscribe(this._pointerMove);
    this._pointerUpSubscription = this._dragDropRegistry.pointerUp.subscribe(this._pointerUp);
    this._scrollSubscription = this._dragDropRegistry.scrolled(this._getShadowRoot()).subscribe((scrollEvent) => this._updateOnScroll(scrollEvent));
    if (this._boundaryElement) {
      this._boundaryRect = getMutableClientRect(this._boundaryElement);
    }
    const previewTemplate = this._previewTemplate;
    this._pickupPositionInElement = previewTemplate && previewTemplate.template && !previewTemplate.matchSize ? {
      x: 0,
      y: 0
    } : this._getPointerPositionInElement(this._initialDomRect, referenceElement, event);
    const pointerPosition = this._pickupPositionOnPage = this._lastKnownPointerPosition = this._getPointerPositionOnPage(event);
    this._pointerDirectionDelta = {
      x: 0,
      y: 0
    };
    this._pointerPositionAtLastDirectionChange = {
      x: pointerPosition.x,
      y: pointerPosition.y
    };
    this._dragStartTime = Date.now();
    this._dragDropRegistry.startDragging(this, event);
  }
  _cleanupDragArtifacts(event) {
    toggleVisibility(this._rootElement, true, dragImportantProperties);
    this._marker.parentNode.replaceChild(this._rootElement, this._marker);
    this._destroyPreview();
    this._destroyPlaceholder();
    this._initialDomRect = this._boundaryRect = this._previewRect = this._initialTransform = void 0;
    this._ngZone.run(() => {
      const container = this._dropContainer;
      const currentIndex = container.getItemIndex(this);
      const pointerPosition = this._getPointerPositionOnPage(event);
      const distance = this._getDragDistance(pointerPosition);
      const isPointerOverContainer = container._isOverContainer(pointerPosition.x, pointerPosition.y);
      this.ended.next({
        source: this,
        distance,
        dropPoint: pointerPosition,
        event
      });
      this.dropped.next({
        item: this,
        currentIndex,
        previousIndex: this._initialIndex,
        container,
        previousContainer: this._initialContainer,
        isPointerOverContainer,
        distance,
        dropPoint: pointerPosition,
        event
      });
      container.drop(this, currentIndex, this._initialIndex, this._initialContainer, isPointerOverContainer, distance, pointerPosition, event);
      this._dropContainer = this._initialContainer;
    });
  }
  _updateActiveDropContainer({
    x,
    y
  }, {
    x: rawX,
    y: rawY
  }) {
    let newContainer = this._initialContainer._getSiblingContainerFromPosition(this, x, y);
    if (!newContainer && this._dropContainer !== this._initialContainer && this._initialContainer._isOverContainer(x, y)) {
      newContainer = this._initialContainer;
    }
    if (newContainer && newContainer !== this._dropContainer) {
      this._ngZone.run(() => {
        const exitIndex = this._dropContainer.getItemIndex(this);
        const nextItemElement = this._dropContainer.getItemAtIndex(exitIndex + 1)?.getVisibleElement() || null;
        this.exited.next({
          item: this,
          container: this._dropContainer
        });
        this._dropContainer.exit(this);
        this._conditionallyInsertAnchor(newContainer, this._dropContainer, nextItemElement);
        this._dropContainer = newContainer;
        this._dropContainer.enter(this, x, y, newContainer === this._initialContainer && newContainer.sortingDisabled ? this._initialIndex : void 0);
        this.entered.next({
          item: this,
          container: newContainer,
          currentIndex: newContainer.getItemIndex(this)
        });
      });
    }
    if (this.isDragging()) {
      this._dropContainer._startScrollingIfNecessary(rawX, rawY);
      this._dropContainer._sortItem(this, x, y, this._pointerDirectionDelta);
      if (this.constrainPosition) {
        this._applyPreviewTransform(x, y);
      } else {
        this._applyPreviewTransform(x - this._pickupPositionInElement.x, y - this._pickupPositionInElement.y);
      }
    }
  }
  _animatePreviewToPlaceholder() {
    if (!this._hasMoved) {
      return Promise.resolve();
    }
    const placeholderRect = this._placeholder.getBoundingClientRect();
    this._preview.addClass("cdk-drag-animating");
    this._applyPreviewTransform(placeholderRect.left, placeholderRect.top);
    const duration = this._preview.getTransitionDuration();
    if (duration === 0) {
      return Promise.resolve();
    }
    return this._ngZone.runOutsideAngular(() => {
      return new Promise((resolve) => {
        const handler = (event) => {
          if (!event || this._preview && _getEventTarget(event) === this._preview.element && event.propertyName === "transform") {
            cleanupListener();
            resolve();
            clearTimeout(timeout);
          }
        };
        const timeout = setTimeout(handler, duration * 1.5);
        const cleanupListener = this._preview.addEventListener("transitionend", handler);
      });
    });
  }
  _createPlaceholderElement() {
    const placeholderConfig = this._placeholderTemplate;
    const placeholderTemplate = placeholderConfig ? placeholderConfig.template : null;
    let placeholder;
    if (placeholderTemplate) {
      this._placeholderRef = placeholderConfig.viewContainer.createEmbeddedView(placeholderTemplate, placeholderConfig.context);
      this._placeholderRef.detectChanges();
      placeholder = getRootNode(this._placeholderRef, this._document);
    } else {
      placeholder = deepCloneNode(this._rootElement);
    }
    placeholder.style.pointerEvents = "none";
    placeholder.classList.add(PLACEHOLDER_CLASS);
    return placeholder;
  }
  _getPointerPositionInElement(elementRect, referenceElement, event) {
    const handleElement = referenceElement === this._rootElement ? null : referenceElement;
    const referenceRect = handleElement ? handleElement.getBoundingClientRect() : elementRect;
    const point = isTouchEvent(event) ? event.targetTouches[0] : event;
    const scrollPosition = this._getViewportScrollPosition();
    const x = point.pageX - referenceRect.left - scrollPosition.left;
    const y = point.pageY - referenceRect.top - scrollPosition.top;
    return {
      x: referenceRect.left - elementRect.left + x,
      y: referenceRect.top - elementRect.top + y
    };
  }
  _getPointerPositionOnPage(event) {
    const scrollPosition = this._getViewportScrollPosition();
    const point = isTouchEvent(event) ? event.touches[0] || event.changedTouches[0] || {
      pageX: 0,
      pageY: 0
    } : event;
    const x = point.pageX - scrollPosition.left;
    const y = point.pageY - scrollPosition.top;
    if (this._ownerSVGElement) {
      const svgMatrix = this._ownerSVGElement.getScreenCTM();
      if (svgMatrix) {
        const svgPoint = this._ownerSVGElement.createSVGPoint();
        svgPoint.x = x;
        svgPoint.y = y;
        return svgPoint.matrixTransform(svgMatrix.inverse());
      }
    }
    return {
      x,
      y
    };
  }
  _getConstrainedPointerPosition(point) {
    const dropContainerLock = this._dropContainer ? this._dropContainer.lockAxis : null;
    let {
      x,
      y
    } = this.constrainPosition ? this.constrainPosition(point, this, this._initialDomRect, this._pickupPositionInElement) : point;
    if (this.lockAxis === "x" || dropContainerLock === "x") {
      y = this._pickupPositionOnPage.y - (this.constrainPosition ? this._pickupPositionInElement.y : 0);
    } else if (this.lockAxis === "y" || dropContainerLock === "y") {
      x = this._pickupPositionOnPage.x - (this.constrainPosition ? this._pickupPositionInElement.x : 0);
    }
    if (this._boundaryRect) {
      const {
        x: pickupX,
        y: pickupY
      } = !this.constrainPosition ? this._pickupPositionInElement : {
        x: 0,
        y: 0
      };
      const boundaryRect = this._boundaryRect;
      const {
        width: previewWidth,
        height: previewHeight
      } = this._getPreviewRect();
      const minY = boundaryRect.top + pickupY;
      const maxY = boundaryRect.bottom - (previewHeight - pickupY);
      const minX = boundaryRect.left + pickupX;
      const maxX = boundaryRect.right - (previewWidth - pickupX);
      x = clamp$1(x, minX, maxX);
      y = clamp$1(y, minY, maxY);
    }
    return {
      x,
      y
    };
  }
  _updatePointerDirectionDelta(pointerPositionOnPage) {
    const {
      x,
      y
    } = pointerPositionOnPage;
    const delta = this._pointerDirectionDelta;
    const positionSinceLastChange = this._pointerPositionAtLastDirectionChange;
    const changeX = Math.abs(x - positionSinceLastChange.x);
    const changeY = Math.abs(y - positionSinceLastChange.y);
    if (changeX > this._config.pointerDirectionChangeThreshold) {
      delta.x = x > positionSinceLastChange.x ? 1 : -1;
      positionSinceLastChange.x = x;
    }
    if (changeY > this._config.pointerDirectionChangeThreshold) {
      delta.y = y > positionSinceLastChange.y ? 1 : -1;
      positionSinceLastChange.y = y;
    }
    return delta;
  }
  _toggleNativeDragInteractions() {
    if (!this._rootElement || !this._handles) {
      return;
    }
    const shouldEnable = this._handles.length > 0 || !this.isDragging();
    if (shouldEnable !== this._nativeInteractionsEnabled) {
      this._nativeInteractionsEnabled = shouldEnable;
      toggleNativeDragInteractions(this._rootElement, shouldEnable);
    }
  }
  _removeRootElementListeners() {
    this._rootElementCleanups?.forEach((cleanup) => cleanup());
    this._rootElementCleanups = void 0;
  }
  _applyRootElementTransform(x, y) {
    const scale = 1 / this.scale;
    const transform = getTransform(x * scale, y * scale);
    const styles = this._rootElement.style;
    if (this._initialTransform == null) {
      this._initialTransform = styles.transform && styles.transform != "none" ? styles.transform : "";
    }
    styles.transform = combineTransforms(transform, this._initialTransform);
  }
  _applyPreviewTransform(x, y) {
    const initialTransform = this._previewTemplate?.template ? void 0 : this._initialTransform;
    const transform = getTransform(x, y);
    this._preview.setTransform(combineTransforms(transform, initialTransform));
  }
  _getDragDistance(currentPosition) {
    const pickupPosition = this._pickupPositionOnPage;
    if (pickupPosition) {
      return {
        x: currentPosition.x - pickupPosition.x,
        y: currentPosition.y - pickupPosition.y
      };
    }
    return {
      x: 0,
      y: 0
    };
  }
  _cleanupCachedDimensions() {
    this._boundaryRect = this._previewRect = void 0;
    this._parentPositions.clear();
  }
  _containInsideBoundaryOnResize() {
    let {
      x,
      y
    } = this._passiveTransform;
    if (x === 0 && y === 0 || this.isDragging() || !this._boundaryElement) {
      return;
    }
    const elementRect = this._rootElement.getBoundingClientRect();
    const boundaryRect = this._boundaryElement.getBoundingClientRect();
    if (boundaryRect.width === 0 && boundaryRect.height === 0 || elementRect.width === 0 && elementRect.height === 0) {
      return;
    }
    const leftOverflow = boundaryRect.left - elementRect.left;
    const rightOverflow = elementRect.right - boundaryRect.right;
    const topOverflow = boundaryRect.top - elementRect.top;
    const bottomOverflow = elementRect.bottom - boundaryRect.bottom;
    if (boundaryRect.width > elementRect.width) {
      if (leftOverflow > 0) {
        x += leftOverflow;
      }
      if (rightOverflow > 0) {
        x -= rightOverflow;
      }
    } else {
      x = 0;
    }
    if (boundaryRect.height > elementRect.height) {
      if (topOverflow > 0) {
        y += topOverflow;
      }
      if (bottomOverflow > 0) {
        y -= bottomOverflow;
      }
    } else {
      y = 0;
    }
    if (x !== this._passiveTransform.x || y !== this._passiveTransform.y) {
      this.setFreeDragPosition({
        y,
        x
      });
    }
  }
  _getDragStartDelay(event) {
    const value = this.dragStartDelay;
    if (typeof value === "number") {
      return value;
    } else if (isTouchEvent(event)) {
      return value.touch;
    }
    return value ? value.mouse : 0;
  }
  _updateOnScroll(event) {
    const scrollDifference = this._parentPositions.handleScroll(event);
    if (scrollDifference) {
      const target = _getEventTarget(event);
      if (this._boundaryRect && target !== this._boundaryElement && target.contains(this._boundaryElement)) {
        adjustDomRect(this._boundaryRect, scrollDifference.top, scrollDifference.left);
      }
      this._pickupPositionOnPage.x += scrollDifference.left;
      this._pickupPositionOnPage.y += scrollDifference.top;
      if (!this._dropContainer) {
        this._activeTransform.x -= scrollDifference.left;
        this._activeTransform.y -= scrollDifference.top;
        this._applyRootElementTransform(this._activeTransform.x, this._activeTransform.y);
      }
    }
  }
  _getViewportScrollPosition() {
    return this._parentPositions.positions.get(this._document)?.scrollPosition || this._parentPositions.getViewportScrollPosition();
  }
  _getShadowRoot() {
    if (this._cachedShadowRoot === void 0) {
      this._cachedShadowRoot = _getShadowRoot(this._rootElement);
    }
    return this._cachedShadowRoot;
  }
  _getPreviewInsertionPoint(initialParent, shadowRoot) {
    const previewContainer = this._previewContainer || "global";
    if (previewContainer === "parent") {
      return initialParent;
    }
    if (previewContainer === "global") {
      const documentRef = this._document;
      return shadowRoot || documentRef.fullscreenElement || documentRef.webkitFullscreenElement || documentRef.mozFullScreenElement || documentRef.msFullscreenElement || documentRef.body;
    }
    return coerceElement(previewContainer);
  }
  _getPreviewRect() {
    if (!this._previewRect || !this._previewRect.width && !this._previewRect.height) {
      this._previewRect = this._preview ? this._preview.getBoundingClientRect() : this._initialDomRect;
    }
    return this._previewRect;
  }
  _nativeDragStart = (event) => {
    if (this._handles.length) {
      const targetHandle = this._getTargetHandle(event);
      if (targetHandle && !this._disabledHandles.has(targetHandle) && !this.disabled) {
        event.preventDefault();
      }
    } else if (!this.disabled) {
      event.preventDefault();
    }
  };
  _getTargetHandle(event) {
    return this._handles.find((handle) => {
      return event.target && (event.target === handle || handle.contains(event.target));
    });
  }
  _conditionallyInsertAnchor(newContainer, exitContainer, nextItemElement) {
    if (newContainer === this._initialContainer) {
      this._anchor?.remove();
      this._anchor = null;
    } else if (exitContainer === this._initialContainer && exitContainer.hasAnchor) {
      const anchor = this._anchor ??= deepCloneNode(this._placeholder);
      anchor.classList.remove(PLACEHOLDER_CLASS);
      anchor.classList.add("cdk-drag-anchor");
      anchor.style.transform = "";
      if (nextItemElement) {
        nextItemElement.before(anchor);
      } else {
        coerceElement(exitContainer.element).appendChild(anchor);
      }
    }
  }
};
function clamp$1(value, min, max) {
  return Math.max(min, Math.min(max, value));
}
function isTouchEvent(event) {
  return event.type[0] === "t";
}
function shadowDomSelectStart(event) {
  event.preventDefault();
}
function moveItemInArray(array, fromIndex, toIndex) {
  const from = clamp(fromIndex, array.length - 1);
  const to = clamp(toIndex, array.length - 1);
  if (from === to) {
    return;
  }
  const target = array[from];
  const delta = to < from ? -1 : 1;
  for (let i = from; i !== to; i += delta) {
    array[i] = array[i + delta];
  }
  array[to] = target;
}
function clamp(value, max) {
  return Math.max(0, Math.min(max, value));
}
var SingleAxisSortStrategy = class {
  _dragDropRegistry;
  _element;
  _sortPredicate;
  _itemPositions = [];
  _activeDraggables;
  orientation = "vertical";
  direction = "ltr";
  constructor(_dragDropRegistry) {
    this._dragDropRegistry = _dragDropRegistry;
  }
  _previousSwap = {
    drag: null,
    delta: 0,
    overlaps: false
  };
  start(items) {
    this.withItems(items);
  }
  sort(item, pointerX, pointerY, pointerDelta) {
    const siblings = this._itemPositions;
    const newIndex = this._getItemIndexFromPointerPosition(item, pointerX, pointerY, pointerDelta);
    if (newIndex === -1 && siblings.length > 0) {
      return null;
    }
    const isHorizontal = this.orientation === "horizontal";
    const currentIndex = siblings.findIndex((currentItem) => currentItem.drag === item);
    const siblingAtNewPosition = siblings[newIndex];
    const currentPosition = siblings[currentIndex].clientRect;
    const newPosition = siblingAtNewPosition.clientRect;
    const delta = currentIndex > newIndex ? 1 : -1;
    const itemOffset = this._getItemOffsetPx(currentPosition, newPosition, delta);
    const siblingOffset = this._getSiblingOffsetPx(currentIndex, siblings, delta);
    const oldOrder = siblings.slice();
    moveItemInArray(siblings, currentIndex, newIndex);
    siblings.forEach((sibling, index) => {
      if (oldOrder[index] === sibling) {
        return;
      }
      const isDraggedItem = sibling.drag === item;
      const offset = isDraggedItem ? itemOffset : siblingOffset;
      const elementToOffset = isDraggedItem ? item.getPlaceholderElement() : sibling.drag.getRootElement();
      sibling.offset += offset;
      const transformAmount = Math.round(sibling.offset * (1 / sibling.drag.scale));
      if (isHorizontal) {
        elementToOffset.style.transform = combineTransforms(`translate3d(${transformAmount}px, 0, 0)`, sibling.initialTransform);
        adjustDomRect(sibling.clientRect, 0, offset);
      } else {
        elementToOffset.style.transform = combineTransforms(`translate3d(0, ${transformAmount}px, 0)`, sibling.initialTransform);
        adjustDomRect(sibling.clientRect, offset, 0);
      }
    });
    this._previousSwap.overlaps = isInsideClientRect(newPosition, pointerX, pointerY);
    this._previousSwap.drag = siblingAtNewPosition.drag;
    this._previousSwap.delta = isHorizontal ? pointerDelta.x : pointerDelta.y;
    return {
      previousIndex: currentIndex,
      currentIndex: newIndex
    };
  }
  enter(item, pointerX, pointerY, index) {
    const activeDraggables = this._activeDraggables;
    const currentIndex = activeDraggables.indexOf(item);
    const placeholder = item.getPlaceholderElement();
    if (currentIndex > -1) {
      activeDraggables.splice(currentIndex, 1);
    }
    const newIndex = index == null || index < 0 ? this._getItemIndexFromPointerPosition(item, pointerX, pointerY) : index;
    let newPositionReference = activeDraggables[newIndex];
    if (newPositionReference === item) {
      newPositionReference = activeDraggables[newIndex + 1];
    }
    if (!newPositionReference && (newIndex == null || newIndex === -1 || newIndex < activeDraggables.length - 1) && this._shouldEnterAsFirstChild(pointerX, pointerY)) {
      newPositionReference = activeDraggables[0];
    }
    if (newPositionReference && !this._dragDropRegistry.isDragging(newPositionReference)) {
      const element = newPositionReference.getRootElement();
      element.parentElement.insertBefore(placeholder, element);
      activeDraggables.splice(newIndex, 0, item);
    } else {
      this._element.appendChild(placeholder);
      activeDraggables.push(item);
    }
    placeholder.style.transform = "";
    this._cacheItemPositions();
  }
  withItems(items) {
    this._activeDraggables = items.slice();
    this._cacheItemPositions();
  }
  withSortPredicate(predicate) {
    this._sortPredicate = predicate;
  }
  reset() {
    this._activeDraggables?.forEach((item) => {
      const rootElement = item.getRootElement();
      if (rootElement) {
        const initialTransform = this._itemPositions.find((p) => p.drag === item)?.initialTransform;
        rootElement.style.transform = initialTransform || "";
      }
    });
    this._itemPositions = [];
    this._activeDraggables = [];
    this._previousSwap.drag = null;
    this._previousSwap.delta = 0;
    this._previousSwap.overlaps = false;
  }
  getActiveItemsSnapshot() {
    return this._activeDraggables;
  }
  getItemIndex(item) {
    return this._getVisualItemPositions().findIndex((currentItem) => currentItem.drag === item);
  }
  getItemAtIndex(index) {
    return this._getVisualItemPositions()[index]?.drag || null;
  }
  updateOnScroll(topDifference, leftDifference) {
    this._itemPositions.forEach(({
      clientRect
    }) => {
      adjustDomRect(clientRect, topDifference, leftDifference);
    });
    this._itemPositions.forEach(({
      drag
    }) => {
      if (this._dragDropRegistry.isDragging(drag)) {
        drag._sortFromLastPointerPosition();
      }
    });
  }
  withElementContainer(container) {
    this._element = container;
  }
  _cacheItemPositions() {
    const isHorizontal = this.orientation === "horizontal";
    this._itemPositions = this._activeDraggables.map((drag) => {
      const elementToMeasure = drag.getVisibleElement();
      return {
        drag,
        offset: 0,
        initialTransform: elementToMeasure.style.transform || "",
        clientRect: getMutableClientRect(elementToMeasure)
      };
    }).sort((a, b) => {
      return isHorizontal ? a.clientRect.left - b.clientRect.left : a.clientRect.top - b.clientRect.top;
    });
  }
  _getVisualItemPositions() {
    return this.orientation === "horizontal" && this.direction === "rtl" ? this._itemPositions.slice().reverse() : this._itemPositions;
  }
  _getItemOffsetPx(currentPosition, newPosition, delta) {
    const isHorizontal = this.orientation === "horizontal";
    let itemOffset = isHorizontal ? newPosition.left - currentPosition.left : newPosition.top - currentPosition.top;
    if (delta === -1) {
      itemOffset += isHorizontal ? newPosition.width - currentPosition.width : newPosition.height - currentPosition.height;
    }
    return itemOffset;
  }
  _getSiblingOffsetPx(currentIndex, siblings, delta) {
    const isHorizontal = this.orientation === "horizontal";
    const currentPosition = siblings[currentIndex].clientRect;
    const immediateSibling = siblings[currentIndex + delta * -1];
    let siblingOffset = currentPosition[isHorizontal ? "width" : "height"] * delta;
    if (immediateSibling) {
      const start = isHorizontal ? "left" : "top";
      const end = isHorizontal ? "right" : "bottom";
      if (delta === -1) {
        siblingOffset -= immediateSibling.clientRect[start] - currentPosition[end];
      } else {
        siblingOffset += currentPosition[start] - immediateSibling.clientRect[end];
      }
    }
    return siblingOffset;
  }
  _shouldEnterAsFirstChild(pointerX, pointerY) {
    if (!this._activeDraggables.length) {
      return false;
    }
    const itemPositions = this._itemPositions;
    const isHorizontal = this.orientation === "horizontal";
    const reversed = itemPositions[0].drag !== this._activeDraggables[0];
    if (reversed) {
      const lastItemRect = itemPositions[itemPositions.length - 1].clientRect;
      return isHorizontal ? pointerX >= lastItemRect.right : pointerY >= lastItemRect.bottom;
    } else {
      const firstItemRect = itemPositions[0].clientRect;
      return isHorizontal ? pointerX <= firstItemRect.left : pointerY <= firstItemRect.top;
    }
  }
  _getItemIndexFromPointerPosition(item, pointerX, pointerY, delta) {
    const isHorizontal = this.orientation === "horizontal";
    const index = this._itemPositions.findIndex(({
      drag,
      clientRect
    }) => {
      if (drag === item) {
        return false;
      }
      if (delta) {
        const direction = isHorizontal ? delta.x : delta.y;
        if (drag === this._previousSwap.drag && this._previousSwap.overlaps && direction === this._previousSwap.delta) {
          return false;
        }
      }
      return isHorizontal ? pointerX >= Math.floor(clientRect.left) && pointerX < Math.floor(clientRect.right) : pointerY >= Math.floor(clientRect.top) && pointerY < Math.floor(clientRect.bottom);
    });
    return index === -1 || !this._sortPredicate(index, item) ? -1 : index;
  }
};
var MixedSortStrategy = class {
  _document;
  _dragDropRegistry;
  _element;
  _sortPredicate;
  _rootNode;
  _activeItems;
  _previousSwap = {
    drag: null,
    deltaX: 0,
    deltaY: 0,
    overlaps: false
  };
  _relatedNodes = [];
  constructor(_document, _dragDropRegistry) {
    this._document = _document;
    this._dragDropRegistry = _dragDropRegistry;
  }
  start(items) {
    const childNodes = this._element.childNodes;
    this._relatedNodes = [];
    for (let i = 0; i < childNodes.length; i++) {
      const node = childNodes[i];
      this._relatedNodes.push([node, node.nextSibling]);
    }
    this.withItems(items);
  }
  sort(item, pointerX, pointerY, pointerDelta) {
    const newIndex = this._getItemIndexFromPointerPosition(item, pointerX, pointerY);
    const previousSwap = this._previousSwap;
    if (newIndex === -1 || this._activeItems[newIndex] === item) {
      return null;
    }
    const toSwapWith = this._activeItems[newIndex];
    if (previousSwap.drag === toSwapWith && previousSwap.overlaps && previousSwap.deltaX === pointerDelta.x && previousSwap.deltaY === pointerDelta.y) {
      return null;
    }
    const previousIndex = this.getItemIndex(item);
    const current = item.getPlaceholderElement();
    const overlapElement = toSwapWith.getRootElement();
    if (newIndex > previousIndex) {
      overlapElement.after(current);
    } else {
      overlapElement.before(current);
    }
    moveItemInArray(this._activeItems, previousIndex, newIndex);
    const newOverlapElement = this._getRootNode().elementFromPoint(pointerX, pointerY);
    previousSwap.deltaX = pointerDelta.x;
    previousSwap.deltaY = pointerDelta.y;
    previousSwap.drag = toSwapWith;
    previousSwap.overlaps = overlapElement === newOverlapElement || overlapElement.contains(newOverlapElement);
    return {
      previousIndex,
      currentIndex: newIndex
    };
  }
  enter(item, pointerX, pointerY, index) {
    const currentIndex = this._activeItems.indexOf(item);
    if (currentIndex > -1) {
      this._activeItems.splice(currentIndex, 1);
    }
    let enterIndex = index == null || index < 0 ? this._getItemIndexFromPointerPosition(item, pointerX, pointerY) : index;
    if (enterIndex === -1) {
      enterIndex = this._getClosestItemIndexToPointer(item, pointerX, pointerY);
    }
    const targetItem = this._activeItems[enterIndex];
    if (targetItem && !this._dragDropRegistry.isDragging(targetItem)) {
      this._activeItems.splice(enterIndex, 0, item);
      targetItem.getRootElement().before(item.getPlaceholderElement());
    } else {
      this._activeItems.push(item);
      this._element.appendChild(item.getPlaceholderElement());
    }
  }
  withItems(items) {
    this._activeItems = items.slice();
  }
  withSortPredicate(predicate) {
    this._sortPredicate = predicate;
  }
  reset() {
    const root = this._element;
    const previousSwap = this._previousSwap;
    for (let i = this._relatedNodes.length - 1; i > -1; i--) {
      const [node, nextSibling] = this._relatedNodes[i];
      if (node.parentNode === root && node.nextSibling !== nextSibling) {
        if (nextSibling === null) {
          root.appendChild(node);
        } else if (nextSibling.parentNode === root) {
          root.insertBefore(node, nextSibling);
        }
      }
    }
    this._relatedNodes = [];
    this._activeItems = [];
    previousSwap.drag = null;
    previousSwap.deltaX = previousSwap.deltaY = 0;
    previousSwap.overlaps = false;
  }
  getActiveItemsSnapshot() {
    return this._activeItems;
  }
  getItemIndex(item) {
    return this._activeItems.indexOf(item);
  }
  getItemAtIndex(index) {
    return this._activeItems[index] || null;
  }
  updateOnScroll() {
    this._activeItems.forEach((item) => {
      if (this._dragDropRegistry.isDragging(item)) {
        item._sortFromLastPointerPosition();
      }
    });
  }
  withElementContainer(container) {
    if (container !== this._element) {
      this._element = container;
      this._rootNode = void 0;
    }
  }
  _getItemIndexFromPointerPosition(item, pointerX, pointerY) {
    const elementAtPoint = this._getRootNode().elementFromPoint(Math.floor(pointerX), Math.floor(pointerY));
    const index = elementAtPoint ? this._activeItems.findIndex((item2) => {
      const root = item2.getRootElement();
      return elementAtPoint === root || root.contains(elementAtPoint);
    }) : -1;
    return index === -1 || !this._sortPredicate(index, item) ? -1 : index;
  }
  _getRootNode() {
    if (!this._rootNode) {
      this._rootNode = _getShadowRoot(this._element) || this._document;
    }
    return this._rootNode;
  }
  _getClosestItemIndexToPointer(item, pointerX, pointerY) {
    if (this._activeItems.length === 0) {
      return -1;
    }
    if (this._activeItems.length === 1) {
      return 0;
    }
    let minDistance = Infinity;
    let minIndex = -1;
    for (let i = 0; i < this._activeItems.length; i++) {
      const current = this._activeItems[i];
      if (current !== item) {
        const {
          x,
          y
        } = current.getRootElement().getBoundingClientRect();
        const distance = Math.hypot(pointerX - x, pointerY - y);
        if (distance < minDistance) {
          minDistance = distance;
          minIndex = i;
        }
      }
    }
    return minIndex;
  }
};
var DROP_PROXIMITY_THRESHOLD = 0.05;
var SCROLL_PROXIMITY_THRESHOLD = 0.05;
var AutoScrollVerticalDirection;
(function(AutoScrollVerticalDirection2) {
  AutoScrollVerticalDirection2[AutoScrollVerticalDirection2["NONE"] = 0] = "NONE";
  AutoScrollVerticalDirection2[AutoScrollVerticalDirection2["UP"] = 1] = "UP";
  AutoScrollVerticalDirection2[AutoScrollVerticalDirection2["DOWN"] = 2] = "DOWN";
})(AutoScrollVerticalDirection || (AutoScrollVerticalDirection = {}));
var AutoScrollHorizontalDirection;
(function(AutoScrollHorizontalDirection2) {
  AutoScrollHorizontalDirection2[AutoScrollHorizontalDirection2["NONE"] = 0] = "NONE";
  AutoScrollHorizontalDirection2[AutoScrollHorizontalDirection2["LEFT"] = 1] = "LEFT";
  AutoScrollHorizontalDirection2[AutoScrollHorizontalDirection2["RIGHT"] = 2] = "RIGHT";
})(AutoScrollHorizontalDirection || (AutoScrollHorizontalDirection = {}));
function createDropListRef(injector, element) {
  return new DropListRef(element, injector.get(DragDropRegistry), injector.get(DOCUMENT), injector.get(NgZone), injector.get(ViewportRuler));
}
var DropListRef = class {
  _dragDropRegistry;
  _ngZone;
  _viewportRuler;
  element;
  disabled = false;
  sortingDisabled = false;
  lockAxis = null;
  autoScrollDisabled = false;
  autoScrollStep = 2;
  hasAnchor = false;
  enterPredicate = () => true;
  sortPredicate = () => true;
  beforeStarted = new Subject();
  entered = new Subject();
  exited = new Subject();
  dropped = new Subject();
  sorted = new Subject();
  receivingStarted = new Subject();
  receivingStopped = new Subject();
  data;
  _container;
  _isDragging = false;
  _parentPositions;
  _sortStrategy;
  _domRect;
  _draggables = [];
  _siblings = [];
  _activeSiblings = /* @__PURE__ */ new Set();
  _viewportScrollSubscription = Subscription.EMPTY;
  _verticalScrollDirection = AutoScrollVerticalDirection.NONE;
  _horizontalScrollDirection = AutoScrollHorizontalDirection.NONE;
  _scrollNode;
  _stopScrollTimers = new Subject();
  _cachedShadowRoot = null;
  _document;
  _scrollableElements = [];
  _initialScrollSnap;
  _direction = "ltr";
  constructor(element, _dragDropRegistry, _document, _ngZone, _viewportRuler) {
    this._dragDropRegistry = _dragDropRegistry;
    this._ngZone = _ngZone;
    this._viewportRuler = _viewportRuler;
    const coercedElement = this.element = coerceElement(element);
    this._document = _document;
    this.withOrientation("vertical").withElementContainer(coercedElement);
    _dragDropRegistry.registerDropContainer(this);
    this._parentPositions = new ParentPositionTracker(_document);
  }
  dispose() {
    this._stopScrolling();
    this._stopScrollTimers.complete();
    this._viewportScrollSubscription.unsubscribe();
    this.beforeStarted.complete();
    this.entered.complete();
    this.exited.complete();
    this.dropped.complete();
    this.sorted.complete();
    this.receivingStarted.complete();
    this.receivingStopped.complete();
    this._activeSiblings.clear();
    this._scrollNode = null;
    this._parentPositions.clear();
    this._dragDropRegistry.removeDropContainer(this);
  }
  isDragging() {
    return this._isDragging;
  }
  start() {
    this._draggingStarted();
    this._notifyReceivingSiblings();
  }
  enter(item, pointerX, pointerY, index) {
    this._draggingStarted();
    if (index == null && this.sortingDisabled) {
      index = this._draggables.indexOf(item);
    }
    this._sortStrategy.enter(item, pointerX, pointerY, index);
    this._cacheParentPositions();
    this._notifyReceivingSiblings();
    this.entered.next({
      item,
      container: this,
      currentIndex: this.getItemIndex(item)
    });
  }
  exit(item) {
    this._reset();
    this.exited.next({
      item,
      container: this
    });
  }
  drop(item, currentIndex, previousIndex, previousContainer, isPointerOverContainer, distance, dropPoint, event) {
    this._reset();
    this.dropped.next({
      item,
      currentIndex,
      previousIndex,
      container: this,
      previousContainer,
      isPointerOverContainer,
      distance,
      dropPoint,
      event
    });
  }
  withItems(items) {
    const previousItems = this._draggables;
    this._draggables = items;
    items.forEach((item) => item._withDropContainer(this));
    if (this.isDragging()) {
      const draggedItems = previousItems.filter((item) => item.isDragging());
      if (draggedItems.every((item) => items.indexOf(item) === -1)) {
        this._reset();
      } else {
        this._sortStrategy.withItems(this._draggables);
      }
    }
    return this;
  }
  withDirection(direction) {
    this._direction = direction;
    if (this._sortStrategy instanceof SingleAxisSortStrategy) {
      this._sortStrategy.direction = direction;
    }
    return this;
  }
  connectedTo(connectedTo) {
    this._siblings = connectedTo.slice();
    return this;
  }
  withOrientation(orientation) {
    if (orientation === "mixed") {
      this._sortStrategy = new MixedSortStrategy(this._document, this._dragDropRegistry);
    } else {
      const strategy = new SingleAxisSortStrategy(this._dragDropRegistry);
      strategy.direction = this._direction;
      strategy.orientation = orientation;
      this._sortStrategy = strategy;
    }
    this._sortStrategy.withElementContainer(this._container);
    this._sortStrategy.withSortPredicate((index, item) => this.sortPredicate(index, item, this));
    return this;
  }
  withScrollableParents(elements) {
    const element = this._container;
    this._scrollableElements = elements.indexOf(element) === -1 ? [element, ...elements] : elements.slice();
    return this;
  }
  withElementContainer(container) {
    if (container === this._container) {
      return this;
    }
    const element = coerceElement(this.element);
    if ((typeof ngDevMode === "undefined" || ngDevMode) && container !== element && !element.contains(container)) {
      throw new Error("Invalid DOM structure for drop list. Alternate container element must be a descendant of the drop list.");
    }
    const oldContainerIndex = this._scrollableElements.indexOf(this._container);
    const newContainerIndex = this._scrollableElements.indexOf(container);
    if (oldContainerIndex > -1) {
      this._scrollableElements.splice(oldContainerIndex, 1);
    }
    if (newContainerIndex > -1) {
      this._scrollableElements.splice(newContainerIndex, 1);
    }
    if (this._sortStrategy) {
      this._sortStrategy.withElementContainer(container);
    }
    this._cachedShadowRoot = null;
    this._scrollableElements.unshift(container);
    this._container = container;
    return this;
  }
  getScrollableParents() {
    return this._scrollableElements;
  }
  getItemIndex(item) {
    return this._isDragging ? this._sortStrategy.getItemIndex(item) : this._draggables.indexOf(item);
  }
  getItemAtIndex(index) {
    return this._isDragging ? this._sortStrategy.getItemAtIndex(index) : this._draggables[index] || null;
  }
  isReceiving() {
    return this._activeSiblings.size > 0;
  }
  _sortItem(item, pointerX, pointerY, pointerDelta) {
    if (this.sortingDisabled || !this._domRect || !isPointerNearDomRect(this._domRect, DROP_PROXIMITY_THRESHOLD, pointerX, pointerY)) {
      return;
    }
    const result = this._sortStrategy.sort(item, pointerX, pointerY, pointerDelta);
    if (result) {
      this.sorted.next({
        previousIndex: result.previousIndex,
        currentIndex: result.currentIndex,
        container: this,
        item
      });
    }
  }
  _startScrollingIfNecessary(pointerX, pointerY) {
    if (this.autoScrollDisabled) {
      return;
    }
    let scrollNode;
    let verticalScrollDirection = AutoScrollVerticalDirection.NONE;
    let horizontalScrollDirection = AutoScrollHorizontalDirection.NONE;
    this._parentPositions.positions.forEach((position, element) => {
      if (element === this._document || !position.clientRect || scrollNode) {
        return;
      }
      if (isPointerNearDomRect(position.clientRect, DROP_PROXIMITY_THRESHOLD, pointerX, pointerY)) {
        [verticalScrollDirection, horizontalScrollDirection] = getElementScrollDirections(element, position.clientRect, this._direction, pointerX, pointerY);
        if (verticalScrollDirection || horizontalScrollDirection) {
          scrollNode = element;
        }
      }
    });
    if (!verticalScrollDirection && !horizontalScrollDirection) {
      const {
        width,
        height
      } = this._viewportRuler.getViewportSize();
      const domRect = {
        width,
        height,
        top: 0,
        right: width,
        bottom: height,
        left: 0
      };
      verticalScrollDirection = getVerticalScrollDirection(domRect, pointerY);
      horizontalScrollDirection = getHorizontalScrollDirection(domRect, pointerX);
      scrollNode = window;
    }
    if (scrollNode && (verticalScrollDirection !== this._verticalScrollDirection || horizontalScrollDirection !== this._horizontalScrollDirection || scrollNode !== this._scrollNode)) {
      this._verticalScrollDirection = verticalScrollDirection;
      this._horizontalScrollDirection = horizontalScrollDirection;
      this._scrollNode = scrollNode;
      if ((verticalScrollDirection || horizontalScrollDirection) && scrollNode) {
        this._ngZone.runOutsideAngular(this._startScrollInterval);
      } else {
        this._stopScrolling();
      }
    }
  }
  _stopScrolling() {
    this._stopScrollTimers.next();
  }
  _draggingStarted() {
    const styles = this._container.style;
    this.beforeStarted.next();
    this._isDragging = true;
    if ((typeof ngDevMode === "undefined" || ngDevMode) && this._container !== coerceElement(this.element)) {
      for (const drag of this._draggables) {
        if (!drag.isDragging() && drag.getVisibleElement().parentNode !== this._container) {
          throw new Error("Invalid DOM structure for drop list. All items must be placed directly inside of the element container.");
        }
      }
    }
    this._initialScrollSnap = styles.msScrollSnapType || styles.scrollSnapType || "";
    styles.scrollSnapType = styles.msScrollSnapType = "none";
    this._sortStrategy.start(this._draggables);
    this._cacheParentPositions();
    this._viewportScrollSubscription.unsubscribe();
    this._listenToScrollEvents();
  }
  _cacheParentPositions() {
    this._parentPositions.cache(this._scrollableElements);
    this._domRect = this._parentPositions.positions.get(this._container).clientRect;
  }
  _reset() {
    this._isDragging = false;
    const styles = this._container.style;
    styles.scrollSnapType = styles.msScrollSnapType = this._initialScrollSnap;
    this._siblings.forEach((sibling) => sibling._stopReceiving(this));
    this._sortStrategy.reset();
    this._stopScrolling();
    this._viewportScrollSubscription.unsubscribe();
    this._parentPositions.clear();
  }
  _startScrollInterval = () => {
    this._stopScrolling();
    interval(0, animationFrameScheduler).pipe(takeUntil(this._stopScrollTimers)).subscribe(() => {
      const node = this._scrollNode;
      const scrollStep = this.autoScrollStep;
      if (this._verticalScrollDirection === AutoScrollVerticalDirection.UP) {
        node.scrollBy(0, -scrollStep);
      } else if (this._verticalScrollDirection === AutoScrollVerticalDirection.DOWN) {
        node.scrollBy(0, scrollStep);
      }
      if (this._horizontalScrollDirection === AutoScrollHorizontalDirection.LEFT) {
        node.scrollBy(-scrollStep, 0);
      } else if (this._horizontalScrollDirection === AutoScrollHorizontalDirection.RIGHT) {
        node.scrollBy(scrollStep, 0);
      }
    });
  };
  _isOverContainer(x, y) {
    return this._domRect != null && isInsideClientRect(this._domRect, x, y);
  }
  _getSiblingContainerFromPosition(item, x, y) {
    return this._siblings.find((sibling) => sibling._canReceive(item, x, y));
  }
  _canReceive(item, x, y) {
    if (!this._domRect || !isInsideClientRect(this._domRect, x, y) || !this.enterPredicate(item, this)) {
      return false;
    }
    const elementFromPoint = this._getShadowRoot().elementFromPoint(x, y);
    if (!elementFromPoint) {
      return false;
    }
    return elementFromPoint === this._container || this._container.contains(elementFromPoint);
  }
  _startReceiving(sibling, items) {
    const activeSiblings = this._activeSiblings;
    if (!activeSiblings.has(sibling) && items.every((item) => {
      return this.enterPredicate(item, this) || this._draggables.indexOf(item) > -1;
    })) {
      activeSiblings.add(sibling);
      this._cacheParentPositions();
      this._listenToScrollEvents();
      this.receivingStarted.next({
        initiator: sibling,
        receiver: this,
        items
      });
    }
  }
  _stopReceiving(sibling) {
    this._activeSiblings.delete(sibling);
    this._viewportScrollSubscription.unsubscribe();
    this.receivingStopped.next({
      initiator: sibling,
      receiver: this
    });
  }
  _listenToScrollEvents() {
    this._viewportScrollSubscription = this._dragDropRegistry.scrolled(this._getShadowRoot()).subscribe((event) => {
      if (this.isDragging()) {
        const scrollDifference = this._parentPositions.handleScroll(event);
        if (scrollDifference) {
          this._sortStrategy.updateOnScroll(scrollDifference.top, scrollDifference.left);
        }
      } else if (this.isReceiving()) {
        this._cacheParentPositions();
      }
    });
  }
  _getShadowRoot() {
    if (!this._cachedShadowRoot) {
      const shadowRoot = _getShadowRoot(this._container);
      this._cachedShadowRoot = shadowRoot || this._document;
    }
    return this._cachedShadowRoot;
  }
  _notifyReceivingSiblings() {
    const draggedItems = this._sortStrategy.getActiveItemsSnapshot().filter((item) => item.isDragging());
    this._siblings.forEach((sibling) => sibling._startReceiving(this, draggedItems));
  }
};
function getVerticalScrollDirection(clientRect, pointerY) {
  const {
    top,
    bottom,
    height
  } = clientRect;
  const yThreshold = height * SCROLL_PROXIMITY_THRESHOLD;
  if (pointerY >= top - yThreshold && pointerY <= top + yThreshold) {
    return AutoScrollVerticalDirection.UP;
  } else if (pointerY >= bottom - yThreshold && pointerY <= bottom + yThreshold) {
    return AutoScrollVerticalDirection.DOWN;
  }
  return AutoScrollVerticalDirection.NONE;
}
function getHorizontalScrollDirection(clientRect, pointerX) {
  const {
    left,
    right,
    width
  } = clientRect;
  const xThreshold = width * SCROLL_PROXIMITY_THRESHOLD;
  if (pointerX >= left - xThreshold && pointerX <= left + xThreshold) {
    return AutoScrollHorizontalDirection.LEFT;
  } else if (pointerX >= right - xThreshold && pointerX <= right + xThreshold) {
    return AutoScrollHorizontalDirection.RIGHT;
  }
  return AutoScrollHorizontalDirection.NONE;
}
function getElementScrollDirections(element, clientRect, direction, pointerX, pointerY) {
  const computedVertical = getVerticalScrollDirection(clientRect, pointerY);
  const computedHorizontal = getHorizontalScrollDirection(clientRect, pointerX);
  let verticalScrollDirection = AutoScrollVerticalDirection.NONE;
  let horizontalScrollDirection = AutoScrollHorizontalDirection.NONE;
  if (computedVertical) {
    const scrollTop = element.scrollTop;
    if (computedVertical === AutoScrollVerticalDirection.UP) {
      if (scrollTop > 0) {
        verticalScrollDirection = AutoScrollVerticalDirection.UP;
      }
    } else if (element.scrollHeight - scrollTop > element.clientHeight) {
      verticalScrollDirection = AutoScrollVerticalDirection.DOWN;
    }
  }
  if (computedHorizontal) {
    const scrollLeft = element.scrollLeft;
    if (direction === "rtl") {
      if (computedHorizontal === AutoScrollHorizontalDirection.RIGHT) {
        if (scrollLeft < 0) {
          horizontalScrollDirection = AutoScrollHorizontalDirection.RIGHT;
        }
      } else if (element.scrollWidth + scrollLeft > element.clientWidth) {
        horizontalScrollDirection = AutoScrollHorizontalDirection.LEFT;
      }
    } else {
      if (computedHorizontal === AutoScrollHorizontalDirection.LEFT) {
        if (scrollLeft > 0) {
          horizontalScrollDirection = AutoScrollHorizontalDirection.LEFT;
        }
      } else if (element.scrollWidth - scrollLeft > element.clientWidth) {
        horizontalScrollDirection = AutoScrollHorizontalDirection.RIGHT;
      }
    }
  }
  return [verticalScrollDirection, horizontalScrollDirection];
}
var DragDrop = class _DragDrop {
  _injector = inject(Injector);
  createDrag(element, config) {
    return createDragRef(this._injector, element, config);
  }
  createDropList(element) {
    return createDropListRef(this._injector, element);
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.0",
    ngImport: core_exports,
    type: _DragDrop,
    deps: [],
    target: FactoryTarget.Service
  });
  static \u0275prov = \u0275\u0275ngDeclareService({
    minVersion: "22.0.0",
    version: "22.2.0",
    ngImport: core_exports,
    type: _DragDrop
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.0",
  ngImport: core_exports,
  type: DragDrop,
  decorators: [{
    type: Service
  }]
});
var CDK_DRAG_PARENT = new InjectionToken("CDK_DRAG_PARENT");
function assertElementNode(node, name) {
  if (node.nodeType !== 1) {
    throw Error(`${name} must be attached to an element node. Currently attached to "${node.nodeName}".`);
  }
}
var CDK_DRAG_HANDLE = new InjectionToken("CdkDragHandle");
var CdkDragHandle = class _CdkDragHandle {
  element = inject(ElementRef);
  _parentDrag = inject(CDK_DRAG_PARENT, {
    optional: true,
    skipSelf: true
  });
  _dragDropRegistry = inject(DragDropRegistry);
  _stateChanges = new Subject();
  get disabled() {
    return this._disabled;
  }
  set disabled(value) {
    this._disabled = value;
    this._stateChanges.next(this);
  }
  _disabled = false;
  constructor() {
    if (typeof ngDevMode === "undefined" || ngDevMode) {
      assertElementNode(this.element.nativeElement, "cdkDragHandle");
    }
    this._parentDrag?._addHandle(this);
  }
  ngAfterViewInit() {
    if (!this._parentDrag) {
      let parent = this.element.nativeElement.parentElement;
      while (parent) {
        const ref = this._dragDropRegistry.getDragDirectiveForNode(parent);
        if (ref) {
          this._parentDrag = ref;
          ref._addHandle(this);
          break;
        }
        parent = parent.parentElement;
      }
    }
  }
  ngOnDestroy() {
    this._parentDrag?._removeHandle(this);
    this._stateChanges.complete();
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.0",
    ngImport: core_exports,
    type: _CdkDragHandle,
    deps: [],
    target: FactoryTarget.Directive
  });
  static \u0275dir = \u0275\u0275ngDeclareDirective({
    minVersion: "16.1.0",
    version: "22.2.0",
    type: _CdkDragHandle,
    isStandalone: true,
    selector: "[cdkDragHandle]",
    inputs: {
      disabled: ["cdkDragHandleDisabled", "disabled", booleanAttribute]
    },
    host: {
      classAttribute: "cdk-drag-handle"
    },
    providers: [{
      provide: CDK_DRAG_HANDLE,
      useExisting: _CdkDragHandle
    }],
    ngImport: core_exports
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.0",
  ngImport: core_exports,
  type: CdkDragHandle,
  decorators: [{
    type: Directive,
    args: [{
      selector: "[cdkDragHandle]",
      host: {
        "class": "cdk-drag-handle"
      },
      providers: [{
        provide: CDK_DRAG_HANDLE,
        useExisting: CdkDragHandle
      }]
    }]
  }],
  ctorParameters: () => [],
  propDecorators: {
    disabled: [{
      type: Input,
      args: [{
        alias: "cdkDragHandleDisabled",
        transform: booleanAttribute
      }]
    }]
  }
});
var CDK_DRAG_CONFIG = new InjectionToken("CDK_DRAG_CONFIG");
var CDK_DROP_LIST = new InjectionToken("CdkDropList");
var CdkDrag = class _CdkDrag {
  element = inject(ElementRef);
  dropContainer = inject(CDK_DROP_LIST, {
    optional: true,
    skipSelf: true
  });
  _ngZone = inject(NgZone);
  _viewContainerRef = inject(ViewContainerRef);
  _dir = inject(Directionality, {
    optional: true
  });
  _changeDetectorRef = inject(ChangeDetectorRef);
  _selfHandle = inject(CDK_DRAG_HANDLE, {
    optional: true,
    self: true
  });
  _parentDrag = inject(CDK_DRAG_PARENT, {
    optional: true,
    skipSelf: true
  });
  _dragDropRegistry = inject(DragDropRegistry);
  _destroyed = new Subject();
  _handles = new BehaviorSubject([]);
  _previewTemplate = null;
  _placeholderTemplate = null;
  _dragRef;
  data;
  lockAxis = null;
  rootElementSelector;
  boundaryElement;
  dragStartDelay;
  freeDragPosition;
  get disabled() {
    return this._disabled || !!(this.dropContainer && this.dropContainer.disabled);
  }
  set disabled(value) {
    this._disabled = value;
    this._dragRef.disabled = this._disabled;
  }
  _disabled = false;
  constrainPosition;
  previewClass;
  previewContainer;
  scale = 1;
  started = new EventEmitter();
  released = new EventEmitter();
  ended = new EventEmitter();
  entered = new EventEmitter();
  exited = new EventEmitter();
  dropped = new EventEmitter();
  moved = new Observable((observer) => {
    const subscription = this._dragRef.moved.pipe(map((movedEvent) => ({
      source: this,
      pointerPosition: movedEvent.pointerPosition,
      event: movedEvent.event,
      delta: movedEvent.delta,
      distance: movedEvent.distance
    }))).subscribe(observer);
    return () => {
      subscription.unsubscribe();
    };
  });
  _injector = inject(Injector);
  constructor() {
    const dropContainer = this.dropContainer;
    const config = inject(CDK_DRAG_CONFIG, {
      optional: true
    });
    this._dragRef = createDragRef(this._injector, this.element, {
      dragStartThreshold: config && config.dragStartThreshold != null ? config.dragStartThreshold : 5,
      pointerDirectionChangeThreshold: config && config.pointerDirectionChangeThreshold != null ? config.pointerDirectionChangeThreshold : 5,
      zIndex: config?.zIndex
    });
    this._dragRef.data = this;
    this._dragDropRegistry.registerDirectiveNode(this.element.nativeElement, this);
    if (config) {
      this._assignDefaults(config);
    }
    if (dropContainer) {
      dropContainer.addItem(this);
      dropContainer._dropListRef.beforeStarted.pipe(takeUntil(this._destroyed)).subscribe(() => {
        this._dragRef.scale = this.scale;
      });
    }
    this._syncInputs(this._dragRef);
    this._handleEvents(this._dragRef);
  }
  getPlaceholderElement() {
    return this._dragRef.getPlaceholderElement();
  }
  getRootElement() {
    return this._dragRef.getRootElement();
  }
  reset() {
    this._dragRef.reset();
  }
  resetToBoundary() {
    this._dragRef.resetToBoundary();
  }
  getFreeDragPosition() {
    return this._dragRef.getFreeDragPosition();
  }
  setFreeDragPosition(value) {
    this._dragRef.setFreeDragPosition(value);
  }
  ngAfterViewInit() {
    afterNextRender(() => {
      this._updateRootElement();
      this._setupHandlesListener();
      this._dragRef.scale = this.scale;
      if (this.freeDragPosition) {
        this._dragRef.setFreeDragPosition(this.freeDragPosition);
      }
    }, {
      injector: this._injector
    });
  }
  ngOnChanges(changes) {
    const rootSelectorChange = changes["rootElementSelector"];
    const positionChange = changes["freeDragPosition"];
    if (rootSelectorChange && !rootSelectorChange.firstChange) {
      this._updateRootElement();
    }
    this._dragRef.scale = this.scale;
    if (positionChange && !positionChange.firstChange && this.freeDragPosition) {
      this._dragRef.setFreeDragPosition(this.freeDragPosition);
    }
  }
  ngOnDestroy() {
    if (this.dropContainer) {
      this.dropContainer.removeItem(this);
    }
    this._dragDropRegistry.removeDirectiveNode(this.element.nativeElement);
    this._ngZone.runOutsideAngular(() => {
      this._handles.complete();
      this._destroyed.next();
      this._destroyed.complete();
      this._dragRef.dispose();
    });
  }
  _addHandle(handle) {
    const handles = this._handles.getValue();
    handles.push(handle);
    this._handles.next(handles);
  }
  _removeHandle(handle) {
    const handles = this._handles.getValue();
    const index = handles.indexOf(handle);
    if (index > -1) {
      handles.splice(index, 1);
      this._handles.next(handles);
    }
  }
  _setPreviewTemplate(preview) {
    this._previewTemplate = preview;
  }
  _resetPreviewTemplate(preview) {
    if (preview === this._previewTemplate) {
      this._previewTemplate = null;
    }
  }
  _setPlaceholderTemplate(placeholder) {
    this._placeholderTemplate = placeholder;
  }
  _resetPlaceholderTemplate(placeholder) {
    if (placeholder === this._placeholderTemplate) {
      this._placeholderTemplate = null;
    }
  }
  _updateRootElement() {
    const element = this.element.nativeElement;
    let rootElement = element;
    if (this.rootElementSelector) {
      rootElement = element.closest !== void 0 ? element.closest(this.rootElementSelector) : element.parentElement?.closest(this.rootElementSelector);
    }
    if (rootElement && (typeof ngDevMode === "undefined" || ngDevMode)) {
      assertElementNode(rootElement, "cdkDrag");
    }
    this._dragRef.withRootElement(rootElement || element);
  }
  _getBoundaryElement() {
    const boundary = this.boundaryElement;
    if (!boundary) {
      return null;
    }
    if (typeof boundary === "string") {
      return this.element.nativeElement.closest(boundary);
    }
    return coerceElement(boundary);
  }
  _syncInputs(ref) {
    ref.beforeStarted.subscribe(() => {
      if (!ref.isDragging()) {
        const dir = this._dir;
        const dragStartDelay = this.dragStartDelay;
        const placeholder = this._placeholderTemplate ? {
          template: this._placeholderTemplate.templateRef,
          context: this._placeholderTemplate.data,
          viewContainer: this._viewContainerRef
        } : null;
        const preview = this._previewTemplate ? {
          template: this._previewTemplate.templateRef,
          context: this._previewTemplate.data,
          matchSize: this._previewTemplate.matchSize,
          viewContainer: this._viewContainerRef
        } : null;
        ref.disabled = this.disabled;
        ref.lockAxis = this.lockAxis;
        ref.scale = this.scale;
        ref.dragStartDelay = typeof dragStartDelay === "object" && dragStartDelay ? dragStartDelay : coerceNumberProperty(dragStartDelay);
        ref.constrainPosition = this.constrainPosition;
        ref.previewClass = this.previewClass;
        ref.withBoundaryElement(this._getBoundaryElement()).withPlaceholderTemplate(placeholder).withPreviewTemplate(preview).withPreviewContainer(this.previewContainer || "global");
        if (dir) {
          ref.withDirection(dir.value);
        }
      }
    });
    ref.beforeStarted.pipe(take(1)).subscribe(() => {
      if (this._parentDrag) {
        ref.withParent(this._parentDrag._dragRef);
        return;
      }
      let parent = this.element.nativeElement.parentElement;
      while (parent) {
        const parentDrag = this._dragDropRegistry.getDragDirectiveForNode(parent);
        if (parentDrag) {
          ref.withParent(parentDrag._dragRef);
          break;
        }
        parent = parent.parentElement;
      }
    });
  }
  _handleEvents(ref) {
    ref.started.subscribe((startEvent) => {
      this.started.emit({
        source: this,
        event: startEvent.event
      });
      this._changeDetectorRef.markForCheck();
    });
    ref.released.subscribe((releaseEvent) => {
      this.released.emit({
        source: this,
        event: releaseEvent.event
      });
    });
    ref.ended.subscribe((endEvent) => {
      this.ended.emit({
        source: this,
        distance: endEvent.distance,
        dropPoint: endEvent.dropPoint,
        event: endEvent.event
      });
      this._changeDetectorRef.markForCheck();
    });
    ref.entered.subscribe((enterEvent) => {
      this.entered.emit({
        container: enterEvent.container.data,
        item: this,
        currentIndex: enterEvent.currentIndex
      });
    });
    ref.exited.subscribe((exitEvent) => {
      this.exited.emit({
        container: exitEvent.container.data,
        item: this
      });
    });
    ref.dropped.subscribe((dropEvent) => {
      this.dropped.emit({
        previousIndex: dropEvent.previousIndex,
        currentIndex: dropEvent.currentIndex,
        previousContainer: dropEvent.previousContainer.data,
        container: dropEvent.container.data,
        isPointerOverContainer: dropEvent.isPointerOverContainer,
        item: this,
        distance: dropEvent.distance,
        dropPoint: dropEvent.dropPoint,
        event: dropEvent.event
      });
    });
  }
  _assignDefaults(config) {
    const {
      lockAxis,
      dragStartDelay,
      constrainPosition,
      previewClass,
      boundaryElement,
      draggingDisabled,
      rootElementSelector,
      previewContainer
    } = config;
    this.disabled = draggingDisabled == null ? false : draggingDisabled;
    this.dragStartDelay = dragStartDelay || 0;
    this.lockAxis = lockAxis || null;
    if (constrainPosition) {
      this.constrainPosition = constrainPosition;
    }
    if (previewClass) {
      this.previewClass = previewClass;
    }
    if (boundaryElement) {
      this.boundaryElement = boundaryElement;
    }
    if (rootElementSelector) {
      this.rootElementSelector = rootElementSelector;
    }
    if (previewContainer) {
      this.previewContainer = previewContainer;
    }
  }
  _setupHandlesListener() {
    this._handles.pipe(tap((handles) => {
      const handleElements = handles.map((handle) => handle.element);
      if (this._selfHandle && this.rootElementSelector) {
        handleElements.push(this.element);
      }
      this._dragRef.withHandles(handleElements);
    }), switchMap((handles) => {
      return merge(...handles.map((item) => item._stateChanges.pipe(startWith(item))));
    }), takeUntil(this._destroyed)).subscribe((handleInstance) => {
      const dragRef = this._dragRef;
      const handle = handleInstance.element.nativeElement;
      handleInstance.disabled ? dragRef.disableHandle(handle) : dragRef.enableHandle(handle);
    });
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.0",
    ngImport: core_exports,
    type: _CdkDrag,
    deps: [],
    target: FactoryTarget.Directive
  });
  static \u0275dir = \u0275\u0275ngDeclareDirective({
    minVersion: "16.1.0",
    version: "22.2.0",
    type: _CdkDrag,
    isStandalone: true,
    selector: "[cdkDrag]",
    inputs: {
      data: ["cdkDragData", "data"],
      lockAxis: ["cdkDragLockAxis", "lockAxis"],
      rootElementSelector: ["cdkDragRootElement", "rootElementSelector"],
      boundaryElement: ["cdkDragBoundary", "boundaryElement"],
      dragStartDelay: ["cdkDragStartDelay", "dragStartDelay"],
      freeDragPosition: ["cdkDragFreeDragPosition", "freeDragPosition"],
      disabled: ["cdkDragDisabled", "disabled", booleanAttribute],
      constrainPosition: ["cdkDragConstrainPosition", "constrainPosition"],
      previewClass: ["cdkDragPreviewClass", "previewClass"],
      previewContainer: ["cdkDragPreviewContainer", "previewContainer"],
      scale: ["cdkDragScale", "scale", numberAttribute]
    },
    outputs: {
      started: "cdkDragStarted",
      released: "cdkDragReleased",
      ended: "cdkDragEnded",
      entered: "cdkDragEntered",
      exited: "cdkDragExited",
      dropped: "cdkDragDropped",
      moved: "cdkDragMoved"
    },
    host: {
      properties: {
        "class.cdk-drag-disabled": "disabled",
        "class.cdk-drag-dragging": "_dragRef.isDragging()"
      },
      classAttribute: "cdk-drag"
    },
    providers: [{
      provide: CDK_DRAG_PARENT,
      useExisting: _CdkDrag
    }],
    exportAs: ["cdkDrag"],
    usesOnChanges: true,
    ngImport: core_exports
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.0",
  ngImport: core_exports,
  type: CdkDrag,
  decorators: [{
    type: Directive,
    args: [{
      selector: "[cdkDrag]",
      exportAs: "cdkDrag",
      host: {
        "class": "cdk-drag",
        "[class.cdk-drag-disabled]": "disabled",
        "[class.cdk-drag-dragging]": "_dragRef.isDragging()"
      },
      providers: [{
        provide: CDK_DRAG_PARENT,
        useExisting: CdkDrag
      }]
    }]
  }],
  ctorParameters: () => [],
  propDecorators: {
    data: [{
      type: Input,
      args: ["cdkDragData"]
    }],
    lockAxis: [{
      type: Input,
      args: ["cdkDragLockAxis"]
    }],
    rootElementSelector: [{
      type: Input,
      args: ["cdkDragRootElement"]
    }],
    boundaryElement: [{
      type: Input,
      args: ["cdkDragBoundary"]
    }],
    dragStartDelay: [{
      type: Input,
      args: ["cdkDragStartDelay"]
    }],
    freeDragPosition: [{
      type: Input,
      args: ["cdkDragFreeDragPosition"]
    }],
    disabled: [{
      type: Input,
      args: [{
        alias: "cdkDragDisabled",
        transform: booleanAttribute
      }]
    }],
    constrainPosition: [{
      type: Input,
      args: ["cdkDragConstrainPosition"]
    }],
    previewClass: [{
      type: Input,
      args: ["cdkDragPreviewClass"]
    }],
    previewContainer: [{
      type: Input,
      args: ["cdkDragPreviewContainer"]
    }],
    scale: [{
      type: Input,
      args: [{
        alias: "cdkDragScale",
        transform: numberAttribute
      }]
    }],
    started: [{
      type: Output,
      args: ["cdkDragStarted"]
    }],
    released: [{
      type: Output,
      args: ["cdkDragReleased"]
    }],
    ended: [{
      type: Output,
      args: ["cdkDragEnded"]
    }],
    entered: [{
      type: Output,
      args: ["cdkDragEntered"]
    }],
    exited: [{
      type: Output,
      args: ["cdkDragExited"]
    }],
    dropped: [{
      type: Output,
      args: ["cdkDragDropped"]
    }],
    moved: [{
      type: Output,
      args: ["cdkDragMoved"]
    }]
  }
});
var CDK_DROP_LIST_GROUP = new InjectionToken("CdkDropListGroup");
var CdkDropListGroup = class _CdkDropListGroup {
  _items = /* @__PURE__ */ new Set();
  disabled = false;
  ngOnDestroy() {
    this._items.clear();
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.0",
    ngImport: core_exports,
    type: _CdkDropListGroup,
    deps: [],
    target: FactoryTarget.Directive
  });
  static \u0275dir = \u0275\u0275ngDeclareDirective({
    minVersion: "16.1.0",
    version: "22.2.0",
    type: _CdkDropListGroup,
    isStandalone: true,
    selector: "[cdkDropListGroup]",
    inputs: {
      disabled: ["cdkDropListGroupDisabled", "disabled", booleanAttribute]
    },
    providers: [{
      provide: CDK_DROP_LIST_GROUP,
      useExisting: _CdkDropListGroup
    }],
    exportAs: ["cdkDropListGroup"],
    ngImport: core_exports
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.0",
  ngImport: core_exports,
  type: CdkDropListGroup,
  decorators: [{
    type: Directive,
    args: [{
      selector: "[cdkDropListGroup]",
      exportAs: "cdkDropListGroup",
      providers: [{
        provide: CDK_DROP_LIST_GROUP,
        useExisting: CdkDropListGroup
      }]
    }]
  }],
  propDecorators: {
    disabled: [{
      type: Input,
      args: [{
        alias: "cdkDropListGroupDisabled",
        transform: booleanAttribute
      }]
    }]
  }
});
var CdkDropList = class _CdkDropList {
  element = inject(ElementRef);
  _changeDetectorRef = inject(ChangeDetectorRef);
  _scrollDispatcher = inject(ScrollDispatcher);
  _dir = inject(Directionality, {
    optional: true
  });
  _group = inject(CDK_DROP_LIST_GROUP, {
    optional: true,
    skipSelf: true
  });
  _latestSortedRefs;
  _destroyed = new Subject();
  _scrollableParentsResolved = false;
  static _dropLists = [];
  _dropListRef;
  connectedTo = [];
  data;
  orientation = "vertical";
  id = inject(_IdGenerator).getId("cdk-drop-list-");
  lockAxis = null;
  get disabled() {
    return this._disabled || !!this._group && this._group.disabled;
  }
  set disabled(value) {
    this._dropListRef.disabled = this._disabled = value;
  }
  _disabled = false;
  sortingDisabled = false;
  enterPredicate = () => true;
  sortPredicate = () => true;
  autoScrollDisabled = false;
  autoScrollStep;
  elementContainerSelector = null;
  hasAnchor = false;
  dropped = new EventEmitter();
  entered = new EventEmitter();
  exited = new EventEmitter();
  sorted = new EventEmitter();
  _unsortedItems = /* @__PURE__ */ new Set();
  constructor() {
    const config = inject(CDK_DRAG_CONFIG, {
      optional: true
    });
    const injector = inject(Injector);
    if (typeof ngDevMode === "undefined" || ngDevMode) {
      assertElementNode(this.element.nativeElement, "cdkDropList");
    }
    this._dropListRef = createDropListRef(injector, this.element);
    this._dropListRef.data = this;
    if (config) {
      this._assignDefaults(config);
    }
    this._dropListRef.enterPredicate = (drag, drop) => {
      return this.enterPredicate(drag.data, drop.data);
    };
    this._dropListRef.sortPredicate = (index, drag, drop) => {
      return this.sortPredicate(index, drag.data, drop.data);
    };
    this._setupInputSyncSubscription(this._dropListRef);
    this._handleEvents(this._dropListRef);
    _CdkDropList._dropLists.push(this);
    if (this._group) {
      this._group._items.add(this);
    }
  }
  addItem(item) {
    this._unsortedItems.add(item);
    item._dragRef._withDropContainer(this._dropListRef);
    if (this._dropListRef.isDragging()) {
      this._syncItemsWithRef(this.getSortedItems().map((item2) => item2._dragRef));
    }
  }
  removeItem(item) {
    this._unsortedItems.delete(item);
    if (this._latestSortedRefs) {
      const index = this._latestSortedRefs.indexOf(item._dragRef);
      if (index > -1) {
        this._latestSortedRefs.splice(index, 1);
        this._syncItemsWithRef(this._latestSortedRefs);
      }
    }
  }
  getSortedItems() {
    return Array.from(this._unsortedItems).sort((a, b) => {
      const documentPosition = a._dragRef.getVisibleElement().compareDocumentPosition(b._dragRef.getVisibleElement());
      return documentPosition & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1;
    });
  }
  ngOnDestroy() {
    const index = _CdkDropList._dropLists.indexOf(this);
    if (index > -1) {
      _CdkDropList._dropLists.splice(index, 1);
    }
    if (this._group) {
      this._group._items.delete(this);
    }
    this._latestSortedRefs = void 0;
    this._unsortedItems.clear();
    this._dropListRef.dispose();
    this._destroyed.next();
    this._destroyed.complete();
  }
  _setupInputSyncSubscription(ref) {
    if (this._dir) {
      this._dir.change.pipe(startWith(this._dir.value), takeUntil(this._destroyed)).subscribe((value) => ref.withDirection(value));
    }
    ref.beforeStarted.subscribe(() => {
      const siblings = coerceArray(this.connectedTo).map((drop) => {
        if (typeof drop === "string") {
          const correspondingDropList = _CdkDropList._dropLists.find((list) => list.id === drop);
          if (!correspondingDropList && (typeof ngDevMode === "undefined" || ngDevMode)) {
            console.warn(`CdkDropList could not find connected drop list with id "${drop}"`);
          }
          return correspondingDropList;
        }
        return drop;
      });
      if (this._group) {
        this._group._items.forEach((drop) => {
          if (siblings.indexOf(drop) === -1) {
            siblings.push(drop);
          }
        });
      }
      if (!this._scrollableParentsResolved) {
        const scrollableParents = this._scrollDispatcher.getAncestorScrollContainers(this.element).map((scrollable) => scrollable.getElementRef().nativeElement);
        this._dropListRef.withScrollableParents(scrollableParents);
        this._scrollableParentsResolved = true;
      }
      if (this.elementContainerSelector) {
        const container = this.element.nativeElement.querySelector(this.elementContainerSelector);
        if (!container && (typeof ngDevMode === "undefined" || ngDevMode)) {
          throw new Error(`CdkDropList could not find an element container matching the selector "${this.elementContainerSelector}"`);
        }
        ref.withElementContainer(container);
      }
      ref.disabled = this.disabled;
      ref.lockAxis = this.lockAxis;
      ref.sortingDisabled = this.sortingDisabled;
      ref.autoScrollDisabled = this.autoScrollDisabled;
      ref.autoScrollStep = coerceNumberProperty(this.autoScrollStep, 2);
      ref.hasAnchor = this.hasAnchor;
      ref.connectedTo(siblings.filter((drop) => drop && drop !== this).map((list) => list._dropListRef)).withOrientation(this.orientation);
    });
  }
  _handleEvents(ref) {
    ref.beforeStarted.subscribe(() => {
      this._syncItemsWithRef(this.getSortedItems().map((item) => item._dragRef));
      this._changeDetectorRef.markForCheck();
    });
    ref.entered.subscribe((event) => {
      this.entered.emit({
        container: this,
        item: event.item.data,
        currentIndex: event.currentIndex
      });
    });
    ref.exited.subscribe((event) => {
      this.exited.emit({
        container: this,
        item: event.item.data
      });
      this._changeDetectorRef.markForCheck();
    });
    ref.sorted.subscribe((event) => {
      this.sorted.emit({
        previousIndex: event.previousIndex,
        currentIndex: event.currentIndex,
        container: this,
        item: event.item.data
      });
    });
    ref.dropped.subscribe((dropEvent) => {
      this.dropped.emit({
        previousIndex: dropEvent.previousIndex,
        currentIndex: dropEvent.currentIndex,
        previousContainer: dropEvent.previousContainer.data,
        container: dropEvent.container.data,
        item: dropEvent.item.data,
        isPointerOverContainer: dropEvent.isPointerOverContainer,
        distance: dropEvent.distance,
        dropPoint: dropEvent.dropPoint,
        event: dropEvent.event
      });
      this._changeDetectorRef.markForCheck();
    });
    merge(ref.receivingStarted, ref.receivingStopped).subscribe(() => this._changeDetectorRef.markForCheck());
  }
  _assignDefaults(config) {
    const {
      lockAxis,
      draggingDisabled,
      sortingDisabled,
      listAutoScrollDisabled,
      listOrientation
    } = config;
    this.disabled = draggingDisabled == null ? false : draggingDisabled;
    this.sortingDisabled = sortingDisabled == null ? false : sortingDisabled;
    this.autoScrollDisabled = listAutoScrollDisabled == null ? false : listAutoScrollDisabled;
    this.orientation = listOrientation || "vertical";
    this.lockAxis = lockAxis || null;
  }
  _syncItemsWithRef(items) {
    this._latestSortedRefs = items;
    this._dropListRef.withItems(items);
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.0",
    ngImport: core_exports,
    type: _CdkDropList,
    deps: [],
    target: FactoryTarget.Directive
  });
  static \u0275dir = \u0275\u0275ngDeclareDirective({
    minVersion: "16.1.0",
    version: "22.2.0",
    type: _CdkDropList,
    isStandalone: true,
    selector: "[cdkDropList], cdk-drop-list",
    inputs: {
      connectedTo: ["cdkDropListConnectedTo", "connectedTo"],
      data: ["cdkDropListData", "data"],
      orientation: ["cdkDropListOrientation", "orientation"],
      id: "id",
      lockAxis: ["cdkDropListLockAxis", "lockAxis"],
      disabled: ["cdkDropListDisabled", "disabled", booleanAttribute],
      sortingDisabled: ["cdkDropListSortingDisabled", "sortingDisabled", booleanAttribute],
      enterPredicate: ["cdkDropListEnterPredicate", "enterPredicate"],
      sortPredicate: ["cdkDropListSortPredicate", "sortPredicate"],
      autoScrollDisabled: ["cdkDropListAutoScrollDisabled", "autoScrollDisabled", booleanAttribute],
      autoScrollStep: ["cdkDropListAutoScrollStep", "autoScrollStep"],
      elementContainerSelector: ["cdkDropListElementContainer", "elementContainerSelector"],
      hasAnchor: ["cdkDropListHasAnchor", "hasAnchor", booleanAttribute]
    },
    outputs: {
      dropped: "cdkDropListDropped",
      entered: "cdkDropListEntered",
      exited: "cdkDropListExited",
      sorted: "cdkDropListSorted"
    },
    host: {
      properties: {
        "attr.id": "id",
        "class.cdk-drop-list-disabled": "disabled",
        "class.cdk-drop-list-dragging": "_dropListRef.isDragging()",
        "class.cdk-drop-list-receiving": "_dropListRef.isReceiving()"
      },
      classAttribute: "cdk-drop-list"
    },
    providers: [{
      provide: CDK_DROP_LIST_GROUP,
      useValue: void 0
    }, {
      provide: CDK_DROP_LIST,
      useExisting: _CdkDropList
    }],
    exportAs: ["cdkDropList"],
    ngImport: core_exports
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.0",
  ngImport: core_exports,
  type: CdkDropList,
  decorators: [{
    type: Directive,
    args: [{
      selector: "[cdkDropList], cdk-drop-list",
      exportAs: "cdkDropList",
      providers: [{
        provide: CDK_DROP_LIST_GROUP,
        useValue: void 0
      }, {
        provide: CDK_DROP_LIST,
        useExisting: CdkDropList
      }],
      host: {
        "class": "cdk-drop-list",
        "[attr.id]": "id",
        "[class.cdk-drop-list-disabled]": "disabled",
        "[class.cdk-drop-list-dragging]": "_dropListRef.isDragging()",
        "[class.cdk-drop-list-receiving]": "_dropListRef.isReceiving()"
      }
    }]
  }],
  ctorParameters: () => [],
  propDecorators: {
    connectedTo: [{
      type: Input,
      args: ["cdkDropListConnectedTo"]
    }],
    data: [{
      type: Input,
      args: ["cdkDropListData"]
    }],
    orientation: [{
      type: Input,
      args: ["cdkDropListOrientation"]
    }],
    id: [{
      type: Input
    }],
    lockAxis: [{
      type: Input,
      args: ["cdkDropListLockAxis"]
    }],
    disabled: [{
      type: Input,
      args: [{
        alias: "cdkDropListDisabled",
        transform: booleanAttribute
      }]
    }],
    sortingDisabled: [{
      type: Input,
      args: [{
        alias: "cdkDropListSortingDisabled",
        transform: booleanAttribute
      }]
    }],
    enterPredicate: [{
      type: Input,
      args: ["cdkDropListEnterPredicate"]
    }],
    sortPredicate: [{
      type: Input,
      args: ["cdkDropListSortPredicate"]
    }],
    autoScrollDisabled: [{
      type: Input,
      args: [{
        alias: "cdkDropListAutoScrollDisabled",
        transform: booleanAttribute
      }]
    }],
    autoScrollStep: [{
      type: Input,
      args: ["cdkDropListAutoScrollStep"]
    }],
    elementContainerSelector: [{
      type: Input,
      args: ["cdkDropListElementContainer"]
    }],
    hasAnchor: [{
      type: Input,
      args: [{
        alias: "cdkDropListHasAnchor",
        transform: booleanAttribute
      }]
    }],
    dropped: [{
      type: Output,
      args: ["cdkDropListDropped"]
    }],
    entered: [{
      type: Output,
      args: ["cdkDropListEntered"]
    }],
    exited: [{
      type: Output,
      args: ["cdkDropListExited"]
    }],
    sorted: [{
      type: Output,
      args: ["cdkDropListSorted"]
    }]
  }
});
var CDK_DRAG_PREVIEW = new InjectionToken("CdkDragPreview");
var CdkDragPreview = class _CdkDragPreview {
  templateRef = inject(TemplateRef);
  _drag = inject(CDK_DRAG_PARENT, {
    optional: true
  });
  data;
  matchSize = false;
  constructor() {
    this._drag?._setPreviewTemplate(this);
  }
  ngOnDestroy() {
    this._drag?._resetPreviewTemplate(this);
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.0",
    ngImport: core_exports,
    type: _CdkDragPreview,
    deps: [],
    target: FactoryTarget.Directive
  });
  static \u0275dir = \u0275\u0275ngDeclareDirective({
    minVersion: "16.1.0",
    version: "22.2.0",
    type: _CdkDragPreview,
    isStandalone: true,
    selector: "ng-template[cdkDragPreview]",
    inputs: {
      data: "data",
      matchSize: ["matchSize", "matchSize", booleanAttribute]
    },
    providers: [{
      provide: CDK_DRAG_PREVIEW,
      useExisting: _CdkDragPreview
    }],
    ngImport: core_exports
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.0",
  ngImport: core_exports,
  type: CdkDragPreview,
  decorators: [{
    type: Directive,
    args: [{
      selector: "ng-template[cdkDragPreview]",
      providers: [{
        provide: CDK_DRAG_PREVIEW,
        useExisting: CdkDragPreview
      }]
    }]
  }],
  ctorParameters: () => [],
  propDecorators: {
    data: [{
      type: Input
    }],
    matchSize: [{
      type: Input,
      args: [{
        transform: booleanAttribute
      }]
    }]
  }
});
var CDK_DRAG_PLACEHOLDER = new InjectionToken("CdkDragPlaceholder");
var CdkDragPlaceholder = class _CdkDragPlaceholder {
  templateRef = inject(TemplateRef);
  _drag = inject(CDK_DRAG_PARENT, {
    optional: true
  });
  data;
  constructor() {
    this._drag?._setPlaceholderTemplate(this);
  }
  ngOnDestroy() {
    this._drag?._resetPlaceholderTemplate(this);
  }
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.0",
    ngImport: core_exports,
    type: _CdkDragPlaceholder,
    deps: [],
    target: FactoryTarget.Directive
  });
  static \u0275dir = \u0275\u0275ngDeclareDirective({
    minVersion: "14.0.0",
    version: "22.2.0",
    type: _CdkDragPlaceholder,
    isStandalone: true,
    selector: "ng-template[cdkDragPlaceholder]",
    inputs: {
      data: "data"
    },
    providers: [{
      provide: CDK_DRAG_PLACEHOLDER,
      useExisting: _CdkDragPlaceholder
    }],
    ngImport: core_exports
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.0",
  ngImport: core_exports,
  type: CdkDragPlaceholder,
  decorators: [{
    type: Directive,
    args: [{
      selector: "ng-template[cdkDragPlaceholder]",
      providers: [{
        provide: CDK_DRAG_PLACEHOLDER,
        useExisting: CdkDragPlaceholder
      }]
    }]
  }],
  ctorParameters: () => [],
  propDecorators: {
    data: [{
      type: Input
    }]
  }
});
var DRAG_DROP_DIRECTIVES = [CdkDropList, CdkDropListGroup, CdkDrag, CdkDragHandle, CdkDragPreview, CdkDragPlaceholder];
var DragDropModule = class _DragDropModule {
  static \u0275fac = \u0275\u0275ngDeclareFactory({
    minVersion: "12.0.0",
    version: "22.2.0",
    ngImport: core_exports,
    type: _DragDropModule,
    deps: [],
    target: FactoryTarget.NgModule
  });
  static \u0275mod = \u0275\u0275ngDeclareNgModule({
    minVersion: "14.0.0",
    version: "22.2.0",
    ngImport: core_exports,
    type: _DragDropModule,
    imports: [CdkDropList, CdkDropListGroup, CdkDrag, CdkDragHandle, CdkDragPreview, CdkDragPlaceholder],
    exports: [CdkScrollableModule, CdkDropList, CdkDropListGroup, CdkDrag, CdkDragHandle, CdkDragPreview, CdkDragPlaceholder]
  });
  static \u0275inj = \u0275\u0275ngDeclareInjector({
    minVersion: "12.0.0",
    version: "22.2.0",
    ngImport: core_exports,
    type: _DragDropModule,
    providers: [DragDrop],
    imports: [CdkScrollableModule]
  });
};
\u0275\u0275ngDeclareClassMetadata({
  minVersion: "12.0.0",
  version: "22.2.0",
  ngImport: core_exports,
  type: DragDropModule,
  decorators: [{
    type: NgModule,
    args: [{
      imports: DRAG_DROP_DIRECTIVES,
      exports: [CdkScrollableModule, ...DRAG_DROP_DIRECTIVES],
      providers: [DragDrop]
    }]
  }]
});

// angular:jit:template:src/app/components/layout/edit-label/edit-label.component.html
var edit_label_component_default = `<app-dialog [headline]="'labeled-element.edit-label' | translate" [dialog]="dialogRef">
  <form [formGroup]="form">
    <mat-form-field>
      <div class="field-container">
        <label class="field-label" for="remoteKey">{{ 'edit-label.label' | translate }}</label>
        <input #firstInput formControlName="label" maxlength="66" matInput>
      </div>
    </mat-form-field>
  </form>
  <app-button class="float-right" color="primary" (action)="save()">{{ 'common.save' | translate }}</app-button>
</app-dialog>
`;

// angular:jit:style:src/app/components/layout/edit-label/edit-label.component.scss
var edit_label_component_default2 = "/* src/app/components/layout/edit-label/edit-label.component.scss */\n/*# sourceMappingURL=edit-label.component.css.map */\n";

// src/app/components/layout/edit-label/edit-label.component.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var EditLabelComponent_1;
var EditLabelComponent = class EditLabelComponent2 {
  static {
    EditLabelComponent_1 = this;
  }
  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  static openDialog(dialog, labelInfo) {
    const config = new MatDialogConfig();
    config.data = labelInfo;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;
    return dialog.open(EditLabelComponent_1, config);
  }
  constructor(dialogRef, data, formBuilder, storageService, snackbarService) {
    this.dialogRef = dialogRef;
    this.data = data;
    this.formBuilder = formBuilder;
    this.storageService = storageService;
    this.snackbarService = snackbarService;
  }
  ngOnInit() {
    this.form = this.formBuilder.group({
      label: [this.data.label]
    });
  }
  ngAfterViewInit() {
    setTimeout(() => this.firstInput.nativeElement.focus());
  }
  save() {
    const label = this.form.get("label").value.trim();
    if (label !== this.data.label) {
      this.storageService.saveLabel(this.data.id, label, this.data.identifiedElementType);
      if (!label) {
        this.snackbarService.showWarning("edit-label.label-removed-warning");
      } else {
        this.snackbarService.showDone("edit-label.done");
      }
      this.dialogRef.close(true);
    } else {
      this.dialogRef.close();
    }
  }
  static {
    this.ctorParameters = () => [
      { type: MatDialogRef },
      { type: LabelInfo, decorators: [{ type: Inject, args: [MAT_DIALOG_DATA] }] },
      { type: UntypedFormBuilder },
      { type: StorageService },
      { type: SnackbarService }
    ];
  }
  static {
    this.propDecorators = {
      firstInput: [{ type: ViewChild, args: ["firstInput"] }]
    };
  }
};
EditLabelComponent = EditLabelComponent_1 = __decorate([
  Component({
    selector: "app-edit-label",
    template: edit_label_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [edit_label_component_default2]
  })
], EditLabelComponent);

// src/app/utils/filters.ts
var FilterFieldTypes;
(function(FilterFieldTypes2) {
  FilterFieldTypes2["TextInput"] = "TextInput";
  FilterFieldTypes2["Select"] = "Select";
})(FilterFieldTypes || (FilterFieldTypes = {}));
function filterList(allElements, currentFilters, filterPropertiesList) {
  if (allElements) {
    const cleanedFilterPropertiesList = [];
    Object.keys(currentFilters).forEach((key) => {
      if (currentFilters[key]) {
        for (const property of filterPropertiesList) {
          if (property.keyNameInFiltersObject === key) {
            cleanedFilterPropertiesList.push(property);
            break;
          }
        }
      }
    });
    const response = allElements.filter((element) => {
      let valid = true;
      cleanedFilterPropertiesList.forEach((filterProperties) => {
        const primaryPropertyValid = String(element[filterProperties.keyNameInElementsArray]).toLowerCase().includes(currentFilters[filterProperties.keyNameInFiltersObject].toLowerCase());
        const secondaryPropertyValid = filterProperties.secondaryKeyNameInElementsArray && String(element[filterProperties.secondaryKeyNameInElementsArray]).toLowerCase().includes(currentFilters[filterProperties.keyNameInFiltersObject].toLowerCase());
        if (!primaryPropertyValid && !secondaryPropertyValid) {
          valid = false;
        }
      });
      return valid;
    });
    return response;
  }
  return null;
}
function updateFilterTexts(currentFilters, filterPropertiesList) {
  const response = [];
  filterPropertiesList.forEach((filterProperties) => {
    if (currentFilters[filterProperties.keyNameInFiltersObject]) {
      let value;
      let translatableValue;
      if (filterProperties.printableLabelsForValues) {
        filterProperties.printableLabelsForValues.forEach((printableLabel) => {
          if (printableLabel.value === currentFilters[filterProperties.keyNameInFiltersObject]) {
            translatableValue = printableLabel.label;
          }
        });
      }
      if (!translatableValue) {
        value = currentFilters[filterProperties.keyNameInFiltersObject];
      }
      response.push({
        filterName: filterProperties.filterName,
        translatableValue,
        value
      });
    }
  });
  return response;
}

// src/app/utils/lists/data-sorter.ts
var SortingColumn = class {
  constructor(properties, label, sortingMode, labelProperties) {
    this.properties = properties;
    this.label = label;
    this.sortingMode = sortingMode;
    this.labelProperties = labelProperties;
  }
  /**
   * Returns a string to be used as ID for the column. It is just a concatenation of the
   * "properties" array.
   */
  get id() {
    return this.properties.join("");
  }
};
var SortingModes;
(function(SortingModes2) {
  SortingModes2["Text"] = "Text";
  SortingModes2["Number"] = "Number";
  SortingModes2["NumberReversed"] = "NumberReversed";
  SortingModes2["Boolean"] = "Boolean";
})(SortingModes || (SortingModes = {}));
var DataSorter = class {
  /**
   * Returns the name of the icon that should be used in the column header for indicating
   * the sorting order.
   */
  get sortingArrow() {
    return this.sortReverse ? "keyboard_arrow_up" : "keyboard_arrow_down";
  }
  /**
   * Returns the column currently being used for sorting the data.
   */
  get currentSortingColumn() {
    return this.sortBy;
  }
  /**
   * Returns if the data is being sorted in reverse order.
   */
  get sortingInReverseOrder() {
    return this.sortReverse;
  }
  /**
   * Emits every time the data is sorted.
   */
  get dataSorted() {
    return this.dataUpdatedSubject.asObservable();
  }
  /**
   * Allow to know if the data is currently being sorted by the label shown in the cells and not
   * the value itself.
   */
  get currentlySortingByLabel() {
    return this.sortByLabel;
  }
  /**
   * @param columns Array with the data about the columns that can be used for sorting the list.
   * @param defaultColumnIndex Index in the "columns" array that must be used by default for
   * sorting the data. This column will be the one selected if not previously saved sorting
   * settings are found and will be used as tie-breaker when 2 columns have the same value.
   * @param id Unique short text identifying the list, for saving the sorting configuration
   * in localStorage.
   */
  constructor(dialog, translateService, storageService, columns, defaultColumnIndex, id) {
    this.dialog = dialog;
    this.translateService = translateService;
    this.storageService = storageService;
    this.sortReverse = false;
    this.sortByLabel = false;
    this.tieBreakerColumnIndex = null;
    this.columnStorageKeyPrefix = "col_";
    this.orderStorageKeyPrefix = "order_";
    this.labelStorageKeyPrefix = "label_";
    this.dataUpdatedSubject = new Subject();
    this.sortableColumns = columns;
    this.id = id;
    this.defaultColumnIndex = defaultColumnIndex;
    this.sortBy = columns[defaultColumnIndex];
    const savedColumn = this.storageService.getDataForHv(this.columnStorageKeyPrefix + id);
    if (savedColumn) {
      const savedColumnData = columns.find((column) => column.id === savedColumn);
      if (savedColumnData) {
        this.sortBy = savedColumnData;
      }
    }
    this.sortReverse = this.storageService.getDataForHv(this.orderStorageKeyPrefix + id) === "true";
    this.sortByLabel = this.storageService.getDataForHv(this.labelStorageKeyPrefix + id) === "true";
  }
  /**
   * Must be called after finishing using the instance.
   */
  dispose() {
    this.dataUpdatedSubject.complete();
  }
  /**
   * Sets the index of the column that will be used for sorting the data when the currently
   * selected column is is not enough.
   */
  setTieBreakerColumnIndex(columnIndex) {
    this.tieBreakerColumnIndex = columnIndex;
  }
  /**
   * Sets the data and inmediatelly sorts it. Each time this instance sorts the data,
   * the provided array is updated.
   * @param data Data to sort.
   */
  setData(data) {
    this.data = data;
    this.sortData();
  }
  /**
   * Changes the column and/or order used for sorting the data.
   */
  changeSortingOrder(column) {
    if (this.sortBy !== column && !column.labelProperties) {
      this.changeSortingParams(column, false, false);
    } else if (column.labelProperties) {
      const options = [
        {
          label: this.translateService.instant("tables.sort-by-value")
        },
        {
          label: this.translateService.instant("tables.sort-by-value") + " " + this.translateService.instant("tables.inverted-order")
        },
        {
          label: this.translateService.instant("tables.sort-by-label")
        },
        {
          label: this.translateService.instant("tables.sort-by-label") + " " + this.translateService.instant("tables.inverted-order")
        }
      ];
      SelectOptionComponent.openDialog(this.dialog, options, "tables.title").afterClosed().subscribe((result) => {
        if (result) {
          this.changeSortingParams(column, result > 2, result % 2 === 0);
        }
      });
    } else {
      this.sortReverse = !this.sortReverse;
      this.storageService.setDataForHv(this.orderStorageKeyPrefix + this.id, String(this.sortReverse));
      this.sortData();
    }
  }
  /**
   * Changes the current sorting params, saves them and sorts the data.
   */
  changeSortingParams(column, sortByLabel, sortReverse) {
    this.sortBy = column;
    this.sortByLabel = sortByLabel;
    this.sortReverse = sortReverse;
    this.storageService.setDataForHv(this.columnStorageKeyPrefix + this.id, column.id);
    this.storageService.setDataForHv(this.orderStorageKeyPrefix + this.id, String(this.sortReverse));
    this.storageService.setDataForHv(this.labelStorageKeyPrefix + this.id, String(this.sortByLabel));
    this.sortData();
  }
  /**
   * Opens the modal window used on small screens for selecting how to sort the data.
   */
  openSortingOrderModal() {
    const options = [];
    const optionParams = [];
    this.sortableColumns.forEach((column) => {
      const label = this.translateService.instant(column.label);
      options.push({
        label
      });
      optionParams.push({
        sortBy: column,
        sortReverse: false,
        sortByLabel: false
      });
      options.push({
        label: label + " " + this.translateService.instant("tables.inverted-order")
      });
      optionParams.push({
        sortBy: column,
        sortReverse: true,
        sortByLabel: false
      });
      if (column.labelProperties) {
        options.push({
          label: label + " " + this.translateService.instant("tables.label")
        });
        optionParams.push({
          sortBy: column,
          sortReverse: false,
          sortByLabel: true
        });
        options.push({
          label: label + " " + this.translateService.instant("tables.label") + " " + this.translateService.instant("tables.inverted-order")
        });
        optionParams.push({
          sortBy: column,
          sortReverse: true,
          sortByLabel: true
        });
      }
    });
    SelectOptionComponent.openDialog(this.dialog, options, "tables.title").afterClosed().subscribe((result) => {
      if (result) {
        this.changeSortingParams(optionParams[result - 1].sortBy, optionParams[result - 1].sortByLabel, optionParams[result - 1].sortReverse);
      }
    });
  }
  /**
   * Sorts the data.
   */
  sortData() {
    if (this.data) {
      this.data.sort((a, b) => {
        let response = this.getSortResponse(this.sortBy, a, b, true);
        if (response === 0) {
          if (this.tieBreakerColumnIndex !== null && this.sortableColumns[this.tieBreakerColumnIndex] !== this.sortBy) {
            response = this.getSortResponse(this.sortableColumns[this.tieBreakerColumnIndex], a, b, false);
          }
        }
        if (response === 0) {
          if (this.sortableColumns[this.defaultColumnIndex] !== this.sortBy) {
            response = this.getSortResponse(this.sortableColumns[this.defaultColumnIndex], a, b, false);
          }
        }
        return response;
      });
      this.dataUpdatedSubject.next();
    }
  }
  /**
   * Returns the value needed by the "sort" function of the data array.
   * @param sortingColumn Column being used to sort the data.
   * @param a First value.
   * @param b Second value.
   * @param sortByLabelIfRequested if true and this.sortByLabel is also true, the data will be
   * sorted using the label instead of the value itself.
   */
  getSortResponse(sortingColumn, a, b, sortByLabelIfRequested) {
    const propertiesList = this.sortByLabel && sortByLabelIfRequested && sortingColumn.labelProperties ? sortingColumn.labelProperties : sortingColumn.properties;
    let aVal = a;
    let bVal = b;
    propertiesList.forEach((property) => {
      aVal = aVal == null ? void 0 : aVal[property];
      bVal = bVal == null ? void 0 : bVal[property];
    });
    const sortingMode = this.sortByLabel && sortByLabelIfRequested ? SortingModes.Text : sortingColumn.sortingMode;
    let response = 0;
    if (sortingMode === SortingModes.Text) {
      const aText = aVal == null ? "" : aVal;
      const bText = bVal == null ? "" : bVal;
      response = !this.sortReverse ? aText.localeCompare(bText) : bText.localeCompare(aText);
    } else if (sortingMode === SortingModes.NumberReversed) {
      response = !this.sortReverse ? bVal - aVal : aVal - bVal;
    } else if (sortingMode === SortingModes.Number) {
      response = !this.sortReverse ? aVal - bVal : bVal - aVal;
    } else if (sortingMode === SortingModes.Boolean) {
      if (aVal && !bVal) {
        response = -1;
      } else if (!aVal && bVal) {
        response = 1;
      }
      response = response * (this.sortReverse ? -1 : 1);
    }
    return response;
  }
};

// angular:jit:template:src/app/components/layout/filters-selection/filters-selection.component.html
var filters_selection_component_default = `<app-dialog [headline]="'filters.filter-action' | translate" [dialog]="dialogRef">
  <form [formGroup]="form">
    <!-- Form fields. -->
    @for (fieldParams of data.filterPropertiesList; track fieldParams) {
      <mat-form-field>
        <div class="field-container">
          <label class="field-label" for="remoteKey">{{ fieldParams.filterName | translate }}</label>
          <!-- Text input. -->
          @if (fieldParams.type === filterFieldTypes.TextInput) {
            <input
              [formControlName]="fieldParams.keyNameInFiltersObject"
              [maxlength]="fieldParams.maxlength ?? null"
              matInput
              >
          }
          <!-- Select. -->
          @if (fieldParams.type === filterFieldTypes.Select) {
            <mat-select
              [formControlName]="fieldParams.keyNameInFiltersObject"
              >
              @for (option of fieldParams.printableLabelsForValues; track option) {
                <mat-option [value]="option.value">
                  <!-- Option image. -->
                  @if (fieldParams.printableLabelGeneralSettings && option.image) {
                    <div
                      class="image-container"
                [style]="
                  'background-image: url(\\'' + fieldParams.printableLabelGeneralSettings.defaultImage + '\\'); ' +
                  'width: ' + fieldParams.printableLabelGeneralSettings.imageWidth + 'px; ' +
                  'height: ' + fieldParams.printableLabelGeneralSettings.imageHeight + 'px;'"
                      >
                      <div
                        class="image"
                        [style]="'background-image: url(\\'' + option.image + '\\');'">
                      </div>
                    </div>
                  }
                  <!-- Option text. -->
                  {{ option.label | translate }}
                </mat-option>
              }
            </mat-select>
          }
        </div>
      </mat-form-field>
    }
  </form>

  <!-- button. -->
  <app-button
    #button
    (action)="apply()"
    color="primary"
    class="float-right"
    >
    {{ 'common.ok' | translate }}
  </app-button>
</app-dialog>
`;

// angular:jit:style:src/app/components/layout/filters-selection/filters-selection.component.scss
var filters_selection_component_default2 = "/* src/app/components/layout/filters-selection/filters-selection.component.scss */\n.image-container {\n  display: inline-block;\n  background-size: contain;\n  margin-right: 5px;\n}\n.image-container .image {\n  background-size: contain;\n  width: 100%;\n  height: 100%;\n}\n/*# sourceMappingURL=filters-selection.component.css.map */\n";

// src/app/components/layout/filters-selection/filters-selection.component.ts
var __decorate2 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var FiltersSelectionComponent_1;
var FiltersSelectionComponent = class FiltersSelectionComponent2 {
  static {
    FiltersSelectionComponent_1 = this;
  }
  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   * @param filterFielsdParams List of the fields the window will show.
   */
  static openDialog(dialog, filterFielsdParams) {
    const config = new MatDialogConfig();
    config.data = filterFielsdParams;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;
    return dialog.open(FiltersSelectionComponent_1, config);
  }
  constructor(data, dialogRef, formBuilder) {
    this.data = data;
    this.dialogRef = dialogRef;
    this.formBuilder = formBuilder;
    this.filterFieldTypes = FilterFieldTypes;
  }
  ngOnInit() {
    const formFields = {};
    this.data.filterPropertiesList.forEach((properties) => {
      formFields[properties.keyNameInFiltersObject] = [this.data.currentFilters[properties.keyNameInFiltersObject]];
    });
    this.form = this.formBuilder.group(formFields);
  }
  // Closes the modal window and returns the selected filters.
  apply() {
    const response = {};
    this.data.filterPropertiesList.forEach((properties) => {
      response[properties.keyNameInFiltersObject] = this.form.get(properties.keyNameInFiltersObject).value.trim();
    });
    this.dialogRef.close(response);
  }
  static {
    this.ctorParameters = () => [
      { type: void 0, decorators: [{ type: Inject, args: [MAT_DIALOG_DATA] }] },
      { type: MatDialogRef },
      { type: UntypedFormBuilder }
    ];
  }
};
FiltersSelectionComponent = FiltersSelectionComponent_1 = __decorate2([
  Component({
    selector: "app-filters-selection",
    template: filters_selection_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [filters_selection_component_default2]
  })
], FiltersSelectionComponent);

// src/app/utils/lists/data-filterer.ts
var DataFilterer = class {
  get currentFiltersTexts() {
    return this.currentFiltersTextsInternal;
  }
  get currentUrlQueryParams() {
    return this.currentUrlQueryParamsInternal;
  }
  /**
   * Emits every time the data is filtered. It returns the filtered array.
   */
  get dataFiltered() {
    return this.dataUpdatedSubject.asObservable();
  }
  /**
   * @param filterPropertiesList List with the data for each filter.
   * @param id Unique short text identifying the list.
   */
  constructor(dialog, route, router, filterPropertiesList, id) {
    this.dialog = dialog;
    this.route = route;
    this.router = router;
    this.currentFiltersTextsInternal = [];
    this.dataUpdatedSubject = new Subject();
    this.filterPropertiesList = filterPropertiesList;
    this.currentFilters = {};
    this.filterPropertiesList.forEach((property) => {
      property.keyNameInFiltersObject = id + "_" + property.keyNameInElementsArray;
      this.currentFilters[property.keyNameInFiltersObject] = "";
    });
    this.navigationsSubscription = this.route.queryParamMap.subscribe((queryParams) => {
      Object.keys(this.currentFilters).forEach((key) => {
        if (queryParams.has(key)) {
          this.currentFilters[key] = queryParams.get(key);
        }
      });
      this.currentUrlQueryParamsInternal = {};
      queryParams.keys.forEach((key) => {
        this.currentUrlQueryParamsInternal[key] = queryParams.get(key);
      });
      this.filter();
    });
  }
  /**
   * Must be called after finishing using the instance.
   */
  dispose() {
    this.dataUpdatedSubject.complete();
    this.navigationsSubscription.unsubscribe();
  }
  /**
   * Sets the data and inmediatelly filters it. The result is returned in an event and the
   * original provided list is not modified.
   * @param data Data to filter.
   */
  setData(data) {
    this.data = data;
    this.filter();
  }
  /**
   * Removes all the filters added by the user.
   */
  removeFilters() {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, "filters.remove-confirmation");
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.closeModal();
      this.router.navigate([], { queryParams: {} });
    });
  }
  /**
   * Opens the filter selection modal window to let the user change the currently selected filters.
   */
  changeFilters() {
    const filtersSelectiondParams = {
      filterPropertiesList: this.filterPropertiesList,
      currentFilters: this.currentFilters
    };
    FiltersSelectionComponent.openDialog(this.dialog, filtersSelectiondParams).afterClosed().subscribe((response) => {
      if (response) {
        this.router.navigate([], { queryParams: response });
      }
    });
  }
  /**
   * Filters the data.
   */
  filter() {
    if (this.data) {
      let filteredData;
      let filtersSet = false;
      Object.keys(this.currentFilters).forEach((key) => {
        if (this.currentFilters[key]) {
          filtersSet = true;
        }
      });
      if (filtersSet) {
        filteredData = filterList(this.data, this.currentFilters, this.filterPropertiesList);
        this.updateCurrentFilters();
      } else {
        filteredData = this.data;
        this.updateCurrentFilters();
      }
      this.dataUpdatedSubject.next(filteredData);
    }
  }
  /**
   * Updates the texts with the currently selected filters.
   */
  updateCurrentFilters() {
    this.currentFiltersTextsInternal = updateFilterTexts(this.currentFilters, this.filterPropertiesList);
  }
};

// src/app/services/reward.service.ts
var __decorate3 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var RewardService = class RewardService2 {
  constructor(http) {
    this.http = http;
    this.rewardSystemUrl = "api/rewards";
    this.rewardDataCache = /* @__PURE__ */ new Map();
    this.cachedDates = [];
    this.fetching = false;
  }
  /**
   * Returns the last N days as YYYY-MM-DD strings, most recent first.
   */
  getLastNDays(n) {
    const dates = [];
    for (let i = 0; i < n; i++) {
      const d = /* @__PURE__ */ new Date();
      d.setDate(d.getDate() - i);
      dates.push(d.toISOString().split("T")[0]);
    }
    return dates;
  }
  /**
   * Formats a date string (YYYY-MM-DD) to short display format (e.g. "Apr 5").
   */
  formatDateShort(dateStr) {
    const d = /* @__PURE__ */ new Date(dateStr + "T00:00:00");
    const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
    return months[d.getMonth()] + " " + d.getDate();
  }
  /**
   * Returns cached reward data if available.
   */
  getCachedData() {
    return this.rewardDataCache;
  }
  /**
   * Returns the dates that were fetched.
   */
  getCachedDates() {
    return this.cachedDates;
  }
  /**
   * Returns true if we have cached data.
   */
  hasCache() {
    return this.cachedDates.length > 0;
  }
  /**
   * Fetches reward history for the last 7 days using the per-day endpoint.
   * Each day fetches: GET <reward_system>/skycoin-rewards/hist/<date>/json
   * which returns an array of { pk, "reward-address", amount, share } entries.
   *
   * Results are cached so switching tabs doesn't re-fetch.
   */
  fetchRewardData(visorPks) {
    if (this.hasCache()) {
      return of(this.rewardDataCache);
    }
    this.fetching = true;
    const requests = visorPks.map((pk) => this.http.get(`${this.rewardSystemUrl}/skycoin-rewards/visor/${pk}?days=7`).pipe(map((resp) => {
      return { pk, history: resp && resp.history ? resp.history : [] };
    }), catchError(() => of({ pk, history: [] }))));
    return forkJoin(requests).pipe(map((results) => {
      const dataMap = /* @__PURE__ */ new Map();
      for (const { pk, history } of results) {
        const visorData = {
          pk,
          rewardAddress: "",
          dailyAmounts: {},
          dailySent: {},
          weekTotal: 0
        };
        for (const day of history) {
          if (day.date) {
            visorData.dailyAmounts[day.date] = day.amount || 0;
            visorData.dailySent[day.date] = day.sent || false;
            visorData.weekTotal += day.amount || 0;
          }
        }
        dataMap.set(pk, visorData);
      }
      this.rewardDataCache = dataMap;
      this.cachedDates = this.getLastNDays(7);
      this.fetching = false;
      return dataMap;
    }), catchError((err) => {
      this.fetching = false;
      return of(/* @__PURE__ */ new Map());
    }));
  }
  /**
   * Clears the cache so the next fetch will re-request data.
   */
  clearCache() {
    this.rewardDataCache = /* @__PURE__ */ new Map();
    this.cachedDates = [];
  }
  static {
    this.ctorParameters = () => [
      { type: HttpClient }
    ];
  }
};
RewardService = __decorate3([
  Injectable({
    providedIn: "root"
  })
], RewardService);

// src/app/components/pages/node-list/node-list.component.ts
var __decorate4 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var compactKey = "nodeListCompact";
var appsColumnKey = "nodeListApps";
var collapsedSectionsKey = "nodeListCollapsedHypervisors";
function loadCollapsedSections() {
  try {
    const saved = JSON.parse(localStorage.getItem(collapsedSectionsKey) || "[]");
    return Array.isArray(saved) ? saved : [];
  } catch (e) {
    return [];
  }
}
function loadFlag(key, fallback) {
  try {
    const v = localStorage.getItem(key);
    return v === null ? fallback : v === "true";
  } catch (e) {
    return fallback;
  }
}
function versionParts(version) {
  const m = /^v?(\d+)\.(\d+)\.(\d+)(?:-(\d+))?/.exec(version || "");
  return m ? [Number(m[1]), Number(m[2]), Number(m[3]), Number(m[4] || 0)] : null;
}
function compareVersions(a, b) {
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) {
      return a[i] - b[i];
    }
  }
  return 0;
}
function appIsUp(app) {
  return app.status === 1 || app.status === 3;
}
function runsApp(node, name) {
  return (node.apps || []).some((a) => a.name === name && appIsUp(a));
}
function fleetAppNames(nodes) {
  const names = /* @__PURE__ */ new Set();
  nodes.forEach((n) => (n.apps || []).forEach((a) => names.add(a.name)));
  return [...names].sort();
}
function appChips(node) {
  return (node.apps || []).map((a) => {
    return { name: a.name, running: appIsUp(a) };
  }).sort((a, b) => Number(b.running) - Number(a.running) || a.name.localeCompare(b.name));
}
var NodeListComponent = class NodeListComponent2 extends PageBaseComponent {
  /**
   * Sub-hypervisor sections (everything beyond the local one).
   * Returns sections[1..] so the template can iterate without
   * needing per-row $index checks. Empty until the tree response
   * lands or when only the local section exists (no remote
   * hypervisors connected). Filtering out `subError` sections is
   * intentionally skipped here — even an unreachable sub-hypervisor
   * is informative to display, with its error rendered inline.
   */
  get subSections() {
    return this.sections.length > 1 ? this.sections.slice(1) : [];
  }
  /** Sub-sections whose hypervisor has no row on this page, so they keep a panel of their own. */
  get detachedSections() {
    return this.subSections.filter((s) => !this.dataSource.some((n) => n.localPk === s.hypervisorPk));
  }
  constructor(nodeService, multipleNodeDataService, router, dialog, authService, storageService, ngZone, snackbarService, clipboardService, translateService, rewardService, route, changeDetectorRef) {
    super();
    this.nodeService = nodeService;
    this.multipleNodeDataService = multipleNodeDataService;
    this.router = router;
    this.dialog = dialog;
    this.authService = authService;
    this.storageService = storageService;
    this.ngZone = ngZone;
    this.snackbarService = snackbarService;
    this.clipboardService = clipboardService;
    this.translateService = translateService;
    this.rewardService = rewardService;
    this.changeDetectorRef = changeDetectorRef;
    this.persistentServerDataResponseKey = "serv-dat-response";
    this.nodesListId = "nl";
    this.dmsgListId = "dl";
    this.hypervisorSortData = new SortingColumn(["isHypervisor"], "nodes.hypervisor", SortingModes.Boolean);
    this.stateSortData = new SortingColumn(["online"], "nodes.state", SortingModes.Boolean);
    this.labelSortData = new SortingColumn(["label"], "nodes.label", SortingModes.Text);
    this.keySortData = new SortingColumn(["localPk"], "nodes.key", SortingModes.Text);
    this.versionSortData = new SortingColumn(["version"], "nodes.version", SortingModes.Text);
    this.configVersionSortData = new SortingColumn(["configVersion"], "nodes.config-version", SortingModes.Text);
    this.dmsgServerSortData = new SortingColumn(["dmsgServerPk"], "nodes.dmsg-server", SortingModes.Text, ["dmsgServerPk_label"]);
    this.pingSortData = new SortingColumn(["roundTripPing"], "nodes.ping", SortingModes.Number);
    this.ipLocationSortData = new SortingColumn(["countryCode"], "nodes.ip-location", SortingModes.Text);
    this.transportsCountSortData = new SortingColumn(["transportsCount"], "nodes.transports", SortingModes.Number);
    this.dmsgServerCountSortData = new SortingColumn(["dmsgServerCount"], "nodes.dmsg-servers", SortingModes.Number);
    this.servicesCountSortData = new SortingColumn(["servicesCount"], "nodes.services", SortingModes.Number);
    this.rewardAddressSortData = new SortingColumn(["rewardsAddress"], "nodes.reward", SortingModes.Text);
    this.customNodeOrder = [];
    this.customNodeOrderKey = "nl-custom-order";
    this.loading = true;
    this.dataSource = [];
    this.tabsData = [];
    this.switcherTabs = [];
    this.options = [];
    this.showDmsgInfo = false;
    this.showRewardsInfo = false;
    this.canLogOut = true;
    this.rewardDataMap = /* @__PURE__ */ new Map();
    this.rewardDates = [];
    this.rewardDateHeaders = [];
    this.rewardDataLoading = false;
    this.rewardDataLoaded = false;
    this.allNodes = [];
    this.quickSearch = "";
    this.quickChip = "all";
    this.quickFilterHides = 0;
    this.newestVersion = null;
    this.compact = loadFlag(compactKey, true);
    this.showApps = loadFlag(appsColumnKey, false);
    this.appFilter = "";
    this.appNames = [];
    this.selected = /* @__PURE__ */ new Set();
    this.hasOfflineNodes = false;
    this.numberOfPages = 1;
    this.currentPage = 1;
    this.currentPageInUrl = 1;
    this.sections = [];
    this.localHypervisorPk = "";
    this.collapsedSections = new Set(loadCollapsedSections());
    this.filterProperties = [
      {
        filterName: "nodes.filter-dialog.online",
        keyNameInElementsArray: "online",
        type: FilterFieldTypes.Select,
        printableLabelsForValues: [
          {
            value: "",
            label: "nodes.filter-dialog.online-options.any"
          },
          {
            value: "true",
            label: "nodes.filter-dialog.online-options.online"
          },
          {
            value: "false",
            label: "nodes.filter-dialog.online-options.offline"
          }
        ]
      },
      {
        filterName: "nodes.filter-dialog.label",
        keyNameInElementsArray: "label",
        type: FilterFieldTypes.TextInput,
        maxlength: 100
      },
      {
        filterName: "nodes.filter-dialog.key",
        keyNameInElementsArray: "localPk",
        type: FilterFieldTypes.TextInput,
        maxlength: 66
      },
      {
        filterName: "nodes.filter-dialog.dmsg",
        keyNameInElementsArray: "dmsgServerPk",
        secondaryKeyNameInElementsArray: "dmsgServerPk_label",
        type: FilterFieldTypes.TextInput,
        maxlength: 66
      }
    ];
    this.secondsSinceLastUpdate = 0;
    this.lastUpdate = Date.now();
    this.updating = false;
    this.errorsUpdating = false;
    this.lastUpdateRequestedManually = false;
    this.labeledElementTypes = LabeledElementTypes;
    this.updateOptionsMenu();
    this.authVerificationSubscription = this.authService.checkLogin().subscribe((response) => {
      this.canLogOut = response !== AuthStates.AuthDisabled;
      this.updateOptionsMenu();
      this.changeDetectorRef.markForCheck();
    });
    this.showRewardsInfo = this.router.url.indexOf("rewards") !== -1;
    this.showDmsgInfo = false;
    this.filterProperties.splice(this.filterProperties.length - 1);
    const sortableColumns = [
      this.hypervisorSortData,
      this.stateSortData,
      this.labelSortData,
      this.ipLocationSortData,
      this.transportsCountSortData,
      this.dmsgServerCountSortData,
      this.keySortData,
      this.versionSortData,
      this.configVersionSortData,
      this.servicesCountSortData,
      this.rewardAddressSortData
    ];
    const listId = this.showRewardsInfo ? "rl" : this.nodesListId;
    this.dataSorter = new DataSorter(this.dialog, this.translateService, this.storageService, sortableColumns, 3, listId);
    this.dataSortedSubscription = this.dataSorter.dataSorted.subscribe(() => {
      this.recalculateElementsToShow();
      this.changeDetectorRef.markForCheck();
    });
    this.dataFilterer = new DataFilterer(this.dialog, route, this.router, this.filterProperties, listId);
    this.dataFiltererSubscription = this.dataFilterer.dataFiltered.subscribe((data) => {
      this.filteredNodes = Array.isArray(data) ? data : [];
      if (this.localHypervisorPk && this.allNodes) {
        const localShown = this.filteredNodes.some((n) => n.localPk === this.localHypervisorPk);
        if (!localShown) {
          const localNode = this.allNodes.find((n) => n.localPk === this.localHypervisorPk);
          if (localNode) {
            this.filteredNodes = [localNode, ...this.filteredNodes];
          }
        }
      }
      this.hasOfflineNodes = false;
      this.filteredNodes.forEach((node) => {
        if (!node.online) {
          this.hasOfflineNodes = true;
        }
      });
      this.dataSorter.setData(this.filteredNodes);
      this.changeDetectorRef.markForCheck();
    });
    this.navigationsSubscription = route.paramMap.subscribe((params) => {
      if (params.has("page")) {
        let selectedPage = Number.parseInt(params.get("page"), 10);
        if (isNaN(selectedPage) || selectedPage < 1) {
          selectedPage = 1;
        }
        this.currentPageInUrl = selectedPage;
        this.recalculateElementsToShow();
      }
      this.changeDetectorRef.markForCheck();
    });
    this.tabsData = homeTabsData();
    this.buildSwitcherTabs();
    this.languageSubscription = this.translateService.onLangChange.subscribe(() => {
      this.multipleNodeDataService.forceRefresh();
      this.changeDetectorRef.markForCheck();
    });
  }
  /**
   * Configures the options menu shown in the top bar.
   */
  updateOptionsMenu() {
    this.options = [];
    this.options.push({
      name: "nodes.modify-rewards-all",
      actionName: "modifyRewardsAll",
      icon: "edit"
    });
    if (this.canLogOut) {
      this.options.push({
        name: "common.logout",
        actionName: "logout",
        icon: "power_settings_new"
      });
    }
  }
  ngOnInit() {
    const savedOrder = this.getLocalValue(this.customNodeOrderKey);
    if (savedOrder && savedOrder.value) {
      try {
        const parsed = JSON.parse(savedOrder.value);
        if (Array.isArray(parsed) && parsed.every((p) => typeof p === "string")) {
          this.customNodeOrder = parsed;
        }
      } catch (e) {
      }
    }
    this.startGettingData(true);
    this.ngZone.runOutsideAngular(() => {
      this.updateTimeSubscription = timer(5e3, 5e3).subscribe(() => {
        this.ngZone.run(() => {
          this.secondsSinceLastUpdate = Math.floor((Date.now() - this.lastUpdate) / 1e3);
        });
        this.changeDetectorRef.markForCheck();
      });
    });
    return super.ngOnInit();
  }
  ngOnDestroy() {
    this.authVerificationSubscription.unsubscribe();
    this.dataSubscription.unsubscribe();
    this.updateTimeSubscription.unsubscribe();
    this.navigationsSubscription.unsubscribe();
    this.languageSubscription.unsubscribe();
    if (this.updateSubscription) {
      this.updateSubscription.unsubscribe();
    }
    this.dataSortedSubscription.unsubscribe();
    this.dataSorter.dispose();
    this.dataFiltererSubscription.unsubscribe();
    this.dataFilterer.dispose();
  }
  /**
   * Called when an option form the top bar is selected.
   * @param actionName Name of the selected option, as defined in the this.options array.
   */
  performAction(actionName) {
    if (actionName === "logout") {
      this.logout();
    } else if (actionName === "updateAll") {
      this.updateAll();
    } else if (actionName === "modifyRewardsAll") {
      this.changeRewardsToAll();
    }
  }
  /**
   * Builds the visor-switcher chips (one per visor in the list) shown in the top
   * bar. Same shape/icons as the node page's switcher so the row is consistent
   * across the list and detail pages; clicking a chip opens that visor's info
   * page. Best-effort: leaves the row empty until node data is available.
   */
  buildSwitcherTabs() {
    this.switcherTabs = visorSwitcherChips(this.allNodes || []);
  }
  /**
   * Returns the scss class to be used to show the current status of the node.
   * @param forDot If true, returns a class for creating a colored dot. If false,
   * returns a class for a colored text.
   */
  nodeStatusClass(node, forDot) {
    if (node.online) {
      if (node.health && node.health.servicesHealth === KnownHealthStatuses.Unhealthy) {
        return forDot ? "dot-yellow blinking" : "yellow-text";
      } else if (node.health && node.health.servicesHealth === KnownHealthStatuses.Healthy) {
        return forDot ? "dot-green" : "green-text";
      } else {
        return forDot ? "dot-outline-gray" : "";
      }
    } else if (node.isStale) {
      return forDot ? "dot-red dimmed" : "red-text dimmed";
    } else {
      return forDot ? "dot-red" : "red-text";
    }
  }
  /**
   * Returns the text to be used to indicate the current status of the node.
   * @param forTooltip If true, returns a text for a tooltip. If false, returns a
   * text for the node list shown on small screens.
   */
  nodeStatusText(node, forTooltip) {
    if (node.online) {
      if (node.health && node.health.servicesHealth === KnownHealthStatuses.Healthy) {
        return "node.statuses.online" + (forTooltip ? "-tooltip" : "");
      } else if (node.health && node.health.servicesHealth === KnownHealthStatuses.Unhealthy) {
        return "node.statuses.partially-online" + (forTooltip ? "-tooltip" : "");
      } else if (node.health && node.health.servicesHealth === KnownHealthStatuses.Connecting) {
        return "node.statuses.connecting" + (forTooltip ? "-tooltip" : "");
      } else {
        return "node.statuses.unknown" + (forTooltip ? "-tooltip" : "");
      }
    } else if (node.isStale) {
      return "node.statuses.stale" + (forTooltip ? "-tooltip" : "");
    } else {
      return "node.statuses.offline" + (forTooltip ? "-tooltip" : "");
    }
  }
  /**
   * Returns "last seen Xm ago" for a stale-cached row, or empty for
   * fresh / never-seen rows. Used as a small caption beside the row's
   * label so the operator knows how old the displayed data is.
   */
  lastSeenText(node) {
    if (!node.lastSeenAt) {
      return "";
    }
    const seen = new Date(node.lastSeenAt).getTime();
    if (isNaN(seen)) {
      return "";
    }
    const diffMs = Date.now() - seen;
    if (diffMs < 0) {
      return "";
    }
    const sec = Math.floor(diffMs / 1e3);
    if (sec < 60) {
      return `last seen ${sec}s ago`;
    }
    const min = Math.floor(sec / 60);
    if (min < 60) {
      return `last seen ${min}m ago`;
    }
    const hr = Math.floor(min / 60);
    if (hr < 24) {
      return `last seen ${hr}h ago`;
    }
    const day = Math.floor(hr / 24);
    return `last seen ${day}d ago`;
  }
  /**
   * Makes the node list to be immediately refreshed.
   * @param requestedManually True if the data is going to be loaded because of a direct request
   * from the user.
   */
  forceDataRefresh(requestedManually = false) {
    if (requestedManually) {
      this.lastUpdateRequestedManually = true;
    }
    this.multipleNodeDataService.forceRefresh();
  }
  /**
   * Starts getting the data from the backend.
   */
  startGettingData(checkSavedData) {
    const savedData = checkSavedData ? this.getLocalValue(this.persistentServerDataResponseKey) : null;
    let nextOperation = this.multipleNodeDataService.startRequestingData();
    if (savedData) {
      nextOperation = of(JSON.parse(savedData.value));
    }
    this.dataSubscription = nextOperation.subscribe((result) => {
      if (!savedData) {
        this.saveLocalValue(this.persistentServerDataResponseKey, JSON.stringify(result));
      }
      this.updating = result ? result.updating : true;
      if (result && !result.updating) {
        if (result.data && !result.error) {
          if (result.sections && result.sections.length > 0) {
            this.sections = result.sections;
            this.localHypervisorPk = result.sections[0].hypervisorPk;
          } else {
            this.sections = [];
            this.localHypervisorPk = "";
          }
          this.allNodes = Array.isArray(result.data) ? result.data : [];
          this.dataFilterer.setData(this.allNodes);
          this.buildSwitcherTabs();
          if (this.showRewardsInfo && !this.rewardDataLoaded && !this.rewardDataLoading) {
            this.loadRewardData();
          }
          this.loading = false;
          this.snackbarService.closeCurrentIfTemporaryError();
          this.lastUpdate = result.momentOfLastCorrectUpdate;
          this.secondsSinceLastUpdate = Math.floor((Date.now() - result.momentOfLastCorrectUpdate) / 1e3);
          this.errorsUpdating = false;
          AppComponent.currentInstance.hideDataProblemMsg();
          if (this.lastUpdateRequestedManually) {
            this.snackbarService.showDone("common.refreshed", null);
            this.lastUpdateRequestedManually = false;
          }
        } else if (result.error) {
          if (!this.errorsUpdating) {
            if (this.loading) {
              this.snackbarService.showError("common.loading-error", null, true, result.error);
            } else {
              this.snackbarService.showError("nodes.error-load", null, true, result.error);
            }
          }
          this.loading = false;
          this.errorsUpdating = true;
          AppComponent.currentInstance.showDataProblemMsg();
        }
      }
      if (savedData) {
        this.startGettingData(false);
      }
      this.changeDetectorRef.markForCheck();
    });
  }
  /**
   * Recalculates which elements should be shown on the UI.
   */
  recalculateElementsToShow() {
    this.currentPage = this.currentPageInUrl;
    if (this.filteredNodes) {
      this.filteredNodes.forEach((n) => this.annotateForSort(n));
      if (this.customNodeOrder.length > 0) {
        this.filteredNodes = this.applyCustomOrder(this.filteredNodes);
      }
      const everyNode = this.filteredNodes.concat(...this.subSections.map((s) => s.nodes || []));
      this.newestVersion = everyNode.reduce((best, n) => {
        const v = versionParts(n.version);
        return v && (!best || compareVersions(v, best) > 0) ? v : best;
      }, null);
      this.appNames = fleetAppNames(everyNode);
      const visible = this.filteredNodes.filter((n) => this.matchesQuickFilter(n));
      this.quickFilterHides = this.filteredNodes.length - visible.length;
      const maxElements = AppConfig.maxFullListElements;
      this.numberOfPages = Math.ceil(visible.length / maxElements);
      if (this.currentPage > this.numberOfPages) {
        this.currentPage = this.numberOfPages;
      }
      const start = maxElements * (this.currentPage - 1);
      const end = start + maxElements;
      this.nodesToShow = visible.slice(start, end);
    } else {
      this.nodesToShow = null;
    }
    if (this.nodesToShow) {
      this.dataSource = this.nodesToShow;
    }
  }
  /** Whether the visor runs an older version than the newest one in the list. */
  isOutdated(node) {
    const v = versionParts(node.version);
    return !!v && !!this.newestVersion && compareVersions(v, this.newestVersion) < 0;
  }
  matchesQuickFilter(node) {
    switch (this.quickChip) {
      case "online":
        if (!node.online) {
          return false;
        }
        break;
      case "offline":
        if (node.online) {
          return false;
        }
        break;
      case "outdated":
        if (!this.isOutdated(node)) {
          return false;
        }
        break;
      case "no-reward":
        if (node.rewardsAddress) {
          return false;
        }
        break;
    }
    if (this.appFilter && !runsApp(node, this.appFilter)) {
      return false;
    }
    const q = this.quickSearch.trim().toLowerCase();
    if (!q) {
      return true;
    }
    return [node.label, node.localPk, node.ip, node.publicIp, node.cityName, node.regionName, node.countryCode, node.version].some((s) => !!s && s.toLowerCase().includes(q));
  }
  /** A sub-hypervisor section's visors that pass the search box and chip. */
  quickFiltered(nodes) {
    return (nodes || []).filter((n) => this.matchesQuickFilter(n));
  }
  /** Whether a row is the hypervisor serving this UI, which keeps the gold star. */
  isLocalHypervisor(node) {
    return this.localHypervisorPk ? node.localPk === this.localHypervisorPk : !!node.isHypervisor;
  }
  /** The sub-section a row's visor runs as hypervisor, if any. */
  sectionOf(pk) {
    return this.subSections.find((s) => s.hypervisorPk === pk);
  }
  /** A sub-hypervisor's visors for nesting under its row, without its own row. */
  childNodes(section) {
    return this.quickFiltered(section.nodes).filter((n) => n.localPk !== section.hypervisorPk);
  }
  toggleSection(pk) {
    if (!this.collapsedSections.delete(pk)) {
      this.collapsedSections.add(pk);
    }
    try {
      localStorage.setItem(collapsedSectionsKey, JSON.stringify([...this.collapsedSections]));
    } catch (e) {
    }
  }
  setQuickSearch(text) {
    this.quickSearch = text;
    this.recalculateElementsToShow();
    this.changeDetectorRef.markForCheck();
  }
  setQuickChip(chip) {
    this.quickChip = this.quickChip === chip ? "all" : chip;
    this.recalculateElementsToShow();
    this.changeDetectorRef.markForCheck();
  }
  toggleCompact() {
    this.compact = !this.compact;
    try {
      localStorage.setItem(compactKey, String(this.compact));
    } catch (e) {
    }
  }
  setAppFilter(name) {
    this.appFilter = name;
    this.recalculateElementsToShow();
    this.changeDetectorRef.markForCheck();
  }
  toggleApps() {
    this.showApps = !this.showApps;
    try {
      localStorage.setItem(appsColumnKey, String(this.showApps));
    } catch (e) {
    }
  }
  nodeAppChips(node) {
    return appChips(node);
  }
  /** The per-type transport counts as one line each, for the compact row's tooltip. */
  transportBreakdown(node) {
    return this.getTransportCounts(node).map((e) => `${e.type} ${e.count} (${e.incoming}\u2193 ${e.outgoing}\u2191)`).join("\n");
  }
  toggleSelected(node) {
    if (this.selected.has(node.localPk)) {
      this.selected.delete(node.localPk);
    } else {
      this.selected.add(node.localPk);
    }
  }
  /** Ticks every visor on this page, or clears them all when all are already ticked. */
  toggleSelectPage() {
    const page = this.dataSource || [];
    if (page.length > 0 && page.every((n) => this.selected.has(n.localPk))) {
      page.forEach((n) => this.selected.delete(n.localPk));
    } else {
      page.forEach((n) => this.selected.add(n.localPk));
    }
  }
  get pageAllSelected() {
    const page = this.dataSource || [];
    return page.length > 0 && page.every((n) => this.selected.has(n.localPk));
  }
  clearSelection() {
    this.selected.clear();
  }
  /** The ticked visors that are still in the list, sub-hypervisor sections included. */
  selectedNodes() {
    const seen = /* @__PURE__ */ new Set();
    return (this.allNodes || []).concat(...this.subSections.map((s) => s.nodes || [])).filter((n) => {
      if (!this.selected.has(n.localPk) || seen.has(n.localPk)) {
        return false;
      }
      seen.add(n.localPk);
      return true;
    });
  }
  /** Opens the dialog that adds, starts or stops an app on each ticked visor. */
  appActionSelected(mode) {
    const direct = new Set((this.allNodes || []).map((n) => n.localPk));
    const nodes = this.selectedNodes().map((n) => {
      return {
        key: n.localPk,
        label: n.label,
        online: !!n.online,
        viaSubHypervisor: !direct.has(n.localPk),
        apps: (n.apps || []).map((a) => a.name)
      };
    });
    BulkAppActionComponent.openDialog(this.dialog, { mode, nodes }).afterClosed().subscribe((changed) => {
      if (changed) {
        this.multipleNodeDataService.forceRefresh();
      }
      this.changeDetectorRef.markForCheck();
    });
  }
  updateSelected() {
    this.updateAll(this.selectedNodes());
  }
  rewardsSelected() {
    this.changeRewardsToAll(this.selectedNodes());
  }
  /** Asks each ticked online visor to restart, after a confirmation. */
  restartSelected() {
    const nodes = this.selectedNodes().filter((n) => n.online);
    if (nodes.length === 0) {
      this.snackbarService.showError("nodes.bulk.none-online");
      return;
    }
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, this.translateService.instant("nodes.bulk.restart-confirmation", { n: nodes.length }));
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();
      nodes.forEach((n) => this.nodeService.restart(n.localPk).subscribe({
        next: () => {
          this.snackbarService.showDone(this.translateService.instant("nodes.bulk.restarting", { name: n.label }));
          this.changeDetectorRef.markForCheck();
        },
        error: () => {
          this.snackbarService.showError(this.translateService.instant("nodes.bulk.restart-error", { name: n.label }));
          this.changeDetectorRef.markForCheck();
        }
      }));
      this.changeDetectorRef.markForCheck();
    });
  }
  /**
   * Pre-computes the numeric counts the new column sorters need
   * (transports + services). Mutates the node in place. Safe to
   * re-run on each refresh — the values come from already-loaded
   * node fields, no extra API calls.
   */
  annotateForSort(node) {
    node.transportsCount = node.transports ? node.transports.length : 0;
    node.servicesCount = this.getNodeServices(node).length;
    node.dmsgServerCount = this.getDmsgServerCount(node);
  }
  /**
   * Total count of dmsg servers this visor is connected to. Prefers the
   * rich dmsgServers list (per-server pk + carrier), falls back to the
   * legacy connectedDmsgServers array, then to a single server if only a
   * primary dmsgServerPk is known. Used both by the sort column and as
   * the "Total" line in the DMSG cell.
   */
  getDmsgServerCount(node) {
    if (node.dmsgServers && node.dmsgServers.length > 0) {
      return node.dmsgServers.length;
    }
    if (node.connectedDmsgServers && node.connectedDmsgServers.length > 0) {
      return node.connectedDmsgServers.length;
    }
    return node.dmsgServerPk ? 1 : 0;
  }
  /**
   * Maps a dmsg server's transport protocol (as reported by the visor:
   * tcp | ws | wss | webtransport | quic | skynet) to the short carrier label
   * shown in the DMSG cell, mirroring how the Transports cell shows a
   * short uppercase type. Unknown/blank protocols fall back to "?".
   */
  dmsgCarrierLabel(protocol) {
    switch ((protocol || "").toLowerCase()) {
      case "wss":
        return "WSS";
      case "ws":
        return "WS";
      case "webtransport":
        return "WT";
      case "tcp":
        return "TCP";
      case "quic":
        return "QUIC";
      case "skynet":
        return "SKY";
      default:
        return "?";
    }
  }
  /**
   * Returns per-carrier counts of the dmsg servers a node is connected to
   * ({type, count}), so the DMSG cell breaks its total down by carrier the
   * same way the Transports cell breaks its total down by transport type.
   * Only available when the rich dmsgServers list carries per-server
   * protocols; empty otherwise (the cell then shows just the total).
   */
  getDmsgCounts(node) {
    if (!node.dmsgServers || node.dmsgServers.length === 0) {
      return [];
    }
    const counts = {};
    node.dmsgServers.forEach((s) => {
      const carrier = this.dmsgCarrierLabel(s.protocol);
      counts[carrier] = (counts[carrier] || 0) + 1;
    });
    return Object.keys(counts).sort().map((k) => {
      return { type: k, count: counts[k] };
    });
  }
  /**
   * Reorders `nodes` so any PK present in customNodeOrder comes
   * first (in the saved order), followed by everything else in its
   * existing (column-sorter-imposed) order. Doesn't allocate when
   * customNodeOrder is empty.
   */
  applyCustomOrder(nodes) {
    if (this.customNodeOrder.length === 0) {
      return nodes;
    }
    const byPk = /* @__PURE__ */ new Map();
    nodes.forEach((n) => byPk.set(n.localPk, n));
    const ordered = [];
    const seen = /* @__PURE__ */ new Set();
    for (const pk of this.customNodeOrder) {
      const n = byPk.get(pk);
      if (n) {
        ordered.push(n);
        seen.add(pk);
      }
    }
    nodes.forEach((n) => {
      if (!seen.has(n.localPk)) {
        ordered.push(n);
      }
    });
    return ordered;
  }
  /**
   * cdkDropList drop handler. Reorders the visible page in place and
   * persists the resulting PK order to localStorage. The whole-list
   * order (across pagination) is reconstructed by capturing the
   * current filteredNodes PK order with the drop applied; rows on
   * other pages keep their existing slot.
   */
  rowDropped(event) {
    if (!this.nodesToShow || event.previousIndex === event.currentIndex) {
      return;
    }
    moveItemInArray(this.nodesToShow, event.previousIndex, event.currentIndex);
    this.dataSource = this.nodesToShow;
    const pageSize = AppConfig.maxFullListElements;
    const start = pageSize * (this.currentPage - 1);
    const newFiltered = this.filteredNodes.slice();
    for (let i = 0; i < this.nodesToShow.length; i++) {
      newFiltered[start + i] = this.nodesToShow[i];
    }
    this.filteredNodes = newFiltered;
    this.customNodeOrder = newFiltered.map((n) => n.localPk);
    this.saveLocalValue(this.customNodeOrderKey, JSON.stringify(this.customNodeOrder));
  }
  /**
   * Clears the user's drag-drop custom order. Triggered by the
   * column-sort headers (which would otherwise be ignored for any
   * rows the user manually arranged). Without this, clicking a
   * column header on a list where the user had dragged rows would
   * leave the rows stuck where they were, surprising operators
   * who expected a sort to fully take effect.
   */
  resetCustomOrder() {
    if (this.customNodeOrder.length === 0) {
      return;
    }
    this.customNodeOrder = [];
    this.saveLocalValue(this.customNodeOrderKey, "");
  }
  /**
   * Wrapper around dataSorter.changeSortingOrder that also clears
   * any active custom drag-drop order, then re-paginates so the
   * fresh sort takes effect immediately. Used by the column header
   * (click) handlers in node-list.component.html so a column sort
   * always wins over a previously-set custom order.
   */
  changeSort(column) {
    this.resetCustomOrder();
    this.dataSorter.changeSortingOrder(column);
  }
  logout() {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, "common.logout-confirmation");
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.closeModal();
      this.authService.logout().subscribe(() => {
        this.router.navigate(["login"]);
        this.changeDetectorRef.markForCheck();
      }, () => {
        this.snackbarService.showError("common.logout-error");
        this.changeDetectorRef.markForCheck();
      });
      this.changeDetectorRef.markForCheck();
    });
  }
  // Updates all visors.
  updateAll(nodes = this.dataSource) {
    if (!nodes || nodes.length === 0) {
      this.snackbarService.showError("nodes.no-visors-to-update");
      return;
    }
    const updatableNodes = [];
    const nonUpdatableNodes = [];
    nodes.forEach((node) => {
      if (node.online) {
        const nodeData = {
          key: node.localPk,
          label: node.label,
          version: node.version,
          tag: node.buildTag
        };
        if (GeneralUtils.checkIfTagIsUpdatable(node.buildTag)) {
          updatableNodes.push(nodeData);
        } else {
          nonUpdatableNodes.push(nodeData);
        }
      }
    });
    UpdateAllComponent.openDialog(this.dialog, updatableNodes, nonUpdatableNodes);
  }
  // Opens the modal window for changing the rewards address of all online nodes.
  changeRewardsToAll(nodes = this.dataSource) {
    if (!nodes || nodes.length === 0) {
      this.snackbarService.showError("nodes.no-visors-to-modify");
      return;
    }
    const nodesToModify = [];
    nodes.forEach((node) => {
      if (node.online) {
        const nodeData = {
          key: node.localPk,
          label: node.label
        };
        nodesToModify.push(nodeData);
      }
    });
    const params = { nodes: nodesToModify };
    BulkRewardAddressChangerComponent.openDialog(this.dialog, params);
  }
  /**
   * Recursively updates the visors in the list. It returns how many visors the function was not
   * able to update.
   * @param keys Keys of the visors to update. The list will be altered by the function.
   * @param labels Labels of the visors to update. The list will be altered by the function.
   * @param errors Errors found during the process. For internal use.
   */
  recursivelyUpdateWallets(keys, labels, errors = 0) {
    return this.nodeService.update(keys[keys.length - 1]).pipe(catchError(() => {
      return of(null);
    }), mergeMap((response) => {
      if (response && response.updated && !response.error) {
        this.snackbarService.showDone(this.translateService.instant("nodes.update.done", { name: labels[labels.length - 1] }));
      } else {
        this.snackbarService.showError(this.translateService.instant("nodes.update.update-error", { name: labels[labels.length - 1] }));
        errors += 1;
      }
      keys.pop();
      labels.pop();
      if (keys.length >= 1) {
        return this.recursivelyUpdateWallets(keys, labels, errors);
      }
      return of(errors);
    }));
  }
  /**
   * Opens the modal window used on small screens with the options of an element.
   */
  showOptionsDialog(node) {
    const options = [
      {
        icon: "filter_none",
        label: "nodes.copy-key"
      }
    ];
    options.push({
      icon: "short_text",
      label: "labeled-element.edit-label"
    });
    if (!node.online) {
      options.push({
        icon: "close",
        label: "nodes.delete-node"
      });
    }
    SelectOptionComponent.openDialog(this.dialog, options, "common.options").afterClosed().subscribe((selectedOption) => {
      if (selectedOption === 1) {
        this.copySpecificTextToClipboard(node.localPk);
      } else if (selectedOption === 2) {
        this.showEditLabelDialog(node);
      } else if (selectedOption === 3) {
        this.deleteNode(node);
      }
      this.changeDetectorRef.markForCheck();
    });
  }
  /**
   * Copies the public key of a visor to the clipboard.
   */
  copyToClipboard(node) {
    this.copySpecificTextToClipboard(node.localPk);
  }
  /**
   * Copies a text to the clipboard.
   * @param text Text to copy.
   */
  copySpecificTextToClipboard(text) {
    if (this.clipboardService.copy(text)) {
      this.snackbarService.showDone("copy.copied");
    }
  }
  /**
   * Opens the modal window for changing the label of a node.
   */
  showEditLabelDialog(node) {
    let labelInfo = this.storageService.getLabelInfo(node.localPk);
    if (!labelInfo) {
      labelInfo = {
        id: node.localPk,
        label: "",
        identifiedElementType: LabeledElementTypes.Node
      };
    }
    EditLabelComponent.openDialog(this.dialog, labelInfo).afterClosed().subscribe((changed) => {
      if (changed) {
        this.forceDataRefresh();
      }
      this.changeDetectorRef.markForCheck();
    });
  }
  /**
   * Removes an offline node from the list, until seeing it online again.
   */
  deleteNode(node) {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, "nodes.delete-node-confirmation");
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();
      this.storageService.setLocalNodesAsHidden([node.localPk], [node.ip]);
      this.forceDataRefresh();
      this.snackbarService.showDone("nodes.deleted");
      this.changeDetectorRef.markForCheck();
    });
  }
  /**
   * Returns an array of {type, count, incoming, outgoing} entries for
   * transport types on the given node. Direction is from the node's
   * perspective: outgoing = this visor dialed it (transport.initiator),
   * incoming = it accepted an inbound dial. Types are displayed uppercase.
   */
  getTransportCounts(node) {
    if (!node.transports || node.transports.length === 0) {
      return [];
    }
    const counts = {};
    node.transports.forEach((t) => {
      const tp = (t.type || "unknown").toUpperCase();
      if (tp === "?") {
        return;
      }
      if (!counts[tp]) {
        counts[tp] = { count: 0, incoming: 0, outgoing: 0 };
      }
      counts[tp].count++;
      if (t.initiator) {
        counts[tp].outgoing++;
      } else {
        counts[tp].incoming++;
      }
    });
    return Object.keys(counts).sort().map((k) => ({
      type: k,
      count: counts[k].count,
      incoming: counts[k].incoming,
      outgoing: counts[k].outgoing
    }));
  }
  /**
   * Returns the total incoming/outgoing transport counts for a node,
   * summed across all (non-placeholder) types. Used for the Total line
   * in the Transports column.
   */
  getTransportTotals(node) {
    let incoming = 0;
    let outgoing = 0;
    if (node.transports) {
      node.transports.forEach((t) => {
        if ((t.type || "").toUpperCase() === "?") {
          return;
        }
        if (t.initiator) {
          outgoing++;
        } else {
          incoming++;
        }
      });
    }
    return { incoming, outgoing };
  }
  /**
   * Returns an array of service display names active on the given node.
   */
  getNodeServices(node) {
    const services = [];
    if (node.apps) {
      node.apps.forEach((app) => {
        if (app.status === 1) {
          if (app.name === "skysocks") {
            services.push("Proxy Server");
          } else if (app.name === "vpn-server") {
            services.push("VPN Server");
          }
        }
      });
    }
    if (node.isPublic) {
      services.push("Public Visor");
    }
    if (node.autoconnectTransports) {
      services.push("Autoconnect");
    }
    return services;
  }
  /**
   * Loads reward data for all known visors.
   */
  loadRewardData() {
    if (!this.allNodes || this.allNodes.length === 0) {
      return;
    }
    this.rewardDataLoading = true;
    const pks = this.allNodes.map((n) => n.localPk);
    this.rewardService.fetchRewardData(pks).subscribe((dataMap) => {
      this.rewardDataMap = dataMap;
      this.rewardDates = this.rewardService.getCachedDates();
      this.rewardDateHeaders = this.rewardDates.map((d) => this.rewardService.formatDateShort(d));
      this.rewardDataLoading = false;
      this.rewardDataLoaded = true;
      this.changeDetectorRef.markForCheck();
    });
  }
  /**
   * Returns the reward data for a given visor PK, or null if not available.
   */
  getRewardData(pk) {
    return this.rewardDataMap.get(pk) || null;
  }
  /**
   * Returns the reward amount for a visor on a given date, formatted for display.
   */
  getRewardAmount(pk, date) {
    const data = this.rewardDataMap.get(pk);
    if (!data || !data.dailyAmounts[date] || data.dailyAmounts[date] === 0) {
      return "-";
    }
    return data.dailyAmounts[date].toFixed(2);
  }
  /**
   * Returns the CSS class for a reward cell based on sent status.
   */
  getRewardClass(pk, date) {
    const data = this.rewardDataMap.get(pk);
    if (!data || !data.dailyAmounts[date] || data.dailyAmounts[date] === 0) {
      return "";
    }
    return data.dailySent[date] ? "reward-sent" : "reward-pending";
  }
  /**
   * Returns the week total for a visor, formatted for display.
   */
  getWeekTotal(pk) {
    const data = this.rewardDataMap.get(pk);
    if (!data || data.weekTotal === 0) {
      return "-";
    }
    return data.weekTotal.toFixed(2);
  }
  /**
   * Returns the reward address for a visor, or "-" if not set.
   */
  getRewardAddress(pk) {
    const node = this.allNodes?.find((n) => n.localPk === pk);
    if (node?.rewardsAddress) {
      return node.rewardsAddress;
    }
    const data = this.rewardDataMap.get(pk);
    if (data?.rewardAddress) {
      return data.rewardAddress;
    }
    return "-";
  }
  /**
   * Removes all offline nodes from the list, until seeing them online again.
   */
  removeOffline() {
    let confirmationText = "nodes.delete-all-offline-confirmation";
    if (this.dataFilterer.currentFiltersTexts && this.dataFilterer.currentFiltersTexts.length > 0) {
      confirmationText = "nodes.delete-all-filtered-offline-confirmation";
    }
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, confirmationText);
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();
      const nodesToRemove = [];
      const ipsToRemove = [];
      this.filteredNodes.forEach((node) => {
        if (!node.online) {
          nodesToRemove.push(node.localPk);
          ipsToRemove.push(node.ip);
        }
      });
      if (nodesToRemove.length > 0) {
        this.storageService.setLocalNodesAsHidden(nodesToRemove, ipsToRemove);
        this.forceDataRefresh();
        if (nodesToRemove.length === 1) {
          this.snackbarService.showDone("nodes.deleted-singular");
        } else {
          this.snackbarService.showDone("nodes.deleted-plural", { number: nodesToRemove.length });
        }
      }
      this.changeDetectorRef.markForCheck();
    });
  }
  static {
    this.ctorParameters = () => [
      { type: NodeService },
      { type: MultipleNodeDataService },
      { type: Router },
      { type: MatDialog },
      { type: AuthService },
      { type: StorageService },
      { type: NgZone },
      { type: SnackbarService },
      { type: ClipboardService },
      { type: TranslateService },
      { type: RewardService },
      { type: ActivatedRoute },
      { type: ChangeDetectorRef }
    ];
  }
};
NodeListComponent = __decorate4([
  Component({
    selector: "app-node-list",
    template: node_list_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [node_list_component_default2]
  })
], NodeListComponent);

export {
  versionParts,
  compareVersions,
  runsApp,
  fleetAppNames,
  appChips,
  NodeListComponent
};
//# debugId=33bef5a7-8b6e-5d70-814f-cc1832bf2432
//# sourceMappingURL=chunk-T47OQHHJ.js.map
