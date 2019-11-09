import { Component, OnInit, Inject, OnDestroy } from '@angular/core';
import { FormBuilder, FormGroup } from '@angular/forms';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material';
import { Subscription } from 'rxjs';

export interface LogsFilter {
  text: string;
  days: number;
}

@Component({
  selector: 'app-log-filter',
  templateUrl: './log-filter.component.html',
  styleUrls: ['./log-filter.component.scss']
})
export class LogFilterComponent implements OnInit, OnDestroy {
  filters: LogsFilter[];
  form: FormGroup;

  private formSubscription: Subscription;

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: LogsFilter,
    private dialogRef: MatDialogRef<LogFilterComponent>,
    private formBuilder: FormBuilder,
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
      'filter': [this.data.days],
    });

    this.formSubscription = this.form.get('filter').valueChanges.subscribe(days => {
      this.dialogRef.close(this.filters.find(filter => filter.days === days));
    });
  }

  ngOnDestroy() {
    this.formSubscription.unsubscribe();
  }
}
