import { Component, Input } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';

import { Node } from '../../../../../app.datatypes';
import { EditLabelComponent } from 'src/app/components/layout/edit-label/edit-label.component';
import { NodeComponent } from '../../node.component';
import TimeUtils, { ElapsedTime } from 'src/app/utils/timeUtils';
import { LabeledElementTypes, StorageService } from 'src/app/services/storage.service';

/**
 * Shows the basic info of a node.
 */
@Component({
  selector: 'app-node-info-content',
  templateUrl: './node-info-content.component.html',
  styleUrls: ['./node-info-content.component.scss']
})
export class NodeInfoContentComponent {
  @Input() set nodeInfo(val: Node) {
    this.node = val;
    this.timeOnline = TimeUtils.getElapsedTime(val.seconds_online);
  }

  node: Node;
  timeOnline: ElapsedTime;

  constructor(
    private dialog: MatDialog,
    public storageService: StorageService,
  ) { }

  showEditLabelDialog() {
    let labelInfo =  this.storageService.getLabelInfo(this.node.local_pk);
    if (!labelInfo) {
      labelInfo = {
        id: this.node.local_pk,
        label: '',
        identifiedElementType: LabeledElementTypes.Node,
      };
    }

    EditLabelComponent.openDialog(this.dialog, labelInfo).afterClosed().subscribe((changed: boolean) => {
      if (changed) {
        NodeComponent.refreshCurrentDisplayedData();
      }
    });
  }
}
