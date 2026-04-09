import { Component, OnInit, OnDestroy } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { ActivatedRoute } from '@angular/router';
import { Subscription } from 'rxjs';
import { catchError } from 'rxjs/operators';
import { of } from 'rxjs';

import { NodeComponent } from '../node.component';
import { LabeledElementTypes, StorageService } from 'src/app/services/storage.service';

interface RewardDay {
  date: string;
  amount: number;
  share: number;
  sent: boolean;
  txid?: string;
}

@Component({
  selector: 'app-node-rewards',
  templateUrl: './node-rewards.component.html',
  styleUrls: ['./node-rewards.component.scss'],
  standalone: false
})
export class NodeRewardsComponent implements OnInit, OnDestroy {
  pk = '';
  label = '';
  history: RewardDay[] = [];
  loading = false;
  days = 30;
  total = 0;
  errorMsg = '';

  private routeSub: Subscription;
  private dataSub: Subscription;

  constructor(
    private http: HttpClient,
    private route: ActivatedRoute,
    private nodeComponent: NodeComponent,
    private storageService: StorageService,
  ) {}

  ngOnInit() {
    this.pk = this.nodeComponent.node?.localPk || this.route.snapshot.parent?.paramMap.get('key') || '';
    const labelInfo = this.storageService.getLabelInfo(this.pk);
    this.label = labelInfo?.label || '';
    this.loadHistory();
  }

  ngOnDestroy() {
    this.routeSub?.unsubscribe();
    this.dataSub?.unsubscribe();
  }

  loadHistory() {
    if (!this.pk) { return; }
    this.loading = true;
    this.errorMsg = '';
    this.dataSub = this.http.get<any>(
      `/api/rewards/skycoin-rewards/visor/${this.pk}?days=${this.days}`
    ).pipe(
      catchError(err => {
        this.errorMsg = 'Failed to load reward data';
        return of({ history: [] });
      })
    ).subscribe(resp => {
      this.history = resp?.history || [];
      this.total = this.history.reduce((sum: number, d: RewardDay) => sum + (d.amount || 0), 0);
      this.loading = false;
    });
  }

  changeDays(days: number) {
    this.days = days;
    this.loadHistory();
  }

  formatDate(dateStr: string): string {
    const d = new Date(dateStr + 'T00:00:00');
    return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
  }

  statusClass(day: RewardDay): string {
    if (!day.amount || day.amount === 0) { return 'no-reward'; }
    return day.sent ? 'sent' : 'pending';
  }

  statusText(day: RewardDay): string {
    if (!day.amount || day.amount === 0) { return 'No reward'; }
    return day.sent ? 'Sent' : 'Pending';
  }
}
