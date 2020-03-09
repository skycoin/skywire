import { Component, Inject, OnInit } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef, MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { AppConfig } from 'src/app/app.config';

@Component({
  selector: 'app-configuration',
  templateUrl: './configuration.component.html',
  styleUrls: ['./configuration.component.scss']
})
export class ConfigurationComponent implements OnInit {

  public static openDialog(dialog: MatDialog, data: any): MatDialogRef<ConfigurationComponent, any> {
    const config = new MatDialogConfig();
    config.data = data;
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;

    return dialog.open(ConfigurationComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: any,
    public dialogRef: MatDialogRef<ConfigurationComponent>,
  ) { }

  ngOnInit() { }
}
