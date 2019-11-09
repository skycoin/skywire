import { MatDialogRef, MatDialogConfig, MatDialog } from '@angular/material';
import { ConfirmationComponent, ConfirmationData } from '../components/layout/confirmation/confirmation.component';

export default class GeneralUtils {
  static createDeleteConfirmation(dialog: MatDialog, text: string): MatDialogRef<ConfirmationComponent, any> {
    const confirmationData: ConfirmationData = {
      text: text,
      headerText: 'confirmation.header-text',
      confirmButtonText: 'confirmation.confirm-button',
      cancelButtonText: 'confirmation.cancel-button',
      disableDismiss: true,
    };

    return dialog.open(ConfirmationComponent, <MatDialogConfig> {
      width: '450px',
      data: confirmationData,
      autoFocus: false,
    });
  }
}
