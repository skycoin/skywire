import { Component, Inject } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA, MatDialog, MatDialogConfig } from '@angular/material/dialog';

import { AppConfig } from 'src/app/app.config';
import { Route } from 'src/app/app.datatypes';
import { StorageService } from 'src/app/services/storage.service';

/**
 * Modal window for showing the details of a route.
 */
@Component({
  selector: 'app-route-details',
  templateUrl: './route-details.component.html',
  styleUrls: ['./route-details.component.scss']
})
export class RouteDetailsComponent {
  routeRule: Route;

  /**
   * Map with the types of route rules that the hypervisor can return and are known by this app.
   */
  private ruleTypes = new Map<number, string>([
    [0, 'App'],
    [1, 'Forward'],
    [2, 'Intermediary forward']
  ]);

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, route: Route): MatDialogRef<RouteDetailsComponent, any> {
    const config = new MatDialogConfig();
    config.data = route;
    config.autoFocus = false;
    config.width = AppConfig.largeModalWidth;

    return dialog.open(RouteDetailsComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) data: Route,
    public dialogRef: MatDialogRef<RouteDetailsComponent>,
    private storageService: StorageService,
  ) {
    this.routeRule = data;
  }

  getRuleTypeName(type: number): string {
    if (this.ruleTypes.has(type)) {
      return this.ruleTypes.get(type);
    }

    return type.toString();
  }

  closePopup() {
    this.dialogRef.close();
  }

  /**
   * Gets the label the user has set for an ID or pk.
   */
  getLabel(id: string) {
    const label = this.storageService.getLabelInfo(id);

    if (label) {
      return ' (' + label.label + ')';
    }

    return '';
  }
}
