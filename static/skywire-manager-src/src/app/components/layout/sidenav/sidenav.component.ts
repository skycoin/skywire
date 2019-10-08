import {
  Component,
  HostListener,
  OnInit,
  TemplateRef
} from '@angular/core';
import { SidenavService } from '../../../services/sidenav.service';

@Component({
  selector: 'app-sidenav',
  templateUrl: './sidenav.component.html',
  styleUrls: ['./sidenav.component.scss']
})
export class SidenavComponent implements OnInit {
  menuVisible = true;
  template: TemplateRef<any>;

  constructor(
    private sidenavService: SidenavService,
  ) { }

  ngOnInit() {
    this.sidenavService.getTemplate().subscribe(content => {
      setTimeout(() => this.template = content);
    });

    this.updateMenuVisibility();
  }

  toggleMenu() {
    this.menuVisible = !this.menuVisible;
  }

  @HostListener('window:resize')
  onWindowResize() {
    this.updateMenuVisibility();
  }

  private updateMenuVisibility() {
    this.menuVisible = !window.matchMedia('(max-width: 768px)').matches;
  }
}
