import { Component, OnInit } from '@angular/core';
import {
  FormBuilder,
  FormControl,
  FormGroup,
  Validators
} from '@angular/forms';
import { TranslateService } from '@ngx-translate/core';

@Component({
  selector: 'app-language',
  templateUrl: './language.component.html',
  styleUrls: ['./language.component.scss']
})
export class LanguageComponent implements OnInit {
  form: FormGroup;

  readonly languages = [
    { name: 'English', value: 'en' },
  ];

  constructor(
    private formBuilder: FormBuilder,
    private translate: TranslateService,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      'language': ['en', Validators.required],
    });

    this.form.valueChanges.subscribe(({language}) => {
      this.translate.use(language);
      this.translate.setDefaultLang(language);
    });
  }
}
