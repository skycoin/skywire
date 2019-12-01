import { Component, OnInit } from '@angular/core';
import { FormBuilder, FormGroup } from '@angular/forms';

import { StorageService } from '../../../../services/storage.service';

/**
 * Allows to change the frequency of the automatic data refresing.
 */
@Component({
  selector: 'app-refresh-rate',
  templateUrl: './refresh-rate.component.html',
  styleUrls: ['./refresh-rate.component.scss']
})
export class RefreshRateComponent implements OnInit {
  form: FormGroup;

  // Options in seconds.
  readonly timesList = ['3', '5', '10', '15', '30', '60', '90', '150', '300'];

  constructor(
    private formBuilder: FormBuilder,
    private storageService: StorageService,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      'refreshRate': [this.storageService.getRefreshTime().toString()],
    });

    this.form.get('refreshRate').valueChanges.subscribe(refreshRate => {
      this.storageService.setRefreshTime(refreshRate);
    });
  }
}
