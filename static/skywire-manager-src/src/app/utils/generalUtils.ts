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

  /**
   * Checks the tag of a node, to know if the node is updatable via API calls.
   */
  static checkIfTagIsUpdatable(tag: string) {
    if (
      tag === undefined ||
      tag === null ||
      tag.toUpperCase() === 'Windows'.toUpperCase() ||
      tag.toUpperCase() === 'Win'.toUpperCase() ||
      tag.toUpperCase() === 'Mac'.toUpperCase() ||
      tag.toUpperCase() === 'Macos'.toUpperCase() ||
      tag.toUpperCase() === 'Mac OS'.toUpperCase() ||
      tag.toUpperCase() === 'Darwin'.toUpperCase()
    ) {
      return false;
    }

    return true;
  }

  /**
   * Checks the tag of a node, to know if the terminal window can be openned for it.
   */
   static checkIfTagCanOpenterminal(tag: string) {
    if (
      tag === undefined ||
      tag === null ||
      tag.toUpperCase() === 'Windows'.toUpperCase() ||
      tag.toUpperCase() === 'Win'.toUpperCase()
    ) {
      return false;
    }

    return true;
  }
}
