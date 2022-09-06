import { Component, OnInit, Inject, OnDestroy } from '@angular/core';
import { UntypedFormBuilder, UntypedFormGroup } from '@angular/forms';
import { MAT_DIALOG_DATA, MatDialogRef, MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { Subscription } from 'rxjs';

import { AppConfig } from 'src/app/app.config';

/**
 * Result returned when a value is selected with LogFilterComponent.
 */
export interface LogsFilter {
  /**
   * Text of the selected option, for using with the "translate" pipe.
   */
  text: string;
  /**
   * Number of days from now of the selected option. It is -1 for the
   * "show all" option.
   */
  days: number;
}

/**
 * Modal window for selecting the initial date of the log messages. The date is
 * indicated in days from now. It returns -1 for the "show all" option.
 */
@Component({
  selector: 'app-log-filter',
  templateUrl: './log-filter.component.html',
  styleUrls: ['./log-filter.component.scss']
})
export class LogFilterComponent implements OnInit, OnDestroy {
  filters: LogsFilter[];
  form: UntypedFormGroup;

  private formSubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, currentFilter: LogsFilter): MatDialogRef<LogFilterComponent, any> {
    const config = new MatDialogConfig();
    config.data = currentFilter;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(LogFilterComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: LogsFilter,
    public dialogRef: MatDialogRef<LogFilterComponent>,
    private formBuilder: UntypedFormBuilder,
  ) { }

  ngOnInit() {
    this.filters = [
      {
        text: 'apps.log.filter.7-days',
        days: 7
      },
      {
        text: 'apps.log.filter.1-month',
        days: 30
      },
      {
        text: 'apps.log.filter.3-months',
        days: 90
      },
      {
        text: 'apps.log.filter.6-months',
        days: 180
      },
      {
        text: 'apps.log.filter.1-year',
        days: 365
      },
      {
        text: 'apps.log.filter.all',
        days: -1
      }
    ];


    this.form = this.formBuilder.group({
      filter: [this.data.days],
    });

    this.formSubscription = this.form.get('filter').valueChanges.subscribe(days => {
      this.dialogRef.close(this.filters.find(filter => filter.days === days));
    });
  }

  ngOnDestroy() {
    this.formSubscription.unsubscribe();
  }
}
