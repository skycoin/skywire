import { Component, OnInit, OnDestroy } from '@angular/core';
import { TabButtonData } from '../../layout/tab-bar/tab-bar.component';
import { AuthService } from '../../../services/auth.service';
import { Router } from '@angular/router';
import { SnackbarService } from '../../../services/snackbar.service';
import { Subscription } from 'rxjs';
import { SidenavService } from 'src/app/services/sidenav.service';

@Component({
  selector: 'app-settings',
  templateUrl: './settings.component.html',
  styleUrls: ['./settings.component.scss']
})
export class SettingsComponent implements OnInit, OnDestroy {
  tabsData: TabButtonData[] = [];

  private menuSubscription: Subscription;

  constructor(
    private authService: AuthService,
    private router: Router,
    private snackbarService: SnackbarService,
    private sidenavService: SidenavService,
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

  ngOnInit() {
    setTimeout(() => {
      this.menuSubscription = this.sidenavService.setContents([
        {
          name: 'nodes.logout',
          actionName: 'logout',
          icon: 'power_settings_new'
        }], null).subscribe(actionName => {
          if (actionName === 'logout') {
            this.logout();
          }
        }
      );
    });
  }

  ngOnDestroy() {
    if (this.menuSubscription) {
      this.menuSubscription.unsubscribe();
    }
  }

  logout() {
    this.authService.logout().subscribe(
      () => this.router.navigate(['login']),
      () => this.snackbarService.showError('nodes.logout-error')
    );
  }
}
