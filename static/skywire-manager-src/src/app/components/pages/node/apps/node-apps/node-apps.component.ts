import { Component, Input } from '@angular/core';
import {Node, NodeFeedback} from '../../../../../app.datatypes';

@Component({
  selector: 'app-node-apps',
  templateUrl: './node-apps.component.html',
  styleUrls: ['./node-apps.component.scss']
})
export class NodeAppsComponent {
  @Input() node: Node;
  @Input() apps = [];
  @Input() nodeInfo;

  getApp(name: string) {
    return (this.apps || []).find(app => app.attributes.some(attr => attr === name));
  }

  getFeedback(appName: string) {
    const appKey = this.getApp(appName) ? this.getApp(appName).key : null;
    let feedback: NodeFeedback;
    if (appKey && this.nodeInfo && this.nodeInfo.app_feedbacks) {
      feedback = this.nodeInfo.app_feedbacks.find(fb => fb.key === appKey);
    }
    return feedback;
  }
}

