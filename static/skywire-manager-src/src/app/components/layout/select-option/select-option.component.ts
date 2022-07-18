import { Component, Inject } from '@angular/core';
import { MatDialogRef, MatDialog, MatDialogConfig, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { AppConfig } from 'src/app/app.config';

/**
 * Data for the options that are shown by SelectOptionComponent.
 */
export interface SelectableOption {
  /**
   * Name of the material icon to show.
   */
  icon?: string;
  /**
   * Label to show.
   */
  label: string;
}

// Data received by SelectOptionComponent.
interface ComponentData {
  options: SelectableOption[];
  title: string;
}

/**
 * Modal window for allowing the user to select an option. When the user selects an option,
 * the modal window is closed and the number of the selected option (counting from 1) is
 * returned in the "afterClosed" envent.
 */
@Component({
  selector: 'app-select-option',
  templateUrl: './select-option.component.html',
  styleUrls: ['./select-option.component.scss'],
})
export class SelectOptionComponent {
  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, optionsToShow: SelectableOption[], title: string): MatDialogRef<SelectOptionComponent, any> {
    const config = new MatDialogConfig();
    config.data = {
      options: optionsToShow,
      title: title,
    };
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(SelectOptionComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) public data: ComponentData,
    public dialogRef: MatDialogRef<SelectOptionComponent>,
  ) { }

  closePopup(selectedOption: number) {
    this.dialogRef.close(selectedOption);
  }
}
