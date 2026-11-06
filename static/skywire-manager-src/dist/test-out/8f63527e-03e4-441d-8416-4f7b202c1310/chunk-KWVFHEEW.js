import {
  AppConfig,
  MAT_DIALOG_DATA,
  MatDialogConfig,
  MatDialogRef
} from "./chunk-CAA347WM.js";
import {
  ChangeDetectionStrategy,
  Component,
  EventEmitter,
  Inject,
  Output,
  ViewChild
} from "./chunk-ZNLGPAUZ.js";

// src/app/utils/home-tabs.ts
function isWasmHvCore() {
  const cfg = typeof window !== "undefined" && window.__SKYWIRE_HV__ || {};
  return !!(cfg.visor || cfg.standalone);
}
function homeTabsData() {
  const wasm = isWasmHvCore();
  return [
    // --- local: this hypervisor ---
    { icon: "view_headline", label: "nodes.title", linkParts: ["/nodes"], group: "local" },
    // "Local Visor" — jump straight to THIS hypervisor's own visor detail page
    // (the /nodes/local route resolves the local PK and redirects). Surfaces the
    // serving visor's controls without hunting for it in the list; for a
    // serverless wasm tab this is the in-browser visor itself.
    { icon: "router", label: "nodes.local-visor-title", linkParts: ["/nodes", "local"], group: "local" },
    { icon: "monetization_on", label: "nodes.rewards-title", linkParts: ["/nodes", "rewards"], group: "local", hidden: wasm },
    { icon: "memory", label: "nodes.resources-title", linkParts: ["/nodes", "resources"], group: "local", hidden: wasm },
    // --- network-wide ---
    { icon: "swap_horiz", label: "nodes.transports-title", linkParts: ["/nodes", "transports"], group: "network" },
    { icon: "public", label: "nodes.network-title", linkParts: ["/nodes", "network"], group: "network" },
    { icon: "bubble_chart", label: "node.details.tpviz.title", linkParts: ["/nodes", "visualizer"], group: "network" },
    { icon: "check_circle", label: "nodes.services-health-title", linkParts: ["/nodes", "services-health"], group: "network" },
    { icon: "schedule", label: "nodes.uptime-title", linkParts: ["/nodes", "uptime"], group: "network" },
    // --- meta ---
    { icon: "settings", label: "settings.title", linkParts: ["/settings"] }
  ];
}

// angular:jit:template:src/app/components/layout/confirmation/confirmation.component.html
var confirmation_component_default = `<app-dialog [headline]="(state !== confirmationStates.Done ? data.headerText : doneTitle) | translate" [dialog]="dialogRef" [disableDismiss]="disableDismiss">
  <div class="text-container">
    {{ (state !== confirmationStates.Done ? data.text : doneText) | translate }}
  </div>
  @if ((data.list && state !== confirmationStates.Done) || (doneList && state === confirmationStates.Done)) {
    <div class="list-container">
      @for (element of (state !== confirmationStates.Done ? data.list : doneList); track element) {
        <div>
          - {{ element | translate }}
        </div>
      }
    </div>
  }
  @if (data.lowerText && state !== confirmationStates.Done) {
    <div class="text-container">
      {{ data.lowerText | translate }}
    </div>
  }

  <div class="buttons">
    @if (data.cancelButtonText && state !== confirmationStates.Done) {
      <app-button
        #cancelButton
        color="accent"
        (action)="closeModal()"
        >
        {{ data.cancelButtonText | translate }}
      </app-button>
    }
    <app-button
      #confirmButton
      color="primary"
      (action)="state === confirmationStates.Asking ? sendOperationAcceptedEvent() : closeModal()">
      {{ (state !== confirmationStates.Done ? data.confirmButtonText : 'confirmation.close') | translate }}
    </app-button>
  </div>
</app-dialog>
`;

// angular:jit:style:src/app/components/layout/confirmation/confirmation.component.scss
var confirmation_component_default2 = "/* src/app/components/layout/confirmation/confirmation.component.scss */\n.text-container {\n  word-break: break-word;\n}\n.list-container {\n  font-size: 14px;\n  margin: 10px;\n  color: #215f9e;\n  word-break: break-word;\n}\n.buttons {\n  margin-top: 15px;\n  text-align: right;\n}\n.buttons app-button {\n  margin-left: 5px;\n}\n/*# sourceMappingURL=confirmation.component.css.map */\n";

// src/app/components/layout/confirmation/confirmation.component.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var ConfirmationStates;
(function(ConfirmationStates2) {
  ConfirmationStates2["Asking"] = "Asking";
  ConfirmationStates2["Processing"] = "Processing";
  ConfirmationStates2["Done"] = "Done";
})(ConfirmationStates || (ConfirmationStates = {}));
var ConfirmationComponent = class ConfirmationComponent2 {
  constructor(dialogRef, data) {
    this.dialogRef = dialogRef;
    this.data = data;
    this.disableDismiss = false;
    this.state = ConfirmationStates.Asking;
    this.confirmationStates = ConfirmationStates;
    this.operationAccepted = new EventEmitter();
    this.disableDismiss = !!data.disableDismiss;
    this.dialogRef.disableClose = this.disableDismiss;
  }
  ngAfterViewInit() {
    if (this.data.cancelButtonText) {
      setTimeout(() => this.cancelButton.focus());
    } else {
      setTimeout(() => this.confirmButton.focus());
    }
  }
  ngOnDestroy() {
    this.operationAccepted.complete();
  }
  closeModal() {
    this.dialogRef.close();
  }
  sendOperationAcceptedEvent() {
    this.operationAccepted.emit();
  }
  /**
   * Puts the modal window in the state in which it is waiting for confirmation.
   * @param newData New configuration for the modal window.
   */
  showAsking(newData) {
    if (newData) {
      this.data = newData;
    }
    this.state = ConfirmationStates.Asking;
    this.confirmButton.reset();
    this.disableDismiss = false;
    this.dialogRef.disableClose = this.disableDismiss;
    if (this.cancelButton) {
      this.cancelButton.showEnabled();
    }
  }
  showProcessing() {
    this.state = ConfirmationStates.Processing;
    this.disableDismiss = true;
    this.confirmButton.showLoading();
    if (this.cancelButton) {
      this.cancelButton.showDisabled();
    }
  }
  /**
   * Use only after the operation is done or receiving an error.
   * @param newTitle New title for the modal window.
   * @param newText New main text for the modal window.
   * @param newList New optional list to show below the main text.
   */
  showDone(newTitle, newText, newList = null) {
    if (newTitle) {
      this.doneTitle = newTitle;
    } else {
      this.doneTitle = this.data.headerText;
    }
    this.doneText = newText;
    this.doneList = newList;
    this.confirmButton.reset();
    setTimeout(() => this.confirmButton.focus());
    this.state = ConfirmationStates.Done;
    this.dialogRef.disableClose = false;
    this.disableDismiss = false;
  }
  static {
    this.ctorParameters = () => [
      { type: MatDialogRef },
      { type: void 0, decorators: [{ type: Inject, args: [MAT_DIALOG_DATA] }] }
    ];
  }
  static {
    this.propDecorators = {
      cancelButton: [{ type: ViewChild, args: ["cancelButton"] }],
      confirmButton: [{ type: ViewChild, args: ["confirmButton"] }],
      operationAccepted: [{ type: Output }]
    };
  }
};
ConfirmationComponent = __decorate([
  Component({
    selector: "app-confirmation",
    template: confirmation_component_default,
    standalone: false,
    changeDetection: ChangeDetectionStrategy.OnPush,
    styles: [confirmation_component_default2]
  })
], ConfirmationComponent);

// src/app/utils/generalUtils.ts
var GeneralUtils = class {
  /**
   * Opens a modal window requesting confirmation from the user and returns a reference to it.
   */
  static createConfirmationDialog(dialog, text) {
    const confirmationData = {
      text,
      headerText: "confirmation.header-text",
      confirmButtonText: "confirmation.confirm-button",
      cancelButtonText: "confirmation.cancel-button",
      disableDismiss: false
    };
    const config = new MatDialogConfig();
    config.data = confirmationData;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;
    return dialog.open(ConfirmationComponent, config);
  }
  /**
   * Checks the tag of a node, to know if the node is updatable via API calls.
   */
  static checkIfTagIsUpdatable(tag) {
    if (tag === void 0 || tag === null || tag.toUpperCase() === "Windows".toUpperCase() || tag.toUpperCase() === "Win".toUpperCase() || tag.toUpperCase() === "Mac".toUpperCase() || tag.toUpperCase() === "Macos".toUpperCase() || tag.toUpperCase() === "Mac OS".toUpperCase() || tag.toUpperCase() === "Darwin".toUpperCase()) {
      return false;
    }
    return true;
  }
  /**
   * Checks the tag of a node, to know if the terminal window can be openned for it.
   */
  static checkIfTagCanOpenterminal(tag) {
    if (tag === void 0 || tag === null || tag.toUpperCase() === "Windows".toUpperCase() || tag.toUpperCase() === "Win".toUpperCase()) {
      return false;
    }
    return true;
  }
  /**
   * Validates an IPv4 address.
   */
  static checkIfIpValidOrEmpty(value) {
    if (!value) {
      return true;
    }
    const parts = value.split(".");
    if (parts.length !== 4) {
      return false;
    }
    for (const part of parts) {
      const number = Number.parseInt(part, 10);
      if (isNaN(number) || number + "" !== part || number < 0 || number > 255) {
        return false;
      }
    }
    return true;
  }
};

// src/app/utils/pty-url.ts
function ptyUrl(key, query = "") {
  const url = new URL("pty/" + key + query, document.baseURI);
  if (url.host === "localhost:4200") {
    url.host = "127.0.0.1:8000";
  }
  return url.href;
}

export {
  homeTabsData,
  GeneralUtils,
  ptyUrl
};
//# debugId=6aeeaba6-02e8-5630-8b7d-fa2377019d66
//# sourceMappingURL=chunk-KWVFHEEW.js.map
