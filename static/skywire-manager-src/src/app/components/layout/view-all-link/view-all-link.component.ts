import { Component, Input } from '@angular/core';

@Component({
  selector: 'app-view-all-link',
  templateUrl: './view-all-link.component.html',
  styleUrls: ['./view-all-link.component.scss']
})
export class ViewAllLinkComponent {
  @Input() numberOfElements = 0;
  @Input() linkParts = [''];
}
