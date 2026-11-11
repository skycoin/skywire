import { Component, OnInit, OnDestroy, ChangeDetectionStrategy, ChangeDetectorRef } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { TranslateService } from '@ngx-translate/core';
import { ActivatedRoute } from '@angular/router';
import { UntypedFormBuilder, UntypedFormGroup, Validators } from '@angular/forms';
import { Subscription, of } from 'rxjs';
import { catchError, debounceTime } from 'rxjs/operators';

import { NodeComponent } from '../node.component';
import { LabeledElementTypes, StorageService } from 'src/app/services/storage.service';
import { NodeService } from 'src/app/services/node.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';
import { needsAcceptRisk, rewardAddressValidator } from 'src/app/utils/reward-address';

interface RewardDay {
  date: string;
  amount: number;
  share: number;
  sent: boolean;
  txid?: string;
}

// The first addresses an xpub derives, or why it derives none.
interface XpubAddresses {
  xpub: string;
  addresses: string[];
  error: string;
}

@Component({
  selector: 'app-node-rewards',
  templateUrl: './node-rewards.component.html',
  styleUrls: ['./node-rewards.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class NodeRewardsComponent implements OnInit, OnDestroy {
  pk = '';
  label = '';
  rewardsAddress = '';
  // Set when the reward address has spent, so its public key is on chain.
  exposure: { spent: boolean; balance: string } | null = null;
  private exposureAddress = '';
  // Where the rewards of the set xpub, and of an xpub typed in the form, go.
  xpubAddresses: XpubAddresses | null = null;
  formXpubAddresses: XpubAddresses | null = null;

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

  private nodeSub!: Subscription;
  private dataSub!: Subscription;
  private saveRewardsSubscription!: Subscription;
  private rewardRulesSubscription!: Subscription;
  private exposureSub?: Subscription;
  private xpubSub?: Subscription;
  private formXpubSub?: Subscription;
  private formChangesSub?: Subscription;
  private riskSub?: Subscription;

  constructor(
    private http: HttpClient,
    private route: ActivatedRoute,
    private nodeComponent: NodeComponent,
    private storageService: StorageService,
    private nodeService: NodeService,
    private snackbarService: SnackbarService,
    private formBuilder: UntypedFormBuilder,
    private changeDetectorRef: ChangeDetectorRef,
    private translate: TranslateService,
  ) {
    this.rewardForm = this.formBuilder.group({
      address: ['', Validators.compose([
        Validators.minLength(20), Validators.maxLength(112), rewardAddressValidator(() => !!this.rewardForm?.get('acceptRisk')!.value),
      ])],
      acceptRisk: [false],
    });
    this.riskSub = this.rewardForm.get('acceptRisk')!.valueChanges.subscribe(() => {
      this.rewardForm.get('address')!.updateValueAndValidity();
      this.changeDetectorRef.markForCheck();
    });
    this.formChangesSub = this.rewardForm.get('address')!.valueChanges.pipe(debounceTime(300)).subscribe(value => {
      this.formXpubSub?.unsubscribe();
      this.formXpubSub = this.loadXpubAddresses((value || '').trim(), r => this.formXpubAddresses = r);
      this.changeDetectorRef.markForCheck();
    });
  }

  ngOnInit() {
    this.pk = this.nodeComponent.node?.localPk || this.route.snapshot.parent?.paramMap.get('key') || '';
    const labelInfo = this.storageService.getLabelInfo(this.pk);
    this.label = labelInfo?.label || '';
    // Pull the current reward address from the live node feed.
    this.nodeSub = NodeComponent.currentNode.subscribe(node => {
      this.rewardsAddress = node?.rewardsAddress || '';
      this.checkExposure();
      this.changeDetectorRef.markForCheck();
    });
    this.loadHistory();
  }

  ngOnDestroy() {
    this.nodeSub?.unsubscribe();
    this.dataSub?.unsubscribe();
    this.saveRewardsSubscription?.unsubscribe();
    this.rewardRulesSubscription?.unsubscribe();
    this.exposureSub?.unsubscribe();
    this.xpubSub?.unsubscribe();
    this.formXpubSub?.unsubscribe();
    this.riskSub?.unsubscribe();
    this.formChangesSub?.unsubscribe();
  }

  // Asks the hypervisor for the first addresses of xpub and passes them to set,
  // or null when xpub is not an xpub.
  private loadXpubAddresses(xpub: string, set: (r: XpubAddresses | null) => void): Subscription | undefined {
    if (!xpub.startsWith('xpub')) {
      set(null);
      this.changeDetectorRef.markForCheck();

      return undefined;
    }

    return this.nodeService.getRewardXpubAddresses(xpub).subscribe({
      next: (r: any) => {
        set({ xpub: xpub, addresses: r?.addresses || [], error: '' });
        this.changeDetectorRef.markForCheck();
      },
      error: (err: OperationError) => {
        err = processServiceError(err);
        set({ xpub: xpub, addresses: [], error: err.originalServerErrorMsg || err.translatableErrorMsg });
        this.changeDetectorRef.markForCheck();
      },
    });
  }

  // Asks the visor whether the reward address has sent coins before. Only runs when the address changes.
  private checkExposure() {
    if (this.rewardsAddress === this.exposureAddress) {
      return;
    }
    this.exposureAddress = this.rewardsAddress;
    this.exposure = null;
    this.exposureSub?.unsubscribe();
    this.xpubSub?.unsubscribe();
    this.xpubSub = this.loadXpubAddresses(this.rewardsAddress, r => this.xpubAddresses = r);
    if (!this.rewardsAddress || this.rewardsAddressIsXpub) {
      return;
    }
    this.exposureSub = this.nodeService.getRewardsAddressExposure(this.pk, this.rewardsAddress).subscribe({
      next: (e: any) => {
        this.exposure = e;
        this.changeDetectorRef.markForCheck();
      },
      // A node that cannot be reached leaves the warning off rather than guessing.
      error: () => {
        this.exposureAddress = '';
      },
    });
  }

  loadHistory() {
    if (!this.pk) {
 return; 
}
    this.loading = true;
    this.errorMsg = '';
    this.dataSub = this.http.get<any>(
      `api/rewards/skycoin-rewards/visor/${this.pk}?days=${this.days}`
    ).pipe(
      catchError(err => {
        this.errorMsg = 'node.details.rewards-info.history.load-error';

        return of({ history: [] });
      })
    ).subscribe(resp => {
      this.history = resp?.history || [];
      this.total = this.history.reduce((sum: number, d: RewardDay) => sum + (d.amount || 0), 0);
      this.loading = false;
      this.changeDetectorRef.markForCheck();
    });
  }

  changeDays(days: number) {
    this.days = days;
    this.loadHistory();
  }

  formatDate(dateStr: string): string {
    const d = new Date(dateStr + 'T00:00:00');

    return d.toLocaleDateString(this.translate.getCurrentLang() || 'en', { month: 'short', day: 'numeric', year: 'numeric' });
  }

  statusClass(day: RewardDay): string {
    if (!day.amount || day.amount === 0) {
 return 'no-reward'; 
}

    return day.sent ? 'sent' : 'pending';
  }

  statusText(day: RewardDay): string {
    if (!day.amount || day.amount === 0) {
 return 'node.details.rewards-info.history.status-none'; 
}

    return day.sent ? 'node.details.rewards-info.history.status-sent' : 'node.details.rewards-info.history.status-pending';
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
      this.rewardForm.get('address')!.setValue(this.rewardsAddress || '');
      this.rewardForm.get('acceptRisk')!.setValue(false);
    }
  }

  submitRewardAddress() {
    if (!this.rewardForm.valid) {
 return; 
}
    const newAddr = (this.rewardForm.get('address')!.value || '').trim();
    const op = newAddr
      ? this.nodeService.setRewardsAddress(this.pk, newAddr, needsAcceptRisk(newAddr, this.rewardForm.get('acceptRisk')!.value))
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
        (text: string) => {
 this.rewardRules = text || ''; 
          this.changeDetectorRef.markForCheck();
        },
        () => {
 this.rewardRules = ''; this.snackbarService.showError('common.loading-error'); 
          this.changeDetectorRef.markForCheck();
        },
      );
    }
  }
}
