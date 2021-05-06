import { MatDialogRef, MatDialogConfig, MatDialog } from '@angular/material/dialog';

import { ConfirmationComponent, ConfirmationData } from '../components/layout/confirmation/confirmation.component';
import { AppConfig } from '../app.config';

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
