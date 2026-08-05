import { NgModule } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule, ReactiveFormsModule } from '@angular/forms';
import { RouterModule } from '@angular/router';

import { MatButtonModule } from '@angular/material/button';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MatDialogModule } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatMenuModule } from '@angular/material/menu';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { MatSelectModule } from '@angular/material/select';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { MatSnackBarModule } from '@angular/material/snack-bar';
import { MatTabsModule } from '@angular/material/tabs';
import { MatTooltipModule } from '@angular/material/tooltip';
import { DragDropModule } from '@angular/cdk/drag-drop';
import { TranslateModule } from '@ngx-translate/core';

import { AutoScalePipe } from '../pipes/auto-scale.pipe';
import { ClipboardDirective } from '../directives/clipboard.directive';

import { BundleMountComponent } from './bundle-mount/bundle-mount.component';

import { BulkRewardAddressChangerComponent } from '../components/layout/bulk-reward-address-changer/bulk-reward-address-changer.component';
import { ButtonComponent } from '../components/layout/button/button.component';
import { ConfirmationComponent } from '../components/layout/confirmation/confirmation.component';
import { CopyToClipboardTextComponent } from '../components/layout/copy-to-clipboard-text/copy-to-clipboard-text.component';
import { DialogComponent } from '../components/layout/dialog/dialog.component';
import { EditLabelComponent } from '../components/layout/edit-label/edit-label.component';
import { FiltersSelectionComponent } from '../components/layout/filters-selection/filters-selection.component';
import { LabeledElementTextComponent } from '../components/layout/labeled-element-text/labeled-element-text.component';
import { LangButtonComponent } from '../components/layout/lang-button/lang-button.component';
import { LineChartComponent } from '../components/layout/line-chart/line-chart.component';
import { LoadingIndicatorComponent } from '../components/layout/loading-indicator/loading-indicator.component';
import { PaginatorComponent } from '../components/layout/paginator/paginator.component';
import { RefreshButtonComponent } from '../components/layout/refresh-button/refresh-button.component';
import { SelectLanguageComponent } from '../components/layout/select-language/select-language.component';
import { SelectOptionComponent } from '../components/layout/select-option/select-option.component';
import { SnackbarComponent } from '../components/layout/snack-bar/snack-bar.component';
import { TabSelectorComponent } from '../components/layout/tab-selector/tab-selector.component';
import { TopBarComponent } from '../components/layout/top-bar/top-bar.component';
import { TruncatedTextComponent } from '../components/layout/truncated-text/truncated-text.component';
import { UpdateAllComponent } from '../components/layout/update-all/update-all.component';
import { UpdateComponent } from '../components/layout/update/update.component';
import { ViewAllLinkComponent } from '../components/layout/view-all-link/view-all-link.component';

/**
 * Cross-feature UI building blocks shared across the SPA.
 *
 * This module owns the reusable *layout* components (buttons, dialogs, the top
 * bar, paginator, charts, …), the app-wide pipe/directive, and re-exports the
 * common Angular / Material modules those templates depend on. It exists so that
 * feature areas can be split into their own (eventually lazy-loaded) NgModules
 * without each having to re-declare the shared pieces or re-import the same
 * Material list — a feature module just imports `SharedModule` and gets both the
 * shared components and the common modules (CommonModule, forms, Material,
 * translation) transitively.
 *
 * Root-only modules (BrowserModule, BrowserAnimationsModule, AppRoutingModule)
 * intentionally stay in AppModule and are NOT re-exported here.
 */
const SHARED_ANGULAR_MODULES = [
  CommonModule,
  FormsModule,
  ReactiveFormsModule,
  // Bare RouterModule for the router directives (routerLink, routerLinkActive)
  // used by shared components like ViewAllLinkComponent. The route table
  // (RouterModule.forRoot) stays in AppModule's AppRoutingModule.
  RouterModule,
  // Bare TranslateModule (the `translate` pipe/directive) — the root provider
  // config lives in AppModule's AppTranslationModule (forRoot). Re-exporting
  // only the bare module keeps future lazy feature modules on the single root
  // TranslateService instead of spawning duplicate services via forRoot.
  TranslateModule,
  MatSnackBarModule,
  MatDialogModule,
  MatFormFieldModule,
  MatInputModule,
  MatTabsModule,
  MatButtonModule,
  MatIconModule,
  MatTooltipModule,
  MatMenuModule,
  MatProgressBarModule,
  MatSelectModule,
  MatProgressSpinnerModule,
  MatCheckboxModule,
  MatSlideToggleModule,
  DragDropModule,
];

const SHARED_DECLARATIONS = [
  AutoScalePipe,
  ClipboardDirective,
  BundleMountComponent,
  BulkRewardAddressChangerComponent,
  ButtonComponent,
  ConfirmationComponent,
  CopyToClipboardTextComponent,
  DialogComponent,
  EditLabelComponent,
  FiltersSelectionComponent,
  LabeledElementTextComponent,
  LangButtonComponent,
  LineChartComponent,
  LoadingIndicatorComponent,
  PaginatorComponent,
  RefreshButtonComponent,
  SelectLanguageComponent,
  SelectOptionComponent,
  SnackbarComponent,
  TabSelectorComponent,
  TopBarComponent,
  TruncatedTextComponent,
  UpdateAllComponent,
  UpdateComponent,
  ViewAllLinkComponent,
];

@NgModule({
  declarations: SHARED_DECLARATIONS,
  imports: SHARED_ANGULAR_MODULES,
  exports: [...SHARED_ANGULAR_MODULES, ...SHARED_DECLARATIONS],
})
export class SharedModule { }
