import { Component } from '@angular/core';
import { TabButtonData } from '../../layout/tab-bar/tab-bar.component';
import { AuthService } from '../../../services/auth.service';
import { Router } from '@angular/router';
import { ErrorsnackbarService } from '../../../services/errorsnackbar.service';
import { TranslateService } from '@ngx-translate/core';

@Component({
  selector: 'app-settings',
  templateUrl: './settings.component.html',
  styleUrls: ['./settings.component.scss']
})
export class SettingsComponent {
  tabsData: TabButtonData[] = [];

  constructor(
    private authService: AuthService,
    private router: Router,
    private errorSnackBar: ErrorsnackbarService,
    private translate: TranslateService,
  ) {
    this.tabsData = [
      {
        icon: 'view_headline',
        label: 'nodes.title',
        linkParts: ['/nodes'],
      },
      {
        icon: 'settings',
        label: 'settings.title',
        linkParts: ['/settings'],
      }
    ];
  }

  logout() {
    this.authService.logout().subscribe(
      () => this.router.navigate(['login']),
      () => this.errorSnackBar.open(this.translate.instant('nodes.logout-error'))
    );
  }
}
