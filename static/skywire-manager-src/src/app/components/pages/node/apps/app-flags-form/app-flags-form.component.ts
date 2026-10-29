import {
  Component, OnInit, OnChanges, OnDestroy, SimpleChanges, Input, Output, EventEmitter,
  ChangeDetectionStrategy, ChangeDetectorRef } from '@angular/core';
import { Subscription } from 'rxjs';
import { take } from 'rxjs/operators';

import { AppsService } from 'src/app/services/apps.service';
import { Node } from 'src/app/app.datatypes';
import { NodeComponent } from '../../node.component';
import { AppFlag, AppFlagsSchema, FlagState, buildFlagArgs, flagError, parseFlagArgs } from './app-flags';

/**
 * A form generated from the flags an app accepts. It edits the args string
 * of the settings panel it sits in, so saving goes through that panel.
 */
@Component({
  selector: 'app-app-flags-form',
  templateUrl: './app-flags-form.component.html',
  styleUrls: ['./app-flags-form.component.scss'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class AppFlagsFormComponent implements OnInit, OnChanges, OnDestroy {
  @Input() appName = '';
  @Input() args = '';
  @Output() argsChange = new EventEmitter<string>();
  // True while every field holds a value the app accepts.
  @Output() validChange = new EventEmitter<boolean>();

  flags: AppFlag[] = [];
  state: { [name: string]: FlagState } = {};
  loading = false;
  error = '';

  private lastArgs = '';
  private sub?: Subscription;

  constructor(private appsService: AppsService, private changeDetectorRef: ChangeDetectorRef) {}

  ngOnInit() {
    this.loading = true;
    NodeComponent.currentNode.pipe(take(1)).subscribe((node: Node) => {
      if (!node) {
        this.loading = false;

        return;
      }
      this.sub = this.appsService.getAppFlags(node.localPk, this.appName).subscribe({
        next: (schema: AppFlagsSchema) => {
          this.loading = false;
          this.flags = (schema && schema.flags) || [];
          this.readArgs();
          this.changeDetectorRef.markForCheck();
        },
        error: (e: any) => {
          this.loading = false;
          this.error = (e && e.message) ? e.message : 'Failed to fetch flags';
          this.changeDetectorRef.markForCheck();
        },
      });
    });
  }

  ngOnChanges(changes: SimpleChanges) {
    // Follow edits made in the raw args field, but not our own echo.
    if (changes['args'] && this.args !== this.lastArgs && this.flags.length) {
      this.readArgs();
    }
  }

  ngOnDestroy() {
    if (this.sub) {
      this.sub.unsubscribe();
    }
  }

  private readArgs() {
    this.lastArgs = this.args;
    this.state = parseFlagArgs(this.args, this.flags).state;
    // Deferred: this can run inside the parent's change detection.
    Promise.resolve().then(() => {
      this.validChange.emit(this.valid);
      this.changeDetectorRef.markForCheck();
    });
  }

  get valid(): boolean {
    return this.flags.every(f => !this.errorFor(f));
  }

  errorFor(f: AppFlag): string {
    const st = this.state[f.name];

    return st && st.set ? flagError(f, st.value) : '';
  }

  setValue(f: AppFlag, value: string | boolean) {
    this.state[f.name] = { value: String(value), set: true };
    this.emit();
  }

  reset(f: AppFlag) {
    this.state[f.name] = { value: f.default, set: false };
    this.emit();
  }

  private emit() {
    this.lastArgs = buildFlagArgs(this.args, this.flags, this.state);
    this.args = this.lastArgs;
    this.argsChange.emit(this.lastArgs);
    this.validChange.emit(this.valid);
  }
}
