import { Component, Input, Output, EventEmitter, OnDestroy } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';

import { SelectOptionComponent, SelectableOption } from '../select-option/select-option.component';
import { StorageService, LabelInfo, LabeledElementTypes } from 'src/app/services/storage.service';
import { ClipboardService } from 'src/app/services/clipboard.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { EditLabelComponent } from '../edit-label/edit-label.component';

/**
 * Shows the id of an element and a label identifying it. An icon is shown at the end of the text,
 * to indicate the user that the text can be copied by clicking for showing options. This
 * component allows to change the label and to copy the id. This component can show truncated
 * text for the id, case in which a tooltip allows the user to see the complete id.
 */
@Component({
  selector: 'app-labeled-element-text',
  templateUrl: './labeled-element-text.component.html',
  styleUrls: ['./labeled-element-text.component.scss']
})
export class LabeledElementTextComponent implements OnDestroy {
  private idInternal: string;
  /**
   * Id of the element to show.
   */
  @Input() set id(val: string) {
    this.idInternal = val;

    // Get the associated label.
    this.labelInfo = this.storageService.getLabelInfo(val);
    this.labelText = this.labelInfo && this.labelInfo.label ? this.labelInfo.label : null;

    this.isLocalNode = this.savedVisibleLocalNodes.has(val);
  }
  get id(): string { return this.idInternal ? this.idInternal : ''; }

  /**
   * Indicates if the text with the id must be truncated if it is too long.
   */
  @Input() public short = false;
  /**
   * Number of characters at the left and right of the id that will be shown if "short" is
   * "true". Example: if the id is "123456789" and this var is set to 2, the component will
   * show "12...89". If the id has a length less than shortTextLength * 2, the whole id
   * is shown.
   */
  @Input() shortTextLength = 5;
  /**
   * Type of the element to which the id corresponds.
   */
  @Input() elementType: LabeledElementTypes = LabeledElementTypes.Node;

  /**
   * Event for when the label is changed.
   */
  @Output() labelEdited = new EventEmitter();

  // Label to show. It is null if no label has been set for the id.
  labelText: string;
  // Info associated to the label. It is null if no label has been set for the id.
  labelInfo: LabelInfo;
  // If the id corresponds to a local node connected to the hypervisor.
  isLocalNode = false;
  // Set with the IDs of all visible (not set as hidden) nodes this app "remembers" as connected
  // to the hypervisor instance.
  savedVisibleLocalNodes = this.storageService.getSavedVisibleLocalNodes();

  constructor(
    private dialog: MatDialog,
    private storageService: StorageService,
    private clipboardService: ClipboardService,
    private snackbarService: SnackbarService,
  ) { }

  ngOnDestroy() {
    this.labelEdited.complete();
  }

  processClick() {
    // Options for the options modal window.
    const options: SelectableOption[] = [
      {
        icon: 'filter_none',
        label: 'labeled-element.copy',
      },
      {
        icon: 'edit',
        label: 'labeled-element.edit-label',
      }
    ];

    // Show the options modal window.
    SelectOptionComponent.openDialog(this.dialog, options, 'common.options').afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        // Copy the id.
        if (this.clipboardService.copy(this.id)) {
          this.snackbarService.showDone('copy.copied');
        }
      } else {
        // Params for the edit label modal window.
        if (selectedOption === 2) {
          let labelInfo =  this.labelInfo;
          if (!labelInfo) {
            labelInfo = {
              id: this.id,
              label: '',
              identifiedElementType: this.elementType,
            };
          }

          // Show the edit label modal window.
          EditLabelComponent.openDialog(this.dialog, labelInfo).afterClosed().subscribe((changed: boolean) => {
            if (changed) {
              this.labelEdited.emit();
            }
          });
        }
      }
    });
  }
}
