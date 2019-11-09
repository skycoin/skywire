import { Component, OnInit } from '@angular/core';
import {
  FormBuilder,
  FormGroup,
  Validators
} from '@angular/forms';
import { StorageService } from '../../../../services/storage.service';

@Component({
  selector: 'app-refresh-rate',
  templateUrl: './refresh-rate.component.html',
  styleUrls: ['./refresh-rate.component.scss']
})
export class RefreshRateComponent implements OnInit {
  form: FormGroup;

  readonly timesList = ['3', '5', '10', '15', '30', '60', '90', '150', '300'];

  constructor(
    private formBuilder: FormBuilder,
    private storageService: StorageService,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      'refreshRate': [this.storageService.getRefreshTime().toString(), Validators.required],
    });

    this.form.get('refreshRate').valueChanges.subscribe(refreshRate => {
      this.storageService.setRefreshTime(refreshRate);
    });
  }
}
