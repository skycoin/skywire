import { Component, Input, Output, EventEmitter, OnInit, OnDestroy } from '@angular/core';
import { of, Subscription } from 'rxjs';
import { delay } from 'rxjs/operators';
import { MatDialog } from '@angular/material/dialog';
import { Router } from '@angular/router';

import { LanguageService, LanguageData } from 'src/app/services/language.service';
import { SelectableOption, SelectOptionComponent } from '../select-option/select-option.component';
import { SelectLanguageComponent } from '../select-language/select-language.component';
import { VpnClientService } from 'src/app/services/vpn-client.service';
import { VpnHelpers } from '../../vpn/vpn-helpers';

/**
 * Properties of a tab shown in TopBarComponent.
 */
export interface TabButtonData {
  /**
   * Array with the parts of the route that must be openned by the tab. This array must the
   * same that would be usend in the "routerLink" property of an <a> tag.
   */
  linkParts: string[];
  icon: string;
  label: string;
  /**
   * If true, the button is not visible in the "lg" window size and larger.
   */
  onlyIfLessThanLg?: boolean;
}

/**
 * Properties of an option shown in TopBarComponent.
 */
export interface MenuOptionData {
  /**
   * Text that will be shown in the button.
   */
  name: string;
  /**
   * Icon that will be shown in the button.
   */
  icon: string;
  /**
   * Unique string to identify the option if the user selects it.
   */
  actionName: string;
  disabled?: boolean;
}

/**
 * Data about the current vpn protection.
 */
interface VpnData {
  showStarted: boolean;
  latency: number;
  uploadSpeed: number;
  downloadSpeed: Number;
}

/**
 * Top bar shown by most of the pages. It shows a list of tabs, a button for refreshing the
 * currently displayed data and a menu button. The design is responsive, but it is advisable
 * to use only a maximum of 3 tabs with short texts, to avoid some problems.
 */
@Component({
  selector: 'app-top-bar',
  templateUrl: './top-bar.component.html',
  styleUrls: ['./top-bar.component.scss']
})
export class TopBarComponent implements OnInit, OnDestroy {
  /**
   * Deactivates the mouse events.
   */
  @Input() disableMouse = false;

  /**
   * Elements to show in the title. The idea is to show the path of the current page.
   */
  @Input() titleParts: string[];
  /**
   * List with the tabs to show.
   */
  @Input() tabsData: TabButtonData[];
  /**
   * Index of the currently selected tab.
   */
  @Input() selectedTabIndex = 0;
  /**
   * List with the options to show.
   */
  @Input() optionsData: MenuOptionData[];
  /**
   * Text for the translatable pipe to be shown in the return button. The return button is only
   * shown if this var has a valid value. If the return button is pressed, the optionSelected
   * event is emited with null as value.
   */
  @Input() returnText: string;

  /**
   * Seconds since the last time the data was updated.
   */
  @Input() secondsSinceLastUpdate: number;
  /**
   * Makes the refresh button to show a loading animation.
   */
  @Input() showLoading: boolean;
  /**
   * Makes the refresh button to show an alert icon, to inform that there was an error
   * updating the data. It also activates a tooltip in which he user can see how often
   * the system retries to get the data.
   */
  @Input() showAlert: boolean;
  /**
   * How often the system automatically refreshes the data, in seconds.
   */
  @Input() refeshRate = -1;
  @Input() showUpdateButton = true;

  /**
   * Public key of the local visor. If set, the vpn info is shown instead of the refresh button.
   */
  @Input() set localVpnKey(val: string) {
    this.localVpnKeyInternal = val;

    // Start or stop getting and showing the info about the vpn client app.
    if (val) {
      this.startGettingVpnInfo();
    } else {
      this.stopGettingVpnInfo();
    }
  }
  private localVpnKeyInternal = '';

  /**
   * Event for when the user presses the update button.
   */
  @Output() refreshRequested = new EventEmitter();
  /**
   * Event for when the user selects an option from the menu. It return the value of the
   * actionName property of the selected option or null, if the back button was pressed.
   */
  @Output() optionSelected = new EventEmitter<string>();

  hideLanguageButton = true;
  // Currently selecte language.
  language: LanguageData;

  // Data about the current state of the vpn client app.
  vpnData: VpnData;
  // If the vpn data must be shown.
  showVpnInfo = false;
  // If the state of the vpn client app has already been obtained for the first time.
  initialVpnStateObtained = false;
  // If the vpn client app was running or not the first time the state was obtained.
  firstVpnRunningCValue = false;
  // If the css class for animating the vpn state changes must be added. Must be true only after
  // the first state change was detected. From that moment, showing a new state will run
  // the animation.
  showVpnStateAnimation = false;
  // If the control with the animation for the vpn state that runs one time must me shown. For
  // running the animation again, this var is set to false and the to true again, after a moment.
  showVpnStateAnimatedDot = true;

  private langSubscriptionsGroup: Subscription[] = [];
  private vpnDataSubscription: Subscription;
  private showVpnStateAnimatedDotSubscription: Subscription;

  constructor(
    private languageService: LanguageService,
    private dialog: MatDialog,
    private router: Router,
    private vpnClientService: VpnClientService,
  ) { }

  ngOnInit() {
    this.langSubscriptionsGroup.push(this.languageService.currentLanguage.subscribe(lang => {
      this.language = lang;
    }));

    this.langSubscriptionsGroup.push(this.languageService.languages.subscribe(langs => {
      if (langs.length > 1) {
        this.hideLanguageButton = false;
      } else {
        this.hideLanguageButton = true;
      }
    }));
  }

  ngOnDestroy() {
    this.langSubscriptionsGroup.forEach(sub => sub.unsubscribe());
    this.refreshRequested.complete();
    this.optionSelected.complete();
    this.stopGettingVpnInfo();
  }

  // Start getting and showing the vpn info.
  startGettingVpnInfo() {
    this.showVpnInfo = true;
    this.vpnClientService.initialize(this.localVpnKeyInternal);

    this.vpnDataSubscription = this.vpnClientService.backendState.subscribe(data => {
      if (data) {
        this.vpnData = {
          showStarted: data.vpnClientAppData.running,
          latency: data.vpnClientAppData.connectionData ? data.vpnClientAppData.connectionData.latency : 0,
          downloadSpeed: data.vpnClientAppData.connectionData ? data.vpnClientAppData.connectionData.downloadSpeed : 0,
          uploadSpeed: data.vpnClientAppData.connectionData ? data.vpnClientAppData.connectionData.uploadSpeed : 0,
        };

        // Include the VPN state change animation only after the first state change is detected.
        if (!this.initialVpnStateObtained) {
          this.initialVpnStateObtained = true;
          this.firstVpnRunningCValue = data.vpnClientAppData.running;
        } else {
          if (this.firstVpnRunningCValue !== data.vpnClientAppData.running) {
            this.showVpnStateAnimation = true;
          }
        }

        // Make the state dot blink after receiving an update.
        this.showVpnStateAnimatedDot = false;
        if (this.showVpnStateAnimatedDotSubscription) {
          this.showVpnStateAnimatedDotSubscription.unsubscribe();
        }
        this.showVpnStateAnimatedDotSubscription = of(0).pipe(delay(1)).subscribe(() => this.showVpnStateAnimatedDot = true);
      }
    });
  }

  // Stop getting and showing the vpn info.
  stopGettingVpnInfo() {
    this.showVpnInfo = false;

    if (this.vpnDataSubscription) {
      this.vpnDataSubscription.unsubscribe();
    }
  }

  // Gets the name of the translatable var that must be used for showing a latency value. This
  // allows to add the correct measure suffix.
  getLatencyValueString(latency: number): string {
    return VpnHelpers.getLatencyValueString(latency);
  }

  // Gets the string value to show in the UI a latency value with an adecuate number of decimals.
  // This function converts the value from ms to segs, if appropriate, so the value must be shown
  // using the var returned by getLatencyValueString.
  getPrintableLatency(latency: number): string {
    return VpnHelpers.getPrintableLatency(latency);
  }

  // Called when the user selects an option from the menu.
  requestAction(name: string) {
    this.optionSelected.emit(name);
  }

  // Opens the language selection modal window.
  openLanguageWindow() {
    SelectLanguageComponent.openDialog(this.dialog);
  }

  sendRefreshEvent() {
    this.refreshRequested.emit();
  }

  openTabSelector() {
    // Create an option for every tab.
    const options: SelectableOption[] = [];
    this.tabsData.forEach(tab => {
      options.push({
        label: tab.label,
        icon: tab.icon,
      });
    });

    // Open the option selection modal window.
    SelectOptionComponent.openDialog(this.dialog, options, 'tabs-window.title').afterClosed().subscribe((result: number) => {
      if (result) {
        result -= 1;
        if (result !== this.selectedTabIndex) {
          this.router.navigate(this.tabsData[result].linkParts);
        }
      }
    });
  }
}
