import {
  needsAcceptRisk,
  rewardAddressValidator
} from "./chunk-CDB4CLQF.js";
import {
  GeneralUtils,
  ptyUrl
} from "./chunk-KWVFHEEW.js";
import {
  UntypedFormBuilder,
  Validators
} from "./chunk-B4GMTCLR.js";
import {
  AppConfig,
  MAT_DIALOG_DATA,
  MatDialog,
  MatDialogConfig,
  MatDialogRef
} from "./chunk-CAA347WM.js";
import {
  LiveService,
  LiveUnavailableError
} from "./chunk-GPHL7TNH.js";
import {
  NodeService
} from "./chunk-XBRWDJ2O.js";
import {
  processServiceError
} from "./chunk-ZMZW6HXB.js";
import {
  StorageService
} from "./chunk-XOUXCPTJ.js";
import {
  BehaviorSubject,
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  Component,
  EMPTY,
  Inject,
  Injectable,
  ViewChild,
  catchError,
  delay,
  mergeMap,
  of,
  tap
} from "./chunk-ZNLGPAUZ.js";

// src/app/services/multiple-node-data.service.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var MultipleNodesBackendData = class {
};
var MultipleNodeDataService = class MultipleNodeDataService2 {
  constructor(storageService, nodeService, live) {
    this.storageService = storageService;
    this.nodeService = nodeService;
    this.live = live;
    this.dataSubject = new BehaviorSubject(null);
    this.lastEmitedData = new MultipleNodesBackendData();
    this.firstCallToGetDataMade = false;
    this.storageService.getRefreshTimeObservable().subscribe((val) => {
      this.dataRefreshDelay = val * 1e3;
      this.forceRefresh();
    });
  }
  /**
   * Makes the service start returning the node list. Returns an observable for
   * getting the data.
   */
  startRequestingData() {
    if (!this.firstCallToGetDataMade) {
      this.startUpdates();
    }
    return this.dataSubject.asObservable();
  }
  /**
   * Makes the service stop returning the node list.
   */
  stopRequestingData() {
    if (this.updateSubscription) {
      this.updateSubscription.unsubscribe();
      this.firstCallToGetDataMade = false;
    }
  }
  /** Follows the list over the live socket when possible, else polls. */
  startUpdates() {
    if (this.live.usable) {
      this.startLiveSubscription();
    } else {
      this.getData(0);
    }
  }
  /**
   * Gets the list pushed over the live socket, which sends it at once and then
   * whenever it changes. A new subscription also serves as a forced refresh.
   * Falls back to polling if the socket cannot be used.
   */
  startLiveSubscription() {
    this.firstCallToGetDataMade = true;
    if (this.updateSubscription) {
      this.updateSubscription.unsubscribe();
    }
    this.updateSubscription = this.live.subscribe("/api/visors-tree-summary", this.dataRefreshDelay).pipe(mergeMap((ev) => {
      if (ev.status === 429) {
        throw new LiveUnavailableError();
      }
      if (ev.status >= 200 && ev.status < 300) {
        return of(this.nodeService.nodesTreeFromResponse(JSON.parse(ev.body)));
      }
      return this.nodeService.getNodesTree().pipe(catchError((err) => {
        this.emitError(err);
        return EMPTY;
      }));
    })).subscribe({
      next: (sections) => this.emitSections(sections),
      error: () => this.getData(0)
    });
  }
  /**
   * Starts periodically getting the node list.
   * @param delayMs Delay before loading the data.
   */
  getData(delayMs) {
    this.firstCallToGetDataMade = true;
    if (this.updateSubscription) {
      this.updateSubscription.unsubscribe();
    }
    this.updateSubscription = of(1).pipe(
      // Wait the requested delay.
      delay(delayMs),
      // Additional steps for making sure the UI shows the animation (important in case of quick errors).
      tap(() => {
        this.lastEmitedData.updating = true;
        this.dataSubject.next(this.lastEmitedData);
      }),
      delay(120),
      // Load the data. Use the tree endpoint as the primary source —
      // it carries the per-sub-hypervisor structure the UI needs to
      // render multi-table layout. The flat `data` field is derived
      // from sections by concatenating each section's nodes; consumers
      // that don't care about sections keep working unchanged.
      mergeMap(() => this.nodeService.getNodesTree())
    ).subscribe((sections) => {
      this.emitSections(sections);
      this.getData(this.dataRefreshDelay);
    }, (err) => {
      this.emitError(err);
      this.getData(AppConfig.connectionRetryDelay);
    });
  }
  emitSections(sections) {
    const flat = sections.length > 0 ? sections[0].nodes.slice() : [];
    this.lastEmitedData = {
      data: flat,
      sections,
      error: null,
      momentOfLastCorrectUpdate: Date.now(),
      updating: false
    };
    this.dataSubject.next(this.lastEmitedData);
  }
  emitError(err) {
    err = processServiceError(err);
    this.lastEmitedData = {
      data: this.lastEmitedData.data,
      sections: this.lastEmitedData.sections,
      error: err,
      momentOfLastCorrectUpdate: this.lastEmitedData.momentOfLastCorrectUpdate,
      updating: false
    };
    this.dataSubject.next(this.lastEmitedData);
  }
  /**
   * Makes the service immediately refresh the node list.
   */
  forceRefresh() {
    this.startUpdates();
  }
  static {
    this.ctorParameters = () => [
      { type: StorageService },
      { type: NodeService },
      { type: LiveService }
    ];
  }
};
MultipleNodeDataService = __decorate([
  Injectable({
    providedIn: "root"
  })
], MultipleNodeDataService);

// angular:jit:template:src/app/components/layout/update-all/update-all.component.html
var update_all_component_default = `<app-dialog [headline]="'update-all.title' | translate" [dialog]="dialogRef">
  @if (updatableNodes && updatableNodes.length > 0) {
    <div class="text-container">
      {{ 'update-all.updatable-list-text' | translate }}
    </div>
    <div class="list-container">
      @for (node of updatableNodes; track node) {
        <div class="list-element">
          <div class="left-part">
            <div class="name">{{node.label}}</div>
            <div class="version" [matTooltip]="node.version">{{ node.version | versionLabel }}</div>
          </div>
          <div class="right-part">
            <app-button color="primary" (click)="openTerminal(node.key)">
              {{ 'update-all.update-button' | translate }}
            </app-button>
          </div>
        </div>
      }
    </div>
  }

  @if (nonUpdatableNodes && nonUpdatableNodes.length > 0) {
    <div class="text-container">
      {{ 'update-all.non-updatable-list-text' | translate }}
    </div>
    <div class="list-container">
      @for (node of nonUpdatableNodes; track node) {
        <div class="list-element">
          <div class="left-part">
            <div class="name">{{node.label}}</div>
            <div class="version" [matTooltip]="node.version">{{ node.version | versionLabel }}</div>
            @if (node.tag) {
              <div class="version">{{node.tag}}</div>
            }
          </div>
        </div>
      }
    </div>
  }
</app-dialog>
`;

// angular:jit:style:src/app/components/layout/update-all/update-all.component.scss
var update_all_component_default2 = "/* src/app/components/layout/update-all/update-all.component.scss */\n.text-container {\n  word-break: break-word;\n}\n.list-container {\n  font-size: 14px;\n  margin: 10px;\n  color: #215f9e;\n  word-break: break-word;\n  line-height: 1.2;\n}\n.list-container .list-element {\n  display: flex;\n  margin-bottom: 10px;\n}\n.list-container .list-element .left-part {\n  flex-grow: 1;\n  flex-shrink: 1;\n  align-self: center;\n  margin-right: 10px;\n  min-width: 0;\n}\n.list-container .list-element .left-part .name {\n  display: block;\n  white-space: nowrap;\n  overflow: hidden;\n  text-overflow: ellipsis;\n}\n@media (max-width: 575px) {\n  .list-container .list-element .left-part .name {\n    font-size: 0.7rem;\n  }\n}\n.list-container .list-element .left-part .version {\n  font-size: 0.7rem;\n  color: #777;\n  white-space: nowrap;\n  overflow: hidden;\n  text-overflow: ellipsis;\n}\n.list-container .list-element .right-part {\n  flex-basis: 0;\n  flex-shrink: 0;\n}\n.list-container .details {\n  color: #777;\n}\n/*# sourceMappingURL=update-all.component.css.map */\n";

// src/app/components/layout/update-all/update-all.component.ts
var __decorate2 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var UpdateAllComponent_1;
var UpdateAllComponent = class UpdateAllComponent2 {
  static {
    UpdateAllComponent_1 = this;
  }
  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   * @param updatableNodes List with the nodes that can be updated with the terminal.
   * @param nonUpdatableNodes List with the nodes that can not be updated with the terminal.
   */
  static openDialog(dialog, updatableNodes, nonUpdatableNodes) {
    const config = new MatDialogConfig();
    config.data = [updatableNodes, nonUpdatableNodes];
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;
    return dialog.open(UpdateAllComponent_1, config);
  }
  constructor(dialogRef, data) {
    this.dialogRef = dialogRef;
    this.updatableNodes = data[0];
    this.nonUpdatableNodes = data[1];
  }
  openTerminal(key) {
    window.open(ptyUrl(key, "?commands=update"), "_blank", "noopener noreferrer");
  }
  static {
    this.ctorParameters = () => [
      { type: MatDialogRef },
      { type: Array, decorators: [{ type: Inject, args: [MAT_DIALOG_DATA] }] }
    ];
  }
};
UpdateAllComponent = UpdateAllComponent_1 = __decorate2([
  Component({
    selector: "app-update-all",
    template: update_all_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [update_all_component_default2]
  })
], UpdateAllComponent);

// angular:jit:template:src/app/components/layout/bulk-reward-address-changer/bulk-reward-address-changer.component.html
var bulk_reward_address_changer_component_default = `<app-dialog [headline]="'bulk-rewards.title' | translate" [dialog]="dialogRef" [disableDismiss]="disableDismiss">
  <form [formGroup]="form">
    <!-- Info. -->
    <div class="text-container">
      <span>{{ 'bulk-rewards.info' | translate }} </span>
      <a href="https://github.com/skycoin/skywire/blob/develop/rewards/mainnet_rules.md" target="_blank" rel="noreferrer nofollow noopener">
        {{'bulk-rewards.more-info-link' | translate}}
      </a>
    </div>

    <!-- Address field. -->
    <mat-form-field>
      <div class="field-container">
        <label class="field-label" for="remoteKey">{{ 'rewards-address-config.address' | translate }}</label>
        <input
          [ngClass]="{'element-disabled' : processingStarted}"
          formControlName="address"
          maxlength="112"
          matInput
          >
      </div>
      <mat-error>
        <span>{{ 'rewards-address-config.address-error' | translate }}</span>
      </mat-error>
    </mat-form-field>
    <mat-checkbox class="accept-risk" color="primary" formControlName="acceptRisk">
      <span class="accept-risk-text">{{ 'rewards-address-config.accept-risk' | translate }}</span>
    </mat-checkbox>

    <div class="text-container">
      {{ 'bulk-rewards.select-visors' | translate }}
    </div>

    <div class="list-container" formArrayName="nodes">

      <!-- Nodes list, before starting the procedure. -->
      @for (node of nodesToEdit; track node; let i = $index) {
        <div class="list-element" [ngClass]="{'element-margin' : processingStarted}">
          @if (!processingStarted) {
            <ng-container [formGroupName]="i">
              <mat-checkbox color="primary" formControlName="selected">
                <div>
                  <div class="contents">
                    {{ node.label }}
                  </div>
                  <div class="address contents">
                    <span class="address-label">{{ 'bulk-rewards.current-address' | translate }}</span>
                    @if (node.currentAddress === null && !node.operationError) {
                      <span class="blinking"> {{ 'bulk-rewards.checking' | translate }}</span>
                    }
                    @if (node.operationError) {
                      <span class="red-text">
                        <span> {{ 'bulk-rewards.error-checking' | translate }}</span>
                        <span> {{ node.operationError | translate }}</span>
                      </span>
                    }
                    @if (node.currentAddress && !node.operationError) {
                      <span> {{ node.currentAddress }}</span>
                    }
                    @if (node.currentAddress === '' && !node.operationError) {
                      <span> {{ 'bulk-rewards.not-registered' | translate }}</span>
                    }
                  </div>
                </div>
              </mat-checkbox>
            </ng-container>
          }
          <!-- Nodes list, after starting the procedure. -->
          @if (processingStarted) {
            <div class="left-area">-</div>
            <div class="right-area contents">
              {{ node.label }}
              <div class="address">
                @if (node.processing && !node.operationError) {
                  <span class="blinking"> {{ 'bulk-rewards.processing' | translate }}</span>
                }
                @if (node.operationError) {
                  <span class="red-text">
                    <span> {{ 'bulk-rewards.error-processing' | translate }}</span>
                    <span> {{ node.operationError | translate }}</span>
                  </span>
                }
                @if (!node.processing && !node.operationError) {
                  <span class="green-text"> {{ 'bulk-rewards.done' | translate }}</span>
                }
              </div>
            </div>
          }
        </div>
      }

    </div>
  </form>

  <!-- Buttons. -->
  <div class="buttons">
    <app-button
      #button
      type="mat-raised-button"
      color="primary"
      (action)="!processingStarted ? checkBeforeProcessing() : closeModal()"
      [disabled]="!formValid()"
      >
      @if (!processingStarted) {
        {{ 'bulk-rewards.perform-changes' | translate }}
      }
      @if (processingStarted) {
        {{ 'common.close' | translate }}
      }
    </app-button>
  </div>
</app-dialog>
`;

// angular:jit:style:src/app/components/layout/bulk-reward-address-changer/bulk-reward-address-changer.component.scss
var bulk_reward_address_changer_component_default2 = "/* src/app/components/layout/bulk-reward-address-changer/bulk-reward-address-changer.component.scss */\n.text-container {\n  word-break: break-word;\n}\nmat-form-field {\n  margin-top: 10px;\n}\n.list-container {\n  font-size: 14px;\n  margin: 10px;\n  color: #215f9e;\n  word-break: break-word;\n}\n.list-container .element-margin {\n  margin: 15px 0;\n}\n.list-container .list-element {\n  display: flex;\n}\n.list-container .list-element .left-area {\n  width: 12px;\n  flex-grow: 0;\n  flex-shrink: 0;\n}\n.list-container .list-element .right-area {\n  flex-grow: 1;\n}\n.list-container .list-element .contents {\n  white-space: normal;\n  line-height: 1.2;\n}\n.list-container .list-element .address {\n  font-size: 0.7rem;\n  color: #777;\n}\n.list-container .list-element .address .address-label {\n  opacity: 0.7;\n}\n.buttons {\n  margin-top: 15px;\n  text-align: right;\n}\n.buttons app-button {\n  margin-left: 5px;\n}\n:host ::ng-deep .list-container .mdc-label {\n  color: #215f9e !important;\n}\n.accept-risk {\n  display: block;\n  margin-bottom: 10px;\n}\n.accept-risk-text {\n  white-space: normal;\n  font-size: 0.85rem;\n}\n:host ::ng-deep .accept-risk .mdc-label {\n  color: #215f9e !important;\n}\n/*# sourceMappingURL=bulk-reward-address-changer.component.css.map */\n";

// src/app/components/layout/bulk-reward-address-changer/bulk-reward-address-changer.component.ts
var __decorate3 = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var BulkRewardAddressChangerComponent_1;
var BulkRewardAddressChangerComponent = class BulkRewardAddressChangerComponent2 {
  static {
    BulkRewardAddressChangerComponent_1 = this;
  }
  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   * @param nodes Nodes to process.
   */
  static openDialog(dialog, nodes) {
    const config = new MatDialogConfig();
    config.data = nodes;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;
    return dialog.open(BulkRewardAddressChangerComponent_1, config);
  }
  constructor(dialogRef, data, nodeService, formBuilder, dialog, changeDetectorRef) {
    this.dialogRef = dialogRef;
    this.data = data;
    this.nodeService = nodeService;
    this.formBuilder = formBuilder;
    this.dialog = dialog;
    this.changeDetectorRef = changeDetectorRef;
    this.processingStarted = false;
    this.processingFinished = false;
    this.currentlyProcessed = 0;
    this.form = formBuilder.group({
      address: ["", Validators.compose([
        Validators.minLength(20),
        Validators.maxLength(112),
        rewardAddressValidator(() => !!this.form?.get("acceptRisk").value)
      ])],
      acceptRisk: [false],
      nodes: formBuilder.array([])
    });
    this.riskSubscription = this.form.get("acceptRisk").valueChanges.subscribe(() => {
      this.form.get("address").updateValueAndValidity();
      this.changeDetectorRef.markForCheck();
    });
    data.nodes.forEach((n) => {
      const nodeFormData = this.formBuilder.group({
        selected: [true]
      });
      this.form.get("nodes").push(nodeFormData);
    });
    this.startChecking();
  }
  formValid() {
    if (!this.processingStarted) {
      if (!this.form.valid) {
        return false;
      }
      let selected = 0;
      this.form.get("nodes").controls.forEach((n, i) => {
        if (n.get("selected")?.value) {
          selected += 1;
        }
      });
      return selected > 0;
    }
    return true;
  }
  /**
   * If true, all the ways provided by default by the UI for closing the modal window are disabled.
   */
  get disableDismiss() {
    return this.processingStarted && !this.processingFinished;
  }
  startChecking() {
    this.nodesToEdit = [];
    this.data.nodes.forEach((node) => {
      this.nodesToEdit.push({
        key: node.key,
        label: node.label,
        currentAddress: null,
        operationError: "",
        processing: false
      });
    });
    this.operationSubscriptions = [];
    this.nodesToEdit.forEach((node, i) => {
      this.operationSubscriptions.push(this.nodeService.getRewardsAddress(node.key).subscribe((response) => {
        this.nodesToEdit[i].currentAddress = response;
        this.changeDetectorRef.markForCheck();
      }, (err) => {
        this.nodesToEdit[i].operationError = err.translatableErrorMsg ? err.translatableErrorMsg : err.originalServerErrorMsg;
        this.changeDetectorRef.markForCheck();
      }));
    });
  }
  /**
   * Checks the data entered by the user and ask for confirmation, if needed, before starting the
   * saving procedure.
   */
  checkBeforeProcessing() {
    if (!this.form.valid) {
      return;
    }
    const address = this.form.get("address").value;
    if (address) {
      this.startProcessing();
    } else {
      const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, "bulk-rewards.empty-warning");
      confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
        confirmationDialog.componentInstance.closeModal();
        this.startProcessing();
        this.changeDetectorRef.markForCheck();
      });
    }
  }
  /**
   * Makes the changes on the selected nodes.
   */
  startProcessing() {
    this.processingStarted = true;
    this.button.showLoading();
    this.closeoperationSubscriptions();
    const newList = [];
    this.form.get("nodes").controls.forEach((n, i) => {
      if (n.get("selected")?.value) {
        this.nodesToEdit[i].operationError = "";
        this.nodesToEdit[i].processing = true;
        newList.push(this.nodesToEdit[i]);
      }
    });
    this.nodesToEdit = newList;
    const newAddress = this.form.get("address").value;
    const acceptRisk = needsAcceptRisk(newAddress, this.form.get("acceptRisk").value);
    this.form.get("address").disable();
    this.form.get("acceptRisk").disable();
    this.currentlyProcessed = 0;
    this.operationSubscriptions = [];
    this.nodesToEdit.forEach((node, i) => {
      let operation = this.nodeService.setRewardsAddress(node.key, newAddress, acceptRisk);
      if (!newAddress) {
        operation = this.nodeService.deleteRewardsAddress(node.key);
      }
      this.operationSubscriptions.push(of(0).pipe(delay(100), mergeMap(() => operation)).subscribe((response) => {
        this.nodesToEdit[i].processing = false;
        this.currentlyProcessed += 1;
        if (this.currentlyProcessed === this.nodesToEdit.length) {
          this.processingFinished = true;
          this.button.reset();
        }
        this.changeDetectorRef.markForCheck();
      }, (err) => {
        this.nodesToEdit[i].processing = false;
        this.nodesToEdit[i].operationError = err.translatableErrorMsg ? err.translatableErrorMsg : err.originalServerErrorMsg;
        this.currentlyProcessed += 1;
        if (this.currentlyProcessed === this.nodesToEdit.length) {
          this.processingFinished = true;
          this.button.reset();
        }
        this.changeDetectorRef.markForCheck();
      }));
    });
  }
  ngOnDestroy() {
    this.riskSubscription?.unsubscribe();
    this.closeoperationSubscriptions();
  }
  closeoperationSubscriptions() {
    if (this.operationSubscriptions) {
      this.operationSubscriptions.forEach((e) => e.unsubscribe());
    }
  }
  closeModal() {
    this.dialogRef.close();
  }
  static {
    this.ctorParameters = () => [
      { type: MatDialogRef },
      { type: void 0, decorators: [{ type: Inject, args: [MAT_DIALOG_DATA] }] },
      { type: NodeService },
      { type: UntypedFormBuilder },
      { type: MatDialog },
      { type: ChangeDetectorRef }
    ];
  }
  static {
    this.propDecorators = {
      button: [{ type: ViewChild, args: ["button"] }]
    };
  }
};
BulkRewardAddressChangerComponent = BulkRewardAddressChangerComponent_1 = __decorate3([
  Component({
    selector: "app-bulk-reward-address-changer",
    template: bulk_reward_address_changer_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [bulk_reward_address_changer_component_default2]
  })
], BulkRewardAddressChangerComponent);

export {
  MultipleNodeDataService,
  UpdateAllComponent,
  BulkRewardAddressChangerComponent
};
//# debugId=8cf0261e-9f5c-5ac6-b0f9-692843ccf421
//# sourceMappingURL=chunk-SW25Q345.js.map
