import { Component, OnInit, OnDestroy } from '@angular/core';
import { UntypedFormBuilder, UntypedFormGroup } from '@angular/forms';
import { Subscription } from 'rxjs';

import { StorageService } from '../../../../services/storage.service';
import { SnackbarService } from 'src/app/services/snackbar.service';

/**
 * Allows to change the frequency of the automatic data refresing.
 */
@Component({
  selector: 'app-refresh-rate',
  templateUrl: './refresh-rate.component.html',
  styleUrls: ['./refresh-rate.component.scss']
})
export class RefreshRateComponent implements OnInit, OnDestroy {
  form: UntypedFormGroup;

  // Options in seconds.
  readonly timesList = ['3', '5', '10', '15', '30', '60', '90', '150', '300'];

  private subscription: Subscription;

  constructor(
    private formBuilder: UntypedFormBuilder,
    private storageService: StorageService,
    private snackbarService: SnackbarService,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      refreshRate: [this.storageService.getRefreshTime().toString()],
    });

    this.subscription = this.form.get('refreshRate').valueChanges.subscribe(refreshRate => {
      this.storageService.setRefreshTime(refreshRate);
      this.snackbarService.showDone('settings.refresh-rate-confirmation');
    });
  }

  ngOnDestroy() {
    this.subscription.unsubscribe();
  }
}
