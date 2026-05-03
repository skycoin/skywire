import { Component, OnInit, OnDestroy } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { ActivatedRoute } from '@angular/router';
import { UntypedFormBuilder, UntypedFormGroup, Validators } from '@angular/forms';
import { Subscription, of } from 'rxjs';
import { catchError } from 'rxjs/operators';

import { NodeComponent } from '../node.component';
import { LabeledElementTypes, StorageService } from 'src/app/services/storage.service';
import { NodeService } from 'src/app/services/node.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';

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
  rewardsAddress = '';

  history: RewardDay[] = [];
  loading = false;
  days = 30;
  total = 0;
  errorMsg = '';

  // Address management (moved here from the Info tab so the Rewards
  // tab is the single surface for everything reward-related).
  showRewardForm = false;
  rewardForm: UntypedFormGroup;
  showRewardRules = false;
  rewardRules: string | null = null;

  private nodeSub: Subscription;
  private dataSub: Subscription;
  private saveRewardsSubscription: Subscription;
  private rewardRulesSubscription: Subscription;

  constructor(
    private http: HttpClient,
    private route: ActivatedRoute,
    private nodeComponent: NodeComponent,
    private storageService: StorageService,
    private nodeService: NodeService,
    private snackbarService: SnackbarService,
    private formBuilder: UntypedFormBuilder,
  ) {
    this.rewardForm = this.formBuilder.group({
      address: ['', Validators.compose([Validators.minLength(20), Validators.maxLength(112)])],
    });
  }

  ngOnInit() {
    this.pk = this.nodeComponent.node?.localPk || this.route.snapshot.parent?.paramMap.get('key') || '';
    const labelInfo = this.storageService.getLabelInfo(this.pk);
    this.label = labelInfo?.label || '';
    // Pull the current reward address from the live node feed.
    this.nodeSub = NodeComponent.currentNode.subscribe(node => {
      this.rewardsAddress = node?.rewardsAddress || '';
    });
    this.loadHistory();
  }

  ngOnDestroy() {
    this.nodeSub?.unsubscribe();
    this.dataSub?.unsubscribe();
    this.saveRewardsSubscription?.unsubscribe();
    this.rewardRulesSubscription?.unsubscribe();
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

  /** True when the configured reward address is actually a BIP44
   *  extended public key (xpub...). The Skycoin block explorer
   *  doesn't accept xpubs, so the template hides the explorer link. */
  get rewardsAddressIsXpub(): boolean {
    return !!this.rewardsAddress && this.rewardsAddress.startsWith('xpub');
  }

  toggleRewardForm() {
    this.showRewardForm = !this.showRewardForm;
    if (this.showRewardForm) {
      this.rewardForm.get('address').setValue(this.rewardsAddress || '');
    }
  }

  submitRewardAddress() {
    if (!this.rewardForm.valid) { return; }
    const newAddr = (this.rewardForm.get('address').value || '').trim();
    const op = newAddr
      ? this.nodeService.setRewardsAddress(this.pk, newAddr)
      : this.nodeService.deleteRewardsAddress(this.pk);
    this.saveRewardsSubscription = op.subscribe({
      next: () => {
        this.snackbarService.showDone('rewards-address-config.done');
        this.showRewardForm = false;
        NodeComponent.refreshCurrentDisplayedData();
      },
      error: (err: OperationError) => {
        err = processServiceError(err);
        this.snackbarService.showError(err);
      },
    });
  }

  toggleRewardRules() {
    this.showRewardRules = !this.showRewardRules;
    if (this.showRewardRules && this.rewardRules === null) {
      this.rewardRulesSubscription = this.nodeService.getRewardRules().subscribe(
        (text: string) => { this.rewardRules = text || ''; },
        () => { this.rewardRules = ''; this.snackbarService.showError('common.loading-error'); },
      );
    }
  }
}
