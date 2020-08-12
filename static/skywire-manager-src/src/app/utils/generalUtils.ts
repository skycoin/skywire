import { MatDialogRef, MatDialogConfig, MatDialog } from '@angular/material/dialog';

import { ConfirmationComponent, ConfirmationData } from '../components/layout/confirmation/confirmation.component';
import { AppConfig } from '../app.config';

/**
 * Represents a possible value of a property. It allows to separate the actual value of the
 * property and the text that will be shown in the UI.
 */
export interface PrintableLabel {
  /**
   * Actual value.
   */
  value: string;
  /**
   * Value to be shown in the UI. Preferably a var for the translate pipe.
   */
  label: string;
}

/**
 * Helper functions for the app.
 */
export default class GeneralUtils {

  /**
   * Opens a modal window requesting confirmation from the user and returns a reference to it.
   */
  static createConfirmationDialog(dialog: MatDialog, text: string): MatDialogRef<ConfirmationComponent, any> {
    const confirmationData: ConfirmationData = {
      text: text,
      headerText: 'confirmation.header-text',
      confirmButtonText: 'confirmation.confirm-button',
      cancelButtonText: 'confirmation.cancel-button',
      disableDismiss: true,
    };

    const config = new MatDialogConfig();
    config.data = confirmationData;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(ConfirmationComponent, config);
  }
}
