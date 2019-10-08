import { Component, OnInit, TemplateRef, ViewChild } from '@angular/core';
import { SidenavService } from '../../../../services/sidenav.service';

@Component({
  selector: 'app-sidenav-content',
  templateUrl: './sidenav-content.component.html',
  styleUrls: ['./sidenav-content.component.css']
})
export class SidenavContentComponent implements OnInit {
  @ViewChild('sidenav') sidenavContent: TemplateRef<any>;

  constructor(
    private sidenavService: SidenavService,
  ) { }

  ngOnInit() {
    this.sidenavService.setTemplate(this.sidenavContent);
  }
}
