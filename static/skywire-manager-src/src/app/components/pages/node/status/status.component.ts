import {Component, Input, OnChanges, OnInit} from '@angular/core';
import {TranslateService} from '@ngx-translate/core';
import {isDiscovered} from '../../../../utils/nodeUtils';

@Component({
  selector: 'app-status',
  templateUrl: './status.component.html',
  styleUrls: ['./status.component.scss']
})
export class StatusComponent implements OnInit, OnChanges {
  @Input() nodeData;
  onlineTooltip: string | any;

  constructor(private translate: TranslateService) { }

  ngOnInit() {
    this.getOnlineTooltip();
  }

  ngOnChanges(): void {
    this.getOnlineTooltip();
  }

  get isDiscovered(): boolean {
    return isDiscovered(this.nodeData.info);
  }

  getOnlineTooltip(): void {
    this.translate.get(this.isDiscovered ? 'node.statuses.discovered-tooltip' : 'node.statuses.online-tooltip')
      .subscribe((text: string) => this.onlineTooltip = text);
  }
}
