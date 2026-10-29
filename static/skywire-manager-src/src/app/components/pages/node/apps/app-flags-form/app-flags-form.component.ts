import {
  Component, OnInit, OnChanges, OnDestroy, SimpleChanges, Input, Output, EventEmitter,
  ChangeDetectionStrategy, ChangeDetectorRef } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { Subscription } from 'rxjs';
import { take } from 'rxjs/operators';

import { AppsService } from 'src/app/services/apps.service';
import { Node, Transport } from 'src/app/app.datatypes';
import { NodeComponent } from '../../node.component';
import { SelectProxyServerComponent } from '../../skysocks-tab/select-proxy-server/select-proxy-server.component';
import {
  AppFlag, AppFlagsSchema, FlagState, ServerType, buildFlagArgs, flagError, isServerFlag, parseFlagArgs } from './app-flags';

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
  // Servers the client app connects to, which enables the server picker.
  @Input() serverType: ServerType | '' = '';
  // Each increase opens the server picker once the flags are loaded.
  @Input() pickServer = 0;
  @Output() serverChosen = new EventEmitter<string>();

  flags: AppFlag[] = [];
  state: { [name: string]: FlagState } = {};
  loading = false;
  error = '';

  private lastArgs = '';
  private sub?: Subscription;
  private transports: Transport[] = [];

  constructor(private appsService: AppsService, private changeDetectorRef: ChangeDetectorRef, private dialog: MatDialog) {}

  ngOnInit() {
    this.loading = true;
    NodeComponent.currentNode.pipe(take(1)).subscribe((node: Node) => {
      if (!node) {
        this.loading = false;

        return;
      }
      this.transports = node.transports || [];
      this.sub = this.appsService.getAppFlags(node.localPk, this.appName).subscribe({
        next: (schema: AppFlagsSchema) => {
          this.loading = false;
          this.flags = (schema && schema.flags) || [];
          this.readArgs();
          this.changeDetectorRef.markForCheck();
          if (this.pickServer > 0) {
            this.openPicker();
          }
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
    if (changes['pickServer'] && !changes['pickServer'].firstChange && this.pickServer > 0 && this.flags.length) {
      this.openPicker();
    }
  }

  canPick(f: AppFlag): boolean {
    return !!this.serverType && isServerFlag(f);
  }

  private openPicker() {
    const f = this.flags.find(fl => this.canPick(fl));
    if (f) {
      this.chooseServer(f);
    }
  }

  /** Opens the server picker and writes the chosen key into the flag. */
  chooseServer(f: AppFlag) {
    SelectProxyServerComponent.openDialog(this.dialog, {
      clientName: this.appName,
      serviceType: this.serverType || 'proxy',
      transports: this.transports,
    }).subscribe(pk => {
      if (!pk) {
        return;
      }
      this.setValue(f, pk);
      this.changeDetectorRef.markForCheck();
      this.serverChosen.emit(pk);
    });
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
